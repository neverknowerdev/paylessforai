package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type responsesAdapter struct{}

func (responsesAdapter) Format() Format { return FormatResponses }
func (responsesAdapter) DecodeRequest(body []byte) (*Request, error) {
	return decodeRequestBody(FormatResponses, body, decodeResponsesOptions, knownKeys("max_output_tokens", "tool_choice", "parallel_tool_calls", "text", "reasoning"), decodeResponsesPayload)
}
func (responsesAdapter) EncodeRequest(req *Request) ([]byte, error) { return encodeResponses(req) }
func (responsesAdapter) DecodeResponse(resp *http.Response) (EventStream, error) {
	if resp != nil && resp.Body != nil && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return decodeResponsesStream(resp)
	}
	return decodeResponseFor(FormatResponses, resp, decodeResponsesResponsePayload, decodeResponsesStreamDelta)
}
func (responsesAdapter) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return encodeResponseFor(FormatResponses, events, dst, encodeResponsesResponse, encodeResponsesStreamEvent)
}

func decodeResponsesOptions(payload map[string]json.RawMessage) (RequestOptions, error) {
	options := decodeCommonOptions(payload)
	options.MaxOutputTokens = intPointer(payload, "max_output_tokens")
	options.StructuredOutput = decodeResponsesStructuredOutput(payload["text"])
	options.ToolChoice = decodeResponsesToolChoice(payload["tool_choice"])
	options.ParallelToolCall = boolPointer(payload, "parallel_tool_calls")
	options.Reasoning = decodeResponsesReasoning(payload["reasoning"])
	options.Text = decodeResponsesText(payload["text"])
	return options, nil
}

func decodeResponsesStructuredOutput(raw json.RawMessage) *StructuredOutput {
	var text map[string]json.RawMessage
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	var format map[string]json.RawMessage
	if json.Unmarshal(text["format"], &format) != nil {
		return nil
	}
	return structuredOutputFromMap(format)
}

func decodeResponsesReasoning(raw json.RawMessage) *ReasoningOptions {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	result := &ReasoningOptions{Effort: stringValue(value["effort"]), Summary: stringValue(value["summary"]), Context: stringValue(value["context"])}
	if raw := value["generate_summary"]; len(raw) > 0 {
		var enabled bool
		if json.Unmarshal(raw, &enabled) == nil {
			result.GenerateSummary = &enabled
		}
	}
	if result.Effort == "" && result.Summary == "" && result.Context == "" && result.GenerateSummary == nil {
		return nil
	}
	return result
}

func decodeResponsesText(raw json.RawMessage) *TextOptions {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	result := &TextOptions{Verbosity: stringValue(value["verbosity"])}
	if result.Verbosity == "" {
		return nil
	}
	return result
}

func decodeResponsesPayload(payload map[string]json.RawMessage, request *Request) error {
	if len(payload["input"]) == 0 {
		return errors.New("input is required")
	}
	if instructions := stringValue(payload["instructions"]); instructions != "" {
		request.Messages = append(request.Messages, Message{Role: RoleDeveloper, Content: []ContentBlock{{Type: "text", Text: instructions}}})
	}
	if input := stringValue(payload["input"]); input != "" {
		request.Messages = append(request.Messages, Message{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: input}}})
		decodeResponsesTools(payload["tools"], request)
		return nil
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(payload["input"], &input); err != nil {
		return err
	}
	for _, item := range input {
		typ := stringValue(item["type"])
		switch typ {
		case "message", "":
			role := Role(stringValue(item["role"]))
			if role == "" {
				role = RoleUser
			}
			blocks, err := decodeStringOrBlocks(item["content"], FormatResponses)
			if err != nil {
				return err
			}
			request.Messages = append(request.Messages, Message{Role: role, Content: blocks})
		case "function_call", "custom_tool_call":
			id := stringValue(item["call_id"])
			if id == "" {
				id = stringValue(item["id"])
			}
			request.Messages = append(request.Messages, Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Type: typ, Name: stringValue(item["name"]), Arguments: jsonArgument(item["arguments"])}}})
		case "function_call_output", "custom_tool_call_output":
			request.Messages = append(request.Messages, Message{Role: RoleTool, ToolCallID: stringValue(item["call_id"]), Content: []ContentBlock{{Type: "tool_result", Result: rawCopy(item["output"])}}})
		case "reasoning":
			var summary []map[string]json.RawMessage
			_ = json.Unmarshal(item["summary"], &summary)
			for _, value := range summary {
				if text := stringValue(value["text"]); text != "" {
					request.Messages = append(request.Messages, Message{Role: RoleAssistant, Reasoning: []ContentBlock{{Type: "reasoning", Text: text}}})
				}
			}
		default:
			return newIncompatibility(FormatResponses, "input item "+typ)
		}
	}
	decodeResponsesTools(payload["tools"], request)
	return nil
}

