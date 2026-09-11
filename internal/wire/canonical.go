package wire

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamResponse incrementally decodes an SSE response. The callback is
// invoked as soon as each semantic event is available; the response body is
// never buffered in memory. It is intended for proxy pumps where the caller
// owns downstream encoding and flushing.
func StreamResponse(format Format, response *http.Response, onEvent func(Event) error) (int, error) {
	if response == nil || response.Body == nil {
		return 0, &MalformedResponseError{Format: format, Err: errors.New("empty response")}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("upstream status %d", response.StatusCode)
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return 0, &MalformedResponseError{Format: format, Err: errors.New("response is not server-sent events")}
	}
	if _, err := CodecFor(format); err != nil {
		return 0, err
	}
	count := 0
	terminal := false
	eventName := ""
	dataLines := make([]string, 0, 1)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	dispatch := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" && format == FormatChatCompletions {
				terminal = true
			}
			eventName = ""
			return nil
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &payload) != nil {
			eventName = ""
			return &MalformedResponseError{Format: format, Err: errors.New("invalid SSE JSON data")}
		}
		typ := stringValue(payload["type"])
		if typ == "error" || eventName == "error" || len(payload["error"]) > 0 {
			message := stringValue(payload["message"])
			if message == "" {
				message = stringValue(payload["error"])
			}
			if nested, ok := rawObject(payload["error"]); ok && message == "" {
				message = stringValue(nested["message"])
			}
			if err := onEvent(Event{Type: EventError, Error: &ProviderError{Type: typ, Message: message}}); err != nil {
				return err
			}
			count++
			eventName = ""
			return fmt.Errorf("provider stream error: %s", message)
		}
		if eventName == "response.completed" || eventName == "response.failed" || eventName == "response.incomplete" || typ == "response.completed" || typ == "message_stop" || eventName == "message_stop" {
			terminal = true
		}
		if len(payload["usage"]) > 0 {
			var usage Usage
			decodeUsage(payload, &usage)
			if err := onEvent(Event{Type: EventUsage, Usage: &usage}); err != nil {
				return err
			}
			count++
		}
		var event *Event
		switch format {
		case FormatChatCompletions:
			event = decodeChatStreamDelta(payload)
		case FormatResponses:
			event = decodeResponsesStreamDelta(payload)
		case FormatAnthropicMessages:
			event = decodeAnthropicStreamDelta(payload)
		}
		if event == nil {
			if decoded, decodeErr := decodeStreamResponseEvent(format, payload); decodeErr == nil {
				event = decoded
			}
		}
		if event == nil {
			eventName = ""
			return nil
		}
		if err := onEvent(*event); err != nil {
			return err
		}
		count++
		eventName = ""
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return count, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	if err := dispatch(); err != nil {
		return count, err
	}
	if err := scanner.Err(); err != nil {
		if count > 0 {
			return count, &PartialResponseError{Err: err}
		}
		return 0, err
	}
	if count == 0 {
		return 0, &MalformedResponseError{Format: format, Err: errors.New("no valid semantic event")}
	}
	if !terminal {
		return count, &PartialResponseError{Err: errors.New("stream ended before terminal event")}
	}
	return count, nil
}

func decodeStreamResponseEvent(format Format, payload map[string]json.RawMessage) (*Event, error) {
	var decoded Response
	var err error
	switch format {
	case FormatChatCompletions:
		decoded, err = decodeChatResponsePayload(payload)
	case FormatResponses:
		decoded, err = decodeResponsesResponsePayload(payload)
	case FormatAnthropicMessages:
		decoded, err = decodeAnthropicResponsePayload(payload)
	default:
		return nil, errors.New("unsupported stream format")
	}
	if err != nil {
		return nil, err
	}
	return &Event{Type: EventResponse, Response: &decoded}, nil
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentBlock is semantic content rather than a provider-specific JSON
// shape. Raw is retained for fields that a later codec may understand.
type ContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	URL        string          `json:"url,omitempty"`
	Data       string          `json:"data,omitempty"`
	MediaType  string          `json:"media_type,omitempty"`
	FileName   string          `json:"file_name,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Signature  string          `json:"signature,omitempty"`
	Visibility string          `json:"visibility,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type Message struct {
	Role       Role           `json:"role"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Content    []ContentBlock `json:"content,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	Reasoning  []ContentBlock `json:"reasoning,omitempty"`
}

