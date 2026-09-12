package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// IsStreamResponse reports whether a response declares an SSE body.
func IsStreamResponse(response *http.Response) bool {
	return response != nil && strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
}

// StreamResponse consumes and closes an SSE body, delivering semantic events
// incrementally. The count includes only events accepted by the callback;
// downstream commitment must be tracked by the writer, not inferred from it.
func StreamResponse(format Format, response *http.Response, onEvent func(Event) error) (int, error) {
	if response == nil || response.Body == nil {
		return 0, &MalformedResponseError{Format: format, Err: errors.New("empty response")}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("upstream status %d", response.StatusCode)
	}
	if !IsStreamResponse(response) {
		return 0, &MalformedResponseError{Format: format, Err: errors.New("response is not server-sent events")}
	}
	if _, err := CodecFor(format); err != nil {
		return 0, err
	}
	decoder := streamDecoder{format: format, emit: onEvent}
	err := readSSE(response.Body, decoder.dispatch)
	if err != nil {
		if decoder.count > 0 {
			return decoder.count, &PartialResponseError{Err: err}
		}
		return 0, err
	}
	if decoder.count == 0 {
		return 0, &MalformedResponseError{Format: format, Err: errors.New("no valid semantic event")}
	}
	if !decoder.terminal {
		return decoder.count, &PartialResponseError{Err: errors.New("stream ended before terminal event")}
	}
	return decoder.count, nil
}

// readSSE owns framing only. JSON and protocol semantics belong to dispatch.
func readSSE(reader io.Reader, dispatch func(string, string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	name := ""
	var data []string
	flush := func() error {
		defer func() { name = ""; data = data[:0] }()
		if len(data) == 0 {
			return nil
		}
		return dispatch(name, strings.TrimSpace(strings.Join(data, "\n")))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data = append(data, value)
		}
	}
	// A read failure must not turn an incomplete frame into an accepted event.
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

type streamDecoder struct {
	format         Format
	emit           func(Event) error
	count          int
	terminal       bool
	responsesTools map[string]*responsesStreamTool
}

type responsesStreamTool struct {
	callID  string
	name    string
	args    string
	emitted int
}

func (d *streamDecoder) send(event Event) error {
	if err := d.emit(event); err != nil {
		return err
	}
	d.count++
	return nil
}

func (d *streamDecoder) dispatch(name, data string) error {
	if d.responsesTools == nil {
		d.responsesTools = make(map[string]*responsesStreamTool)
	}
	if d.terminal || data == "" {
		return nil
	}
	if data == "[DONE]" {
		if d.format == FormatChatCompletions {
			d.terminal = true
		}
		return nil
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &payload) != nil || payload == nil {
		return &MalformedResponseError{Format: d.format, Err: errors.New("invalid SSE JSON data")}
	}
	typ := stringValue(payload["type"])
	if typ == "" {
		typ = name
		payload["type"], _ = json.Marshal(typ)
	}
	if hasUsage(payload) {
		var usage Usage
		decodeUsage(payload, &usage)
		if err := d.send(Event{Type: EventUsage, Usage: &usage}); err != nil {
			return err
		}
	}
	if typ == "error" || name == "error" || (len(payload["error"]) > 0 && string(payload["error"]) != "null") || typ == "response.failed" || typ == "response.incomplete" {
		providerErr := ProviderError{Type: typ, Message: stringValue(payload["message"])}
		errorPayload := payload["error"]
		if nested, ok := rawObject(payload["response"]); ok {
			errorPayload = nested["error"]
		}
		if nested, ok := rawObject(errorPayload); ok {
			providerErr.Type = valueOr(stringValue(nested["type"]), typ)
			providerErr.Code = stringValue(nested["code"])
			providerErr.Message = stringValue(nested["message"])
		}
		if providerErr.Message == "" {
			providerErr.Message = valueOr(stringValue(errorPayload), typ)
		}
		if err := d.send(Event{Type: EventError, Error: &providerErr}); err != nil {
			return err
		}
		return fmt.Errorf("provider stream error: %s", providerErr.Message)
	}
	var event *Event
	switch d.format {
	case FormatChatCompletions:
		event = decodeChatStreamDelta(payload)
	case FormatResponses:
		event = d.decodeResponsesStreamDelta(payload)
		if typ == "response.completed" {
			d.terminal = true
			if response, err := decodeResponsesResponsePayload(payload); err == nil {
				event = &Event{Type: EventResponse, Response: &response}
			}
		}
	case FormatAnthropicMessages:
		event = decodeAnthropicStreamDelta(payload)
		if typ == "message_stop" {
			d.terminal = true
		}
	}
	if event != nil {
		return d.send(*event)
	}
	return nil
}

func (d *streamDecoder) decodeResponsesStreamDelta(payload map[string]json.RawMessage) *Event {
	typ := stringValue(payload["type"])
	if typ == "response.output_item.added" || typ == "response.output_item.done" {
		item, ok := rawObject(payload["item"])
		if !ok {
			return nil
		}
		itemType := stringValue(item["type"])
		if itemType != "function_call" && itemType != "custom_tool_call" {
			return nil
		}
		itemID := stringValue(item["id"])
		if itemID == "" {
			return nil
		}
		state := d.responsesTools[itemID]
		if state == nil {
			state = &responsesStreamTool{}
			d.responsesTools[itemID] = state
		}
		state.callID = stringValue(item["call_id"])
		if state.callID == "" {
			state.callID = itemID
		}
		state.name = stringValue(item["name"])
		if raw := item["arguments"]; len(raw) > 0 {
			state.args = string(jsonArgument(raw))
		}
		if typ == "response.output_item.added" && state.name != "" && state.emitted == 0 {
			state.emitted = len(state.args)
			return &Event{Type: EventToolCallDelta, ToolCall: &ToolCall{ID: state.callID, Type: "function", Name: state.name}}
		}
		return nil
	}
	if typ == "response.function_call_arguments.delta" || typ == "response.function_call_arguments.done" {
		itemID := stringValue(payload["item_id"])
		if itemID == "" {
			return nil
		}
		state := d.responsesTools[itemID]
		if state == nil {
			state = &responsesStreamTool{callID: itemID}
			d.responsesTools[itemID] = state
		}
		if typ == "response.function_call_arguments.delta" {
			state.args += stringValue(payload["delta"])
		} else {
			state.args = string(jsonArgument(payload["arguments"]))
		}
		if state.emitted > len(state.args) {
			return nil
		}
		fragment := state.args[state.emitted:]
		state.emitted = len(state.args)
		if state.name == "" {
			return nil
		}
		return &Event{Type: EventToolCallDelta, ToolCall: &ToolCall{ID: state.callID, Type: "function", Name: state.name, Arguments: json.RawMessage(fragment)}}
	}
	if typ == "response.output_text.delta" {
		if text := stringValue(payload["delta"]); text != "" {
			return &Event{Type: EventTextDelta, Text: text}
		}
		return nil
	}
	if typ == "response.reasoning_text.delta" || typ == "response.reasoning_summary_text.delta" {
		return &Event{Type: EventReasoningDelta, Reasoning: stringValue(payload["delta"])}
	}
	return nil
}