func decodeResponsesTools(raw json.RawMessage, request *Request) {
	var values []map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	for _, value := range values {
		var tool Tool
		_ = json.Unmarshal(value["type"], &tool.Type)
		_ = json.Unmarshal(value["name"], &tool.Name)
		_ = json.Unmarshal(value["description"], &tool.Description)
		tool.Parameters = ensureJSONObject(value["parameters"])
		if raw := value["strict"]; len(raw) > 0 {
			var strict bool
			if json.Unmarshal(raw, &strict) == nil {
				tool.Strict = &strict
			}
		}
		request.Tools = append(request.Tools, tool)
	}
}

func decodeResponsesToolChoice(raw json.RawMessage) *ToolChoice {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		return &ToolChoice{Mode: strings.ToLower(mode)}
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return &ToolChoice{Mode: "unsupported"}
	}
	choice := &ToolChoice{Mode: strings.ToLower(stringValue(value["type"]))}
	if choice.Mode == "function" {
		choice.Mode = "named"
		choice.Name = stringValue(value["name"])
	}
	if choice.Mode == "" {
		choice.Mode = "unsupported"
	}
	return choice
}

func encodeResponses(request *Request) ([]byte, error) {
	payload := map[string]any{"model": request.Model, "input": make([]any, 0), "stream": request.Options.Stream}
	input := payload["input"].([]any)
	for _, message := range request.Messages {
		switch message.Role {
		case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant:
			content, err := encodeResponsesContent(message.Role, message.Content)
			if err != nil {
				return nil, err
			}
			if len(message.Reasoning) > 0 {
				content = append(content, map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": blocksText(message.Reasoning)}}})
			}
			input = append(input, map[string]any{"type": "message", "role": string(message.Role), "content": content})
			for _, call := range message.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
			}
		case RoleTool:
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": blocksText(message.Content)})
		default:
			return nil, newIncompatibility(FormatResponses, "role "+string(message.Role))
		}
	}
	payload["input"] = input
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			item := map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": jsonOrEmpty(tool.Parameters)}
			if tool.Strict != nil {
				item["strict"] = *tool.Strict
			}
			tools = append(tools, item)
		}
		payload["tools"] = tools
	}
	if err := writeResponsesOptions(payload, request); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func writeResponsesOptions(payload map[string]any, request *Request) error {
	o := request.Options
	if err := validateResponsesOptions(o); err != nil {
		return err
	}
	if o.MaxOutputTokens != nil {
		payload["max_output_tokens"] = *o.MaxOutputTokens
	}
	if o.Temperature != nil {
		payload["temperature"] = *o.Temperature
	}
	if o.TopP != nil {
		payload["top_p"] = *o.TopP
	}
	if choice, err := encodeResponsesToolChoice(o.ToolChoice); err != nil {
		return err
	} else if choice != nil {
		payload["tool_choice"] = choice
	}
	if o.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *o.ParallelToolCall
	}
	text := map[string]any{}
	if output, err := encodeResponsesStructuredOutput(o.StructuredOutput); err != nil {
		return err
	} else if output != nil {
		text["format"] = output
	}
	if o.Text != nil && o.Text.Verbosity != "" {
		text["verbosity"] = o.Text.Verbosity
	}
	if len(text) > 0 {
		payload["text"] = text
	}
	if o.Reasoning != nil {
		reasoning := map[string]any{}
		if o.Reasoning.Effort != "" {
			reasoning["effort"] = o.Reasoning.Effort
		}
		if o.Reasoning.Summary != "" {
			reasoning["summary"] = o.Reasoning.Summary
		}
		if o.Reasoning.Context != "" {
			reasoning["context"] = o.Reasoning.Context
		}
		if o.Reasoning.GenerateSummary != nil {
			reasoning["generate_summary"] = *o.Reasoning.GenerateSummary
		}
		if len(reasoning) > 0 {
			payload["reasoning"] = reasoning
		}
	}
	copyExtensions(payload, request, FormatResponses)
	return nil
}