type Tool struct {
	Type        string          `json:"type,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type Usage struct {
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
	CachedReadTokens int64 `json:"cached_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	// InputTokensNetOfCache is true for providers whose input_tokens excludes
	// cache buckets (currently Anthropic Messages).
	InputTokensNetOfCache bool `json:"-"`
}

type Request struct {
	SourceFormat Format
	Model        string
	Messages     []Message
	Tools        []Tool
	Options      RequestOptions
	Extensions   map[string]json.RawMessage
}

type Response struct {
	ID           string
	Model        string
	Messages     []Message
	Text         string
	FinishReason string
	Usage        Usage
	Error        *ProviderError
}

type ProviderError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type EventType string

const (
	EventResponse       EventType = "response"
	EventTextDelta      EventType = "text_delta"
	EventReasoningDelta EventType = "reasoning_delta"
	EventAudioDelta     EventType = "audio_delta"
	EventToolCallDelta  EventType = "tool_call_delta"
	EventUsage          EventType = "usage"
	EventComplete       EventType = "complete"
	EventError          EventType = "error"
)

type Event struct {
	Type         EventType
	Text         string
	Reasoning    string
	Audio        string
	ToolCall     *ToolCall
	Response     *Response
	Usage        *Usage
	Error        *ProviderError
	FinishReason string
}

type EventStream struct {
	Events []Event
	Stream bool
}

type IncompatibilityError struct {
	Format  Format
	Feature string
	Message string
}

func (e *IncompatibilityError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s cannot represent %s: %s", e.Format, e.Feature, e.Message)
	}
	return fmt.Sprintf("%s cannot represent %s", e.Format, e.Feature)
}

func IsIncompatibility(err error) bool {
	var target *IncompatibilityError
	return errors.As(err, &target)
}

type MalformedResponseError struct {
	Format Format
	Err    error
}

func (e *MalformedResponseError) Error() string {
	return fmt.Sprintf("malformed %s response: %v", e.Format, e.Err)
}
func (e *MalformedResponseError) Unwrap() error { return e.Err }

type PartialResponseError struct{ Err error }

func (e *PartialResponseError) Error() string {
	return "upstream stream ended after delivering data: " + e.Err.Error()
}
func (e *PartialResponseError) Unwrap() error { return e.Err }

func newIncompatibility(format Format, feature string) error {
	return &IncompatibilityError{Format: format, Feature: feature}
}

func decodeJSON(body []byte) (map[string]json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("body must be valid JSON: %w", err)
	}
	return payload, nil
}

func rawCopy(value json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), value...) }

func decodeStringOrBlocks(raw json.RawMessage, format Format) ([]ContentBlock, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []ContentBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("decode %s content: %w", format, err)
	}
	result := make([]ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		decoded, err := decodeBlock(block, format)
		if err != nil {
			return nil, err
		}
		result = append(result, decoded...)
	}
	return result, nil
}

