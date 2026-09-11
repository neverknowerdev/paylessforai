package wire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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
	var value map[string]json.RawMessage
	if json.Unmarshal(payload["usage"], &value) != nil {
		return
	}
	_ = json.Unmarshal(value["prompt_tokens"], &usage.InputTokens)
	_ = json.Unmarshal(value["input_tokens"], &usage.InputTokens)
	_ = json.Unmarshal(value["completion_tokens"], &usage.OutputTokens)
	_ = json.Unmarshal(value["output_tokens"], &usage.OutputTokens)
	_ = json.Unmarshal(value["total_tokens"], &usage.TotalTokens)
	_ = json.Unmarshal(value["reasoning_tokens"], &usage.ReasoningTokens)
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
	}
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
	for _, line := range lines {
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
		if len(payload["usage"]) > 0 {
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
	return map[string]any{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
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