func validateResponsesOptions(o RequestOptions) error {
	if len(o.Stop) > 0 {
		return newIncompatibility(FormatResponses, "stop sequences")
	}
	if len(o.Modalities) > 0 {
		return newIncompatibility(FormatResponses, "output modalities")
	}
	if o.Reasoning != nil && o.Reasoning.Thinking {
		return newIncompatibility(FormatResponses, "Anthropic thinking")
	}
	return nil
}

func encodeResponsesToolChoice(choice *ToolChoice) (any, error) {
	if choice == nil {
		return nil, nil
	}
	switch choice.Mode {
	case "auto", "none", "required":
		return choice.Mode, nil
	case "named":
		return map[string]any{"type": "function", "name": choice.Name}, nil
	default:
		return nil, newIncompatibility(FormatResponses, "tool_choice")
	}
}

func encodeResponsesStructuredOutput(value *StructuredOutput) (any, error) {
	if value == nil {
		return nil, nil
	}
	if value.Type != "json_schema" && value.Type != "json_object" {
		return nil, newIncompatibility(FormatResponses, "structured output")
	}
	if value.Type == "json_object" {
		return map[string]any{"type": "json_object"}, nil
	}
	result := map[string]any{"type": "json_schema", "name": value.Name, "schema": jsonRawValue(value.Schema)}
	if value.Strict != nil {
		result["strict"] = *value.Strict
	}
	return result, nil
}

func encodeResponsesContent(role Role, blocks []ContentBlock) ([]any, error) {
	textType := "input_text"
	if role == RoleAssistant {
		textType = "output_text"
	}
	result := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			result = append(result, map[string]any{"type": textType, "text": block.Text})
		case "image":
			if url := mediaDataURL(block); url != "" {
				result = append(result, map[string]any{"type": "input_image", "image_url": url})
			} else {
				return nil, newIncompatibility(FormatResponses, "image")
			}
		case "audio":
			if block.Data != "" {
				result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": block.Data, "format": valueOr(block.MediaType, "wav")}})
			} else {
				return nil, newIncompatibility(FormatResponses, "audio")
			}
		case "file":
			result = append(result, map[string]any{"type": "input_file", "file_id": block.ID, "filename": block.FileName, "file_url": block.URL})
		case "reasoning":
			result = append(result, map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": block.Text}}})
		case "tool_result":
			result = append(result, map[string]any{"type": textType, "text": blocksText([]ContentBlock{block})})
		default:
			return nil, newIncompatibility(FormatResponses, block.Type)
		}
	}
	return result, nil
}