func decodeBlock(block map[string]json.RawMessage, format Format) ([]ContentBlock, error) {
	var typ string
	_ = json.Unmarshal(block["type"], &typ)
	lower := strings.ToLower(typ)
	content := ContentBlock{Type: lower, Raw: mustJSON(block)}
	var value string
	_ = json.Unmarshal(block["text"], &value)
	content.Text = value
	switch lower {
	case "text", "input_text", "output_text":
		content.Type = "text"
	case "image", "image_url", "input_image":
		content.Type = "image"
		var nested map[string]json.RawMessage
		if json.Unmarshal(block["image_url"], &nested) == nil {
			_ = json.Unmarshal(nested["url"], &content.URL)
		}
		_ = json.Unmarshal(block["url"], &content.URL)
		_ = json.Unmarshal(block["media_type"], &content.MediaType)
		if content.URL == "" {
			_ = json.Unmarshal(block["image_url"], &content.URL)
		}
		var source map[string]json.RawMessage
		if json.Unmarshal(block["source"], &source) == nil {
			_ = json.Unmarshal(source["url"], &content.URL)
			_ = json.Unmarshal(source["data"], &content.Data)
			_ = json.Unmarshal(source["media_type"], &content.MediaType)
		}
		if content.URL == "" && content.Data != "" {
			content.URL = mediaDataURL(content)
		}
	case "audio", "input_audio":
		content.Type = "audio"
		var nested map[string]json.RawMessage
		if json.Unmarshal(block["input_audio"], &nested) == nil {
			_ = json.Unmarshal(nested["data"], &content.Data)
			_ = json.Unmarshal(nested["format"], &content.MediaType)
		}
		_ = json.Unmarshal(block["data"], &content.Data)
		if content.MediaType == "" {
			_ = json.Unmarshal(block["media_type"], &content.MediaType)
		}
	case "file", "input_file", "document":
		content.Type = "file"
		_ = json.Unmarshal(block["file_id"], &content.ID)
		_ = json.Unmarshal(block["filename"], &content.FileName)
		_ = json.Unmarshal(block["file_name"], &content.FileName)
		_ = json.Unmarshal(block["file_url"], &content.URL)
		_ = json.Unmarshal(block["url"], &content.URL)
	case "reasoning", "thinking":
		content.Type = "reasoning"
		_ = json.Unmarshal(block["signature"], &content.Signature)
		_ = json.Unmarshal(block["visibility"], &content.Visibility)
	case "tool_use", "function_call":
		content.Type = "tool_call"
		_ = json.Unmarshal(block["id"], &content.ID)
		_ = json.Unmarshal(block["name"], &content.Name)
		if value := block["arguments"]; len(value) > 0 {
			content.Arguments = rawCopy(value)
		}
		if value := block["input"]; len(value) > 0 && len(content.Arguments) == 0 {
			content.Arguments = rawCopy(value)
		}
	case "tool_result", "function_call_output":
		content.Type = "tool_result"
		_ = json.Unmarshal(block["tool_use_id"], &content.ID)
		_ = json.Unmarshal(block["call_id"], &content.ID)
		if value := block["content"]; len(value) > 0 {
			content.Result = rawCopy(value)
		}
		if value := block["output"]; len(value) > 0 && len(content.Result) == 0 {
			content.Result = rawCopy(value)
		}
	default:
		if typ != "" {
			return nil, newIncompatibility(format, "content block "+typ)
		}
	}
	return []ContentBlock{content}, nil
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func mediaDataURL(block ContentBlock) string {
	if block.URL != "" {
		return block.URL
	}
	if block.Data == "" {
		return ""
	}
	if strings.HasPrefix(block.Data, "data:") {
		return block.Data
	}
	media := block.MediaType
	if media == "" {
		media = "application/octet-stream"
	}
	// Data is expected to be base64 by all three supported APIs. Do not
	// reinterpret arbitrary bytes or silently change a caller's URL.
	if _, err := base64.StdEncoding.DecodeString(block.Data); err != nil {
		return ""
	}
	return "data:" + media + ";base64," + block.Data
}

func ensureJSONObject(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return nil
	}
	return rawCopy(value)
}

// DecodeRequest selects a codec and decodes a client request exactly once.
func DecodeRequest(format Format, body []byte) (*Request, error) {
	codec, err := CodecFor(format)
	if err != nil {
		return nil, err
	}
	return codec.DecodeRequest(body)
}

func decodeRequestBody(format Format, body []byte, decodeOptions func(map[string]json.RawMessage) (RequestOptions, error), known map[string]bool, decode func(map[string]json.RawMessage, *Request) error) (*Request, error) {
	if err := format.Validate(); err != nil {
		return nil, err
	}
	payload, err := decodeJSON(body)
	if err != nil {
		return nil, err
	}
	options, err := decodeOptions(payload)
	if err != nil {
		return nil, err
	}
	request := &Request{SourceFormat: format, Options: options, Extensions: make(map[string]json.RawMessage)}
	if err := json.Unmarshal(payload["model"], &request.Model); err != nil || strings.TrimSpace(request.Model) == "" {
		return nil, errors.New("model is required")
	}
	request.Model = strings.TrimSpace(request.Model)
	if err := decode(payload, request); err != nil {
		return nil, err
	}
	for key, value := range payload {
		if !known[key] {
			request.Extensions[key] = rawCopy(value)
		}
	}
	return request, nil
}