func decodeResponsesResponsePayload(payload map[string]json.RawMessage) (Response, error) {
	// Responses streaming lifecycle events wrap the completed response under
	// `response`; decode that object using the same response contract.
	if len(payload["output"]) == 0 && len(payload["output_text"]) == 0 {
		if nested, ok := rawObject(payload["response"]); ok {
			return decodeResponsesResponsePayload(nested)
		}
	}
	result := Response{ID: stringValue(payload["id"]), Model: stringValue(payload["model"]), Text: stringValue(payload["output_text"]), FinishReason: responsesFinishReason(stringValue(payload["status"]))}
	assistant := Message{Role: RoleAssistant}
	if len(payload["output"]) > 0 {
		var output []map[string]json.RawMessage
		if json.Unmarshal(payload["output"], &output) == nil {
			for _, item := range output {
				if typ := stringValue(item["type"]); typ == "function_call" || typ == "custom_tool_call" {
					id := stringValue(item["call_id"])
					if id == "" {
						id = stringValue(item["id"])
					}
					name := stringValue(item["name"])
					if strings.TrimSpace(name) == "" {
						return result, fmt.Errorf("responses tool call %s has no function name", id)
					}
					assistant.ToolCalls = append(assistant.ToolCalls, ToolCall{ID: id, Type: "function", Name: name, Arguments: jsonArgument(item["arguments"])})
					continue
				}
				role := stringValue(item["role"])
				if role == "" {
					role = "assistant"
				}
				blocks, _ := decodeStringOrBlocks(item["content"], FormatResponses)
				result.Messages = append(result.Messages, Message{Role: Role(role), Content: blocks})
			}
		}
	}
	if len(assistant.ToolCalls) > 0 {
		result.Messages = append(result.Messages, assistant)
	}
	if result.Text == "" {
		for _, message := range result.Messages {
			result.Text += blocksText(message.Content)
		}
	}
	decodeUsage(payload, &result.Usage)
	if len(result.Messages) == 0 && result.Text == "" {
		return result, errors.New("response has no semantic content")
	}
	return result, nil
}

func responsesFinishReason(status string) string {
	switch status {
	case "completed":
		return "stop"
	case "incomplete":
		return "length"
	default:
		return status
	}
}

type responsesToolState struct {
	itemID  string
	call    ToolCall
	args    string
	emitted int
}