func boolField(values map[string]json.RawMessage, name string) bool {
	var value bool
	_ = json.Unmarshal(values[name], &value)
	return value
}
func boolPointer(values map[string]json.RawMessage, name string) *bool {
	if len(values[name]) == 0 {
		return nil
	}
	var value bool
	if json.Unmarshal(values[name], &value) != nil {
		return nil
	}
	return &value
}
func floatPointer(values map[string]json.RawMessage, name string) *float64 {
	if len(values[name]) == 0 {
		return nil
	}
	var value float64
	if json.Unmarshal(values[name], &value) != nil {
		return nil
	}
	return &value
}
func intPointer(values map[string]json.RawMessage, names ...string) *int64 {
	for _, name := range names {
		if len(values[name]) == 0 {
			continue
		}
		var value int64
		if json.Unmarshal(values[name], &value) == nil && value > 0 {
			return &value
		}
	}
	return nil
}

// EncodeRequest creates a fresh body for each upstream attempt. The source
// request is never mutated, which is important when the next route uses a
// different dialect after a format-related failure.
func EncodeRequest(format Format, request *Request) ([]byte, error) {
	if request == nil {
		return nil, errors.New("canonical request is nil")
	}
	codec, err := CodecFor(format)
	if err != nil {
		return nil, err
	}
	return codec.EncodeRequest(request)
}

func blocksText(blocks []ContentBlock) string {
	var result strings.Builder
	for _, block := range blocks {
		if block.Text != "" {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(block.Text)
		} else if len(block.Result) > 0 {
			result.Write(block.Result)
		}
	}
	return result.String()
}
func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func jsonRawValue(value json.RawMessage) any {
	var decoded any
	if json.Unmarshal(value, &decoded) == nil {
		return decoded
	}
	return string(value)
}

func jsonArgument(value json.RawMessage) json.RawMessage {
	var encoded string
	if json.Unmarshal(value, &encoded) == nil {
		return json.RawMessage(encoded)
	}
	return rawCopy(value)
}
func jsonObject(value json.RawMessage) any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return jsonRawValue(value)
}
func jsonOrEmpty(value json.RawMessage) any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return jsonRawValue(value)
}

// DecodeResponse consumes the body so callers can validate an upstream
// response before writing any bytes to the client.
func DecodeResponse(format Format, response *http.Response) (EventStream, error) {
	codec, err := CodecFor(format)
	if err != nil {
		return EventStream{}, err
	}
	return codec.DecodeResponse(response)
}

type responsePayloadDecoder func(map[string]json.RawMessage) (Response, error)
type responseStreamDecoder func(map[string]json.RawMessage) *Event

func decodeResponseFor(format Format, response *http.Response, payloadDecoder responsePayloadDecoder, streamDecoder responseStreamDecoder) (EventStream, error) {
	if response == nil || response.Body == nil {
		return EventStream{}, &MalformedResponseError{Format: format, Err: errors.New("empty response")}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return EventStream{}, fmt.Errorf("upstream status %d", response.StatusCode)
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		events, err := decodeSSEFor(format, body, payloadDecoder, streamDecoder)
		if readErr != nil && len(events.Events) > 0 {
			return events, &PartialResponseError{Err: readErr}
		}
		return events, err
	}
	if readErr != nil {
		return EventStream{}, readErr
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return EventStream{}, &MalformedResponseError{Format: format, Err: err}
	}
	decoded, err := payloadDecoder(payload)
	if err != nil {
		return EventStream{}, &MalformedResponseError{Format: format, Err: err}
	}
	return EventStream{Events: []Event{{Type: EventResponse, Response: &decoded}}, Stream: false}, nil
}

func stringValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func decodeUsage(payload map[string]json.RawMessage, usage *Usage) {
	payload = usagePayload(payload)
	var value map[string]json.RawMessage
	if raw := payload["usage"]; len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return
	}
	_ = json.Unmarshal(value["prompt_tokens"], &usage.InputTokens)
	_ = json.Unmarshal(value["input_tokens"], &usage.InputTokens)
	_ = json.Unmarshal(value["completion_tokens"], &usage.OutputTokens)
	_ = json.Unmarshal(value["output_tokens"], &usage.OutputTokens)
	_ = json.Unmarshal(value["total_tokens"], &usage.TotalTokens)
	_ = json.Unmarshal(value["reasoning_tokens"], &usage.ReasoningTokens)
	if details, ok := rawObject(value["prompt_tokens_details"]); ok {
		_ = json.Unmarshal(details["cached_tokens"], &usage.CachedReadTokens)
		_ = json.Unmarshal(details["cache_read_input_tokens"], &usage.CachedReadTokens)
		_ = json.Unmarshal(details["cache_write_tokens"], &usage.CacheWriteTokens)
	}
	if details, ok := rawObject(value["input_tokens_details"]); ok {
		_ = json.Unmarshal(details["cached_tokens"], &usage.CachedReadTokens)
		_ = json.Unmarshal(details["cache_read_input_tokens"], &usage.CachedReadTokens)
		_ = json.Unmarshal(details["cache_write_tokens"], &usage.CacheWriteTokens)
	}
	_ = json.Unmarshal(value["cached_tokens"], &usage.CachedReadTokens)
	_ = json.Unmarshal(value["cache_read_input_tokens"], &usage.CachedReadTokens)
	_ = json.Unmarshal(value["cache_creation_input_tokens"], &usage.CacheWriteTokens)
	_ = json.Unmarshal(value["cache_write_tokens"], &usage.CacheWriteTokens)
	if _, hasCacheRead := value["cache_read_input_tokens"]; hasCacheRead {
		usage.InputTokensNetOfCache = true
	}
	if details, ok := rawObject(value["completion_tokens_details"]); ok {
		_ = json.Unmarshal(details["reasoning_tokens"], &usage.ReasoningTokens)
	}
	if details, ok := rawObject(value["output_tokens_details"]); ok {
		if raw := details["reasoning_tokens"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &usage.ReasoningTokens)
		}
		if raw := details["thinking_tokens"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &usage.ReasoningTokens)
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		if usage.InputTokensNetOfCache {
			usage.TotalTokens += usage.CachedReadTokens + usage.CacheWriteTokens
		}
	}
}

// usagePayload unwraps lifecycle envelopes used by Responses and Anthropic
// streaming. It intentionally only follows documented response/message keys.
func usagePayload(payload map[string]json.RawMessage) map[string]json.RawMessage {
	if len(payload["usage"]) > 0 {
		return payload
	}
	for _, key := range []string{"response", "message"} {
		if nested, ok := rawObject(payload[key]); ok && len(nested["usage"]) > 0 {
			return nested
		}
	}
	return payload
}

func hasUsage(payload map[string]json.RawMessage) bool {
	return len(usagePayload(payload)["usage"]) > 0
}

func rawObject(value json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(value) == 0 {
		return nil, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(value, &result) != nil {
		return nil, false
	}
	return result, true
}
func decodeSSEFor(format Format, body []byte, payloadDecoder responsePayloadDecoder, streamDecoder responseStreamDecoder) (EventStream, error) {
	lines := strings.Split(string(body), "\n")
	events := make([]Event, 0)
	valid := false
	terminal := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" && format == FormatChatCompletions {
				terminal = true
			}
			continue
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &payload) != nil {
			continue
		}
		typ := stringValue(payload["type"])
		if typ == "response.completed" || typ == "message_stop" {
			terminal = true
		}
		if hasUsage(payload) {
			var usage Usage
			decodeUsage(payload, &usage)
			events = append(events, Event{Type: EventUsage, Usage: &usage})
		}
		if delta := streamDecoder(payload); delta != nil {
			events = append(events, *delta)
			valid = true
			continue
		}
		if decoded, err := payloadDecoder(payload); err == nil {
			events = append(events, Event{Type: EventResponse, Response: &decoded})
			valid = true
		} else if text := stringValue(payload["text"]); text != "" {
			events = append(events, Event{Type: EventTextDelta, Text: text})
			valid = true
		}
	}
	if !valid {
		return EventStream{}, &MalformedResponseError{Format: format, Err: errors.New("no valid semantic event")}
	}
	if !terminal {
		return EventStream{Events: events, Stream: true}, &PartialResponseError{Err: errors.New("stream ended before terminal event")}
	}
	return EventStream{Events: events, Stream: true}, nil
}

// EncodeResponse emits a response in the requested client format.
func EncodeResponse(format Format, events EventStream, w http.ResponseWriter) error {
	codec, err := CodecFor(format)
	if err != nil {
		return err
	}
	return codec.EncodeResponse(events, w)
}