// decodeResponsesStream joins Responses lifecycle events by output item ID.
// The item ID is the key used by argument deltas; call.ID is the stable ID
// exposed to the downstream protocol.
func decodeResponsesStream(resp *http.Response) (EventStream, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return EventStream{}, &PartialResponseError{Err: err}
	}
	states := make(map[string]*responsesToolState)
	events := make([]Event, 0)
	var completed *Response
	valid := false
	terminal := false
	nextToolIndex := 0
	emitTool := func(state *responsesToolState) {
		if state.call.Name == "" || state.call.ID == "" || state.emitted > len(state.args) {
			return
		}
		fragment := state.args[state.emitted:]
		events = append(events, Event{Type: EventToolCallDelta, ToolCall: &ToolCall{ID: state.call.ID, Type: "function", Name: state.call.Name, Arguments: json.RawMessage(fragment)}})
		state.emitted = len(state.args)
		valid = true
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &payload) != nil {
			continue
		}
		typ := stringValue(payload["type"])
		switch typ {
		case "response.output_text.delta":
			if delta := stringValue(payload["delta"]); delta != "" {
				events = append(events, Event{Type: EventTextDelta, Text: delta})
				valid = true
			}
		case "response.output_item.added", "response.output_item.done":
			item, ok := rawObject(payload["item"])
			if !ok {
				continue
			}
			if itemType := stringValue(item["type"]); itemType != "function_call" && itemType != "custom_tool_call" {
				continue
			}
			itemID := stringValue(item["id"])
			if itemID == "" {
				itemID = stringValue(payload["item_id"])
			}
			if itemID == "" {
				continue
			}
			state := states[itemID]
			if state == nil {
				state = &responsesToolState{itemID: itemID, call: ToolCall{Index: nextToolIndex}}
				nextToolIndex++
				states[itemID] = state
			}
			if callID := stringValue(item["call_id"]); callID != "" {
				state.call.ID = callID
			}
			if state.call.ID == "" {
				state.call.ID = itemID
			}
			state.call.Type = "function"
			if name := stringValue(item["name"]); name != "" {
				state.call.Name = name
			}
			if raw := item["arguments"]; len(raw) > 0 {
				full := jsonArgument(raw)
				if len(full) > 0 {
					reconcileResponseArguments(state, string(full))
				}
			}
			if typ == "response.output_item.added" {
				emitTool(state)
			}
			if typ == "response.output_item.done" {
				emitTool(state)
			}
			if typ == "response.output_item.done" && state.call.Name == "" {
				return EventStream{}, fmt.Errorf("responses tool item %s has no function name", itemID)
			}
		case "response.function_call_arguments.delta":
			itemID := stringValue(payload["item_id"])
			if itemID == "" {
				continue
			}
			state := states[itemID]
			if state == nil {
				state = &responsesToolState{itemID: itemID, call: ToolCall{Index: nextToolIndex}}
				nextToolIndex++
				states[itemID] = state
			}
			fragment := stringValue(payload["delta"])
			state.args += fragment
			if state.call.ID == "" {
				state.call.ID = itemID
			}
			emitTool(state)
		case "response.function_call_arguments.done":
			itemID := stringValue(payload["item_id"])
			state := states[itemID]
			if state != nil {
				reconcileResponseArguments(state, string(jsonArgument(payload["arguments"])))
				emitTool(state)
			}
		case "response.completed":
			value, err := decodeResponsesResponsePayload(payload)
			if err != nil {
				return EventStream{Events: events, Stream: true}, &MalformedResponseError{Format: FormatResponses, Err: err}
			}
			completed = &value
			events = append(events, Event{Type: EventResponse, Response: completed})
			valid = true
			terminal = true
		case "response.incomplete":
			value, err := decodeResponsesResponsePayload(payload)
			if err != nil {
				return EventStream{Events: events, Stream: true}, &MalformedResponseError{Format: FormatResponses, Err: err}
			}
			completed = &value
			events = append(events, Event{Type: EventResponse, Response: completed})
			valid = true
			terminal = true
		case "response.failed", "error":
			return EventStream{}, fmt.Errorf("responses upstream event %s", typ)
		}
	}
	if !valid {
		return EventStream{}, &MalformedResponseError{Format: FormatResponses, Err: errors.New("no valid semantic event")}
	}
	if !terminal {
		return EventStream{Events: events, Stream: true}, &PartialResponseError{Err: errors.New("responses stream ended before terminal event")}
	}
	return EventStream{Events: events, Stream: true}, nil
}

func reconcileResponseArguments(state *responsesToolState, snapshot string) {
	if snapshot == "" {
		return
	}
	if state.args == snapshot || strings.HasPrefix(snapshot, state.args) {
		state.args = snapshot
		return
	}
	if state.args == "" {
		state.args = snapshot
	}
}

func decodeResponsesStreamDelta(payload map[string]json.RawMessage) *Event {
	if text := stringValue(payload["delta"]); text != "" {
		return &Event{Type: EventTextDelta, Text: text}
	}
	return nil
}

func encodeResponsesResponse(response Response) any {
	output := []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": response.Text}}}}
	for _, message := range response.Messages {
		for _, call := range message.ToolCalls {
			output = append(output, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
		}
	}
	usage := map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.TotalTokens}
	inputDetails := map[string]any{}
	if response.Usage.CachedReadTokens != 0 {
		inputDetails["cached_tokens"] = response.Usage.CachedReadTokens
	}
	if response.Usage.CacheWriteTokens != 0 {
		inputDetails["cache_write_tokens"] = response.Usage.CacheWriteTokens
	}
	if len(inputDetails) > 0 {
		usage["input_tokens_details"] = inputDetails
	}
	if response.Usage.ReasoningTokens != 0 {
		usage["output_tokens_details"] = map[string]any{"reasoning_tokens": response.Usage.ReasoningTokens}
	}
	return map[string]any{"id": valueOr(response.ID, "resp-translation"), "object": "response", "model": response.Model, "status": "completed", "output_text": response.Text, "output": output, "usage": usage}
}

func encodeResponsesStreamEvent(event Event) any {
	return map[string]any{"type": "response.output_text.delta", "delta": event.Text}
}