// StartStream initializes a downstream SSE response.
func StartStream(format Format, w http.ResponseWriter) (http.Flusher, error) {
	if err := format.Validate(); err != nil {
		return nil, err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if format == FormatResponses {
		if err := writeSSEFrame(w, flusher, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp-translation", "object": "response", "status": "in_progress"}}); err != nil {
			return flusher, err
		}
	}
	if format == FormatAnthropicMessages {
		if err := writeSSEFrame(w, flusher, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg-translation", "type": "message", "role": "assistant", "content": []any{}, "model": "translation"}}); err != nil {
			return flusher, err
		}
		if err := writeSSEFrame(w, flusher, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
			return flusher, err
		}
	}
	return flusher, nil
}

// EncodeStreamEvent writes one protocol-specific SSE event and flushes it.
func EncodeStreamEvent(format Format, event Event, w http.ResponseWriter, flusher http.Flusher) error {
	var payload any
	switch format {
	case FormatChatCompletions:
		payload = encodeChatStreamEvent(event)
	case FormatResponses:
		payload = encodeResponsesStreamEvent(event)
	case FormatAnthropicMessages:
		payload = encodeAnthropicStreamEvent(event)
	default:
		return format.Validate()
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeSSEFrame(w, flusher, streamEventName(format, event), json.RawMessage(encoded))
}

func writeSSEFrame(w io.Writer, flusher http.Flusher, event string, payload any) error {
	encoded, err := json.Marshal(payload)
	if raw, ok := payload.(json.RawMessage); ok {
		encoded = raw
	}
	if err != nil {
		return err
	}
	frame := ""
	if event != "" {
		frame += "event: " + event + "\n"
	}
	frame += "data: " + string(encoded) + "\n\n"
	if _, err := io.WriteString(w, frame); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func streamEventName(format Format, event Event) string {
	switch format {
	case FormatResponses:
		switch event.Type {
		case EventTextDelta:
			return "response.output_text.delta"
		case EventReasoningDelta:
			return "response.reasoning.delta"
		case EventToolCallDelta:
			return "response.function_call_arguments.delta"
		case EventUsage:
			return "response.completed"
		case EventComplete:
			return "response.completed"
		case EventResponse:
			return "response.completed"
		}
	case FormatAnthropicMessages:
		switch event.Type {
		case EventTextDelta, EventReasoningDelta, EventToolCallDelta:
			return "content_block_delta"
		case EventUsage:
			return "message_delta"
		case EventComplete:
			return "message_stop"
		}
	}
	return ""
}

// EndStream emits the protocol terminal marker. Chat uses the conventional
// [DONE] sentinel; the other protocols use a typed terminal event.
func EndStream(format Format, w http.ResponseWriter, flusher http.Flusher) error {
	if format == FormatChatCompletions {
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	if format == FormatAnthropicMessages {
		if err := writeSSEFrame(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
			return err
		}
		if err := writeSSEFrame(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}}); err != nil {
			return err
		}
	}
	return EncodeStreamEvent(format, Event{Type: EventComplete}, w, flusher)
}

func encodeResponseFor(format Format, events EventStream, w http.ResponseWriter, responseEncoder func(Response) any, streamEncoder func(Event) any) error {
	if err := format.Validate(); err != nil {
		return err
	}
	response := Response{}
	for _, event := range events.Events {
		if event.Response != nil {
			response = *event.Response
			break
		}
		if event.Text != "" {
			response.Text += event.Text
		}
	}
	if response.Text == "" && len(response.Messages) > 0 {
		response.Text = blocksText(response.Messages[0].Content)
	}
	if events.Stream {
		return encodeStreamFor(events, w, streamEncoder)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(responseEncoder(response))
}
func usageMap(usage Usage) map[string]any {
	result := map[string]any{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
	prompt := map[string]any{}
	if usage.CachedReadTokens != 0 {
		prompt["cached_tokens"] = usage.CachedReadTokens
	}
	if usage.CacheWriteTokens != 0 {
		prompt["cache_write_tokens"] = usage.CacheWriteTokens
	}
	if len(prompt) > 0 {
		result["prompt_tokens_details"] = prompt
	}
	if usage.ReasoningTokens != 0 {
		result["completion_tokens_details"] = map[string]any{"reasoning_tokens": usage.ReasoningTokens}
	}
	return result
}
func encodeStreamFor(events EventStream, w http.ResponseWriter, streamEncoder func(Event) any) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, event := range events.Events {
		payload := streamEncoder(event)
		encoded, _ := json.Marshal(payload)
		_, _ = io.WriteString(w, "data: "+string(encoded)+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	return nil
}
