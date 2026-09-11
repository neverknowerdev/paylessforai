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
	Model            string
	Messages         []Message
	Tools            []Tool
	ToolChoice       json.RawMessage
	ParallelToolCall *bool
	ResponseFormat   json.RawMessage
	Temperature      *float64
	TopP             *float64
	Stop             json.RawMessage
	MaxOutputTokens  *int64
	Stream           bool
	Modalities       []string
	Metadata         map[string]json.RawMessage
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
	if err := format.Validate(); err != nil {
		return nil, err
	}
	payload, err := decodeJSON(body)
	if err != nil {
		return nil, err
	}
	request := &Request{Metadata: make(map[string]json.RawMessage)}
	if err := json.Unmarshal(payload["model"], &request.Model); err != nil || strings.TrimSpace(request.Model) == "" {
		return nil, errors.New("model is required")
	}
	request.Model = strings.TrimSpace(request.Model)
	request.Stream = boolField(payload, "stream")
	request.ResponseFormat = rawCopy(payload["response_format"])
	request.ToolChoice = rawCopy(payload["tool_choice"])
	request.Stop = rawCopy(payload["stop"])
	request.ParallelToolCall = boolPointer(payload, "parallel_tool_calls")
	request.Temperature = floatPointer(payload, "temperature")
	request.TopP = floatPointer(payload, "top_p")
	request.MaxOutputTokens = intPointer(payload, "max_tokens", "max_completion_tokens", "max_output_tokens")
	if raw := payload["modalities"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &request.Modalities)
	}

	switch format {
	case FormatChatCompletions:
		if len(payload["messages"]) == 0 {
			return nil, errors.New("messages is required")
		}
		if err := decodeChatMessages(payload["messages"], request); err != nil {
			return nil, err
		}
		decodeTools(payload["tools"], request)
	case FormatResponses:
		if len(payload["input"]) == 0 {
			return nil, errors.New("input is required")
		}
		if raw := payload["instructions"]; len(raw) > 0 {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				request.Messages = append(request.Messages, Message{Role: RoleDeveloper, Content: []ContentBlock{{Type: "text", Text: text}}})
			}
		}
		if err := decodeResponsesInput(payload["input"], request); err != nil {
			return nil, err
		}
		decodeTools(payload["tools"], request)
	case FormatAnthropicMessages:
		if len(payload["messages"]) == 0 {
			return nil, errors.New("messages is required")
		}
		if raw := payload["system"]; len(raw) > 0 {
			blocks, err := decodeStringOrBlocks(raw, format)
			if err != nil {
				return nil, err
			}
			request.Messages = append(request.Messages, Message{Role: RoleSystem, Content: blocks})
		}
		if err := decodeAnthropicMessages(payload["messages"], request); err != nil {
			return nil, err
		}
		decodeAnthropicTools(payload["tools"], request)
		request.MaxOutputTokens = intPointer(payload, "max_tokens", "max_output_tokens")
	}
	known := map[string]bool{"model": true, "messages": true, "input": true, "instructions": true, "system": true, "tools": true, "tool_choice": true, "parallel_tool_calls": true, "response_format": true, "temperature": true, "top_p": true, "stop": true, "max_tokens": true, "max_completion_tokens": true, "max_output_tokens": true, "stream": true, "modalities": true}
	for key, value := range payload {
		if !known[key] {
			request.Metadata[key] = rawCopy(value)
		}
	}
	return request, nil
}

func decodeChatMessages(raw json.RawMessage, request *Request) error {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return err
	}
	for _, item := range messages {
		var role string
		_ = json.Unmarshal(item["role"], &role)
		message := Message{Role: Role(strings.ToLower(role))}
		if message.Role == "" {
			return errors.New("message role is required")
		}
		_ = json.Unmarshal(item["name"], &message.Name)
		_ = json.Unmarshal(item["tool_call_id"], &message.ToolCallID)
		blocks, err := decodeStringOrBlocks(item["content"], FormatChatCompletions)
		if err != nil {
			return err
		}
		message.Content = blocks
		if rawCalls := item["tool_calls"]; len(rawCalls) > 0 {
			var calls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(rawCalls, &calls); err != nil {
				return err
			}
			for _, call := range calls {
				message.ToolCalls = append(message.ToolCalls, ToolCall{ID: call.ID, Type: call.Type, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
			}
		}
		if rawReasoning := item["reasoning_content"]; len(rawReasoning) > 0 {
			blocks, err := decodeStringOrBlocks(rawReasoning, FormatChatCompletions)
			if err != nil {
				return err
			}
			for i := range blocks {
				blocks[i].Type = "reasoning"
			}
			message.Reasoning = blocks
		}
		request.Messages = append(request.Messages, message)
	}
	return nil
}

func decodeResponsesInput(raw json.RawMessage, request *Request) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		request.Messages = append(request.Messages, Message{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: text}}})
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	for _, item := range items {
		var typ, role string
		_ = json.Unmarshal(item["type"], &typ)
		_ = json.Unmarshal(item["role"], &role)
		switch strings.ToLower(typ) {
		case "message", "":
			if role == "" {
				role = "user"
			}
			blocks, err := decodeStringOrBlocks(item["content"], FormatResponses)
			if err != nil {
				return err
			}
			request.Messages = append(request.Messages, Message{Role: Role(role), Content: blocks})
		case "function_call", "custom_tool_call":
			var call ToolCall
			_ = json.Unmarshal(item["call_id"], &call.ID)
			_ = json.Unmarshal(item["id"], &call.ID)
			_ = json.Unmarshal(item["name"], &call.Name)
			call.Type = typ
			call.Arguments = rawCopy(item["arguments"])
			request.Messages = append(request.Messages, Message{Role: RoleAssistant, ToolCalls: []ToolCall{call}})
		case "function_call_output", "custom_tool_call_output":
			var id string
			_ = json.Unmarshal(item["call_id"], &id)
			result := rawCopy(item["output"])
			request.Messages = append(request.Messages, Message{Role: RoleTool, ToolCallID: id, Content: []ContentBlock{{Type: "tool_result", ID: id, Result: result}}})
		default:
			return newIncompatibility(FormatResponses, "input item "+typ)
		}
	}
	return nil
}

func decodeAnthropicMessages(raw json.RawMessage, request *Request) error {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return err
	}
	for _, item := range messages {
		var role string
		_ = json.Unmarshal(item["role"], &role)
		blocks, err := decodeStringOrBlocks(item["content"], FormatAnthropicMessages)
		if err != nil {
			return err
		}
		message := Message{Role: Role(role), Content: blocks}
		for _, block := range blocks {
			if block.Type == "tool_call" {
				message.ToolCalls = append(message.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Arguments, Type: "function"})
			}
		}
		request.Messages = append(request.Messages, message)
	}
	return nil
}

func decodeTools(raw json.RawMessage, request *Request) {
	var values []map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	for _, value := range values {
		var tool Tool
		_ = json.Unmarshal(value["type"], &tool.Type)
		if tool.Type == "function" {
			var fn map[string]json.RawMessage
			if json.Unmarshal(value["function"], &fn) == nil {
				_ = json.Unmarshal(fn["name"], &tool.Name)
				_ = json.Unmarshal(fn["description"], &tool.Description)
				tool.Parameters = ensureJSONObject(fn["parameters"])
			}
		} else {
			_ = json.Unmarshal(value["name"], &tool.Name)
			tool.Parameters = ensureJSONObject(value["parameters"])
		}
		request.Tools = append(request.Tools, tool)
	}
}
func decodeAnthropicTools(raw json.RawMessage, request *Request) {
	var values []map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	for _, value := range values {
		var tool Tool
		tool.Type = "function"
		_ = json.Unmarshal(value["name"], &tool.Name)
		_ = json.Unmarshal(value["description"], &tool.Description)
		tool.Parameters = ensureJSONObject(value["input_schema"])
		request.Tools = append(request.Tools, tool)
	}
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
	if err := format.Validate(); err != nil {
		return nil, err
	}
	switch format {
	case FormatChatCompletions:
		return encodeChat(request)
	case FormatResponses:
		return encodeResponses(request)
	case FormatAnthropicMessages:
		return encodeAnthropic(request)
	}
	return nil, format.Validate()
}

func encodeChat(request *Request) ([]byte, error) {
	payload := map[string]any{"model": request.Model, "messages": make([]any, 0, len(request.Messages)), "stream": request.Stream}
	for _, message := range request.Messages {
		item := map[string]any{"role": string(message.Role)}
		if message.Name != "" {
			item["name"] = message.Name
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			calls := make([]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				calls = append(calls, map[string]any{"id": call.ID, "type": valueOr(call.Type, "function"), "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
			}
			item["tool_calls"] = calls
		}
		if len(message.Reasoning) > 0 {
			item["reasoning_content"] = blocksText(message.Reasoning)
		}
		contentBlocks := make([]ContentBlock, 0, len(message.Content))
		reasoning := append([]ContentBlock(nil), message.Reasoning...)
		for _, block := range message.Content {
			if block.Type == "reasoning" {
				reasoning = append(reasoning, block)
				continue
			}
			contentBlocks = append(contentBlocks, block)
		}
		encoded, err := encodeChatContent(contentBlocks, FormatChatCompletions)
		if err != nil {
			return nil, err
		}
		if len(contentBlocks) > 0 {
			item["content"] = encoded
		}
		if len(reasoning) > 0 {
			item["reasoning_content"] = blocksText(reasoning)
		}
		payload["messages"] = append(payload["messages"].([]any), item)
	}
	addCommon(payload, request, "max_tokens")
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": jsonOrEmpty(tool.Parameters)}})
		}
		payload["tools"] = tools
	}
	if len(request.ToolChoice) > 0 {
		payload["tool_choice"] = jsonRawValue(request.ToolChoice)
	}
	if request.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *request.ParallelToolCall
	}
	if len(request.ResponseFormat) > 0 {
		payload["response_format"] = jsonRawValue(request.ResponseFormat)
	}
	addFormatMetadata(payload, request.Metadata, FormatChatCompletions)
	return json.Marshal(payload)
}

func encodeResponses(request *Request) ([]byte, error) {
	payload := map[string]any{"model": request.Model, "input": make([]any, 0), "stream": request.Stream}
	input := payload["input"].([]any)
	for _, message := range request.Messages {
		if message.Role == RoleSystem || message.Role == RoleDeveloper || message.Role == RoleUser || message.Role == RoleAssistant {
			content, err := encodeResponsesContent(message.Content)
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
		} else if message.Role == RoleTool {
			result := ""
			if len(message.Content) > 0 {
				result = blocksText(message.Content)
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": result})
		} else {
			return nil, newIncompatibility(FormatResponses, "role "+string(message.Role))
		}
	}
	payload["input"] = input
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": jsonOrEmpty(tool.Parameters)})
		}
		payload["tools"] = tools
	}
	if len(request.ToolChoice) > 0 {
		payload["tool_choice"] = jsonRawValue(request.ToolChoice)
	}
	if request.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *request.ParallelToolCall
	}
	if len(request.ResponseFormat) > 0 {
		payload["text"] = map[string]any{"format": structuredFormat(request.ResponseFormat)}
	}
	addCommon(payload, request, "max_output_tokens")
	addFormatMetadata(payload, request.Metadata, FormatResponses)
	return json.Marshal(payload)
}

func encodeAnthropic(request *Request) ([]byte, error) {
	payload := map[string]any{"model": request.Model, "messages": make([]any, 0), "stream": request.Stream}
	messages := payload["messages"].([]any)
	system := []ContentBlock{}
	for _, message := range request.Messages {
		if message.Role == RoleSystem || message.Role == RoleDeveloper {
			system = append(system, message.Content...)
			continue
		}
		role := string(message.Role)
		content := make([]any, 0)
		if role == "tool" {
			role = "user"
			result := blocksText(message.Content)
			content = append(content, map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": result})
			messages = append(messages, map[string]any{"role": role, "content": content})
			continue
		}
		for _, block := range message.Content {
			encoded, err := encodeAnthropicBlock(block)
			if err != nil {
				return nil, err
			}
			content = append(content, encoded)
		}
		for _, call := range message.ToolCalls {
			content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": jsonObject(call.Arguments)})
		}
		messages = append(messages, map[string]any{"role": role, "content": content})
	}
	payload["messages"] = messages
	if len(system) > 0 {
		val, err := encodeAnthropicContent(system)
		if err != nil {
			return nil, err
		}
		payload["system"] = val
	}
	tools := make([]any, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": jsonOrEmpty(tool.Parameters)})
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	if len(request.ToolChoice) > 0 {
		payload["tool_choice"] = jsonRawValue(request.ToolChoice)
	}
	if request.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *request.ParallelToolCall
	}
	addCommon(payload, request, "max_tokens")
	if len(request.Stop) > 0 {
		delete(payload, "stop")
		payload["stop_sequences"] = jsonRawValue(request.Stop)
	}
	if len(request.ResponseFormat) > 0 {
		payload["output_config"] = map[string]any{"format": structuredFormat(request.ResponseFormat)}
	}
	addFormatMetadata(payload, request.Metadata, FormatAnthropicMessages)
	return json.Marshal(payload)
}

// addFormatMetadata copies provider-specific request options while translating
// the reasoning option between the OpenAI-compatible dialects. OpenCode sends
// reasoning_effort on its Chat Completions requests, but the Responses API
// only accepts that value under reasoning.effort.
func addFormatMetadata(payload map[string]any, metadata map[string]json.RawMessage, format Format) {
	if format == FormatResponses {
		if raw, ok := metadata["reasoning"]; ok {
			payload["reasoning"] = jsonRawValue(raw)
		}
		if raw, ok := metadata["reasoning_effort"]; ok {
			reasoning, ok := payload["reasoning"].(map[string]any)
			if !ok {
				reasoning = make(map[string]any)
			}
			reasoning["effort"] = jsonRawValue(raw)
			payload["reasoning"] = reasoning
		}
	}
	if format == FormatChatCompletions {
		if raw, ok := metadata["reasoning_effort"]; ok {
			payload["reasoning_effort"] = jsonRawValue(raw)
		} else if raw, ok := metadata["reasoning"]; ok {
			var reasoning map[string]json.RawMessage
			if json.Unmarshal(raw, &reasoning) == nil {
				if effort, ok := reasoning["effort"]; ok {
					payload["reasoning_effort"] = jsonRawValue(effort)
				}
			}
		}
	}

	for key, value := range metadata {
		if key == "reasoning" || key == "reasoning_effort" {
			continue
		}
		if _, exists := payload[key]; !exists {
			payload[key] = jsonRawValue(value)
		}
	}
}

func addCommon(payload map[string]any, request *Request, maxName string) {
	if request.MaxOutputTokens != nil {
		payload[maxName] = *request.MaxOutputTokens
	}
	if request.Temperature != nil {
		payload["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		payload["top_p"] = *request.TopP
	}
	if len(request.Stop) > 0 {
		payload["stop"] = jsonRawValue(request.Stop)
	}
}
func encodeChatContent(blocks []ContentBlock, format Format) (any, error) {
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text, nil
	}
	result := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			result = append(result, map[string]any{"type": "text", "text": block.Text})
		case "image":
			url := mediaDataURL(block)
			if url == "" {
				return nil, newIncompatibility(format, "image")
			}
			result = append(result, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		case "audio":
			if block.Data == "" {
				return nil, newIncompatibility(format, "audio")
			}
			result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": block.Data, "format": valueOr(block.MediaType, "wav")}})
		case "file":
			result = append(result, map[string]any{"type": "file", "file": map[string]any{"file_id": block.ID, "filename": block.FileName, "file_data": mediaDataURL(block)}})
		case "reasoning":
			return nil, newIncompatibility(format, "reasoning")
		case "tool_result":
			result = append(result, map[string]any{"type": "text", "text": blocksText([]ContentBlock{block})})
		default:
			return nil, newIncompatibility(format, block.Type)
		}
	}
	return result, nil
}
func encodeResponsesContent(blocks []ContentBlock) ([]any, error) {
	result := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			result = append(result, map[string]any{"type": "input_text", "text": block.Text})
		case "image":
			url := mediaDataURL(block)
			if url == "" {
				return nil, newIncompatibility(FormatResponses, "image")
			}
			result = append(result, map[string]any{"type": "input_image", "image_url": url})
		case "audio":
			if block.Data == "" {
				return nil, newIncompatibility(FormatResponses, "audio")
			}
			result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": block.Data, "format": valueOr(block.MediaType, "wav")}})
		case "file":
			result = append(result, map[string]any{"type": "input_file", "file_id": block.ID, "filename": block.FileName, "file_url": block.URL})
		case "reasoning":
			result = append(result, map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": block.Text}}})
		case "tool_result":
			result = append(result, map[string]any{"type": "input_text", "text": blocksText([]ContentBlock{block})})
		default:
			return nil, newIncompatibility(FormatResponses, block.Type)
		}
	}
	return result, nil
}
func encodeAnthropicBlock(block ContentBlock) (any, error) {
	switch block.Type {
	case "text":
		return map[string]any{"type": "text", "text": block.Text}, nil
	case "image":
		url := mediaDataURL(block)
		if url == "" {
			return nil, newIncompatibility(FormatAnthropicMessages, "image")
		}
		if strings.HasPrefix(url, "data:") {
			parts := strings.SplitN(url, ",", 2)
			header := strings.TrimPrefix(parts[0], "data:")
			media := strings.TrimSuffix(strings.TrimPrefix(header, ""), ";base64")
			return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": media, "data": parts[1]}}, nil
		}
		return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": url}}, nil
	case "audio":
		return nil, newIncompatibility(FormatAnthropicMessages, "audio")
	case "file":
		return map[string]any{"type": "document", "source": map[string]any{"type": "url", "url": block.URL}}, nil
	case "tool_result":
		return map[string]any{"type": "tool_result", "tool_use_id": block.ID, "content": blocksText([]ContentBlock{{Type: "text", Text: string(block.Result)}})}, nil
	default:
		return nil, newIncompatibility(FormatAnthropicMessages, block.Type)
	}
}
func encodeAnthropicContent(blocks []ContentBlock) (any, error) {
	result := make([]any, 0, len(blocks))
	for _, block := range blocks {
		item, err := encodeAnthropicBlock(block)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
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

func structuredFormat(value json.RawMessage) any {
	var source map[string]json.RawMessage
	if json.Unmarshal(value, &source) != nil {
		return jsonRawValue(value)
	}
	var typ string
	_ = json.Unmarshal(source["type"], &typ)
	if typ != "json_schema" {
		return jsonRawValue(value)
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(source["json_schema"], &schema) != nil {
		return jsonRawValue(value)
	}
	result := map[string]any{"type": "json_schema"}
	var name, strict, schemaValue any
	_ = json.Unmarshal(schema["name"], &name)
	_ = json.Unmarshal(schema["schema"], &schemaValue)
	_ = json.Unmarshal(schema["strict"], &strict)
	if name != nil {
		result["name"] = name
	}
	if schemaValue != nil {
		result["schema"] = schemaValue
	}
	if strict != nil {
		result["strict"] = strict
	}
	return result
}

// DecodeResponse consumes the body so callers can validate an upstream
// response before writing any bytes to the client.
func DecodeResponse(format Format, response *http.Response) (EventStream, error) {
	if response == nil || response.Body == nil {
		return EventStream{}, &MalformedResponseError{Format: format, Err: errors.New("empty response")}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return EventStream{}, fmt.Errorf("upstream status %d", response.StatusCode)
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		events, err := decodeSSE(format, body)
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
	decoded, err := decodeResponsePayload(format, payload)
	if err != nil {
		return EventStream{}, &MalformedResponseError{Format: format, Err: err}
	}
	return EventStream{Events: []Event{{Type: EventResponse, Response: &decoded}}, Stream: false}, nil
}

func decodeResponsePayload(format Format, payload map[string]json.RawMessage) (Response, error) {
	result := Response{}
	_ = json.Unmarshal(payload["id"], &result.ID)
	_ = json.Unmarshal(payload["model"], &result.Model)
	switch format {
	case FormatChatCompletions:
		var choices []map[string]json.RawMessage
		if json.Unmarshal(payload["choices"], &choices) != nil || len(choices) == 0 {
			return result, errors.New("choices is missing")
		}
		choice := choices[0]
		_ = json.Unmarshal(choice["finish_reason"], &result.FinishReason)
		if len(choice["message"]) > 0 {
			var msg map[string]json.RawMessage
			_ = json.Unmarshal(choice["message"], &msg)
			var role string
			_ = json.Unmarshal(msg["role"], &role)
			blocks, err := decodeStringOrBlocks(msg["content"], format)
			if err != nil {
				return result, err
			}
			result.Messages = []Message{{Role: Role(role), Content: blocks}}
			result.Messages[0].ToolCalls = decodeChatToolCalls(msg["tool_calls"])
			if reasoning := stringValue(msg["reasoning_content"]); reasoning != "" {
				result.Messages[0].Reasoning = []ContentBlock{{Type: "reasoning", Text: reasoning}}
			}
		}
	case FormatResponses:
		if text := stringValue(payload["output_text"]); text != "" {
			result.Text = text
		}
		if len(payload["output"]) > 0 {
			var output []map[string]json.RawMessage
			if json.Unmarshal(payload["output"], &output) == nil {
				for _, item := range output {
					var role string
					_ = json.Unmarshal(item["role"], &role)
					if role == "" {
						role = "assistant"
					}
					blocks, _ := decodeStringOrBlocks(item["content"], format)
					result.Messages = append(result.Messages, Message{Role: Role(role), Content: blocks})
				}
			}
		}
		if result.Text == "" {
			for _, message := range result.Messages {
				result.Text += blocksText(message.Content)
			}
		}
		_ = json.Unmarshal(payload["status"], &result.FinishReason)
	case FormatAnthropicMessages:
		blocks, err := decodeStringOrBlocks(payload["content"], format)
		if err != nil {
			return result, err
		}
		message := Message{Role: RoleAssistant, Content: blocks}
		for _, block := range blocks {
			if block.Type == "tool_call" {
				message.ToolCalls = append(message.ToolCalls, ToolCall{ID: block.ID, Type: "function", Name: block.Name, Arguments: block.Arguments})
			}
		}
		result.Messages = []Message{message}
		result.Text = blocksText(blocks)
		_ = json.Unmarshal(payload["stop_reason"], &result.FinishReason)
	default:
		return result, format.Validate()
	}
	decodeUsage(payload, &result.Usage)
	if len(result.Messages) == 0 && result.Text == "" {
		return result, errors.New("response has no semantic content")
	}
	return result, nil
}

func decodeChatToolCalls(raw json.RawMessage) []ToolCall {
	var calls []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &calls) != nil {
		return nil
	}
	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, ToolCall{ID: call.ID, Type: call.Type, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	return result
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
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
}
func decodeSSE(format Format, body []byte) (EventStream, error) {
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
		if delta := streamDelta(format, payload); delta != nil {
			events = append(events, *delta)
			valid = true
			continue
		}
		if decoded, err := decodeResponsePayload(format, payload); err == nil {
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

func streamDelta(format Format, payload map[string]json.RawMessage) *Event {
	if format == FormatResponses {
		if text := stringValue(payload["delta"]); text != "" {
			return &Event{Type: EventTextDelta, Text: text}
		}
	}
	if format == FormatAnthropicMessages {
		var delta map[string]json.RawMessage
		if json.Unmarshal(payload["delta"], &delta) == nil {
			if text := stringValue(delta["text"]); text != "" {
				return &Event{Type: EventTextDelta, Text: text}
			}
		}
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(payload["choices"], &choices) == nil && len(choices) > 0 {
		var delta map[string]json.RawMessage
		if json.Unmarshal(choices[0]["delta"], &delta) == nil {
			if text := stringValue(delta["content"]); text != "" {
				return &Event{Type: EventTextDelta, Text: text}
			}
			if text := stringValue(delta["reasoning_content"]); text != "" {
				return &Event{Type: EventReasoningDelta, Reasoning: text}
			}
		}
	}
	return nil
}

// EncodeResponse emits a response in the requested client format.
func EncodeResponse(format Format, events EventStream, w http.ResponseWriter) error {
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
		return encodeStream(format, events, w)
	}
	var payload any
	switch format {
	case FormatChatCompletions:
		message := map[string]any{"role": "assistant", "content": response.Text}
		if len(response.Messages) > 0 {
			if len(response.Messages[0].ToolCalls) > 0 {
				calls := make([]any, 0, len(response.Messages[0].ToolCalls))
				for _, call := range response.Messages[0].ToolCalls {
					calls = append(calls, map[string]any{"id": call.ID, "type": valueOr(call.Type, "function"), "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
				}
				message["tool_calls"] = calls
				message["content"] = nil
			}
		}
		payload = map[string]any{"id": valueOr(response.ID, "chatcmpl-translation"), "object": "chat.completion", "model": response.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": valueOr(response.FinishReason, "stop")}}, "usage": usageMap(response.Usage)}
	case FormatResponses:
		output := []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": response.Text}}}}
		if len(response.Messages) > 0 {
			for _, call := range response.Messages[0].ToolCalls {
				output = append(output, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
			}
		}
		payload = map[string]any{"id": valueOr(response.ID, "resp-translation"), "object": "response", "model": response.Model, "status": "completed", "output_text": response.Text, "output": output, "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.TotalTokens}}
	case FormatAnthropicMessages:
		content := []any{map[string]any{"type": "text", "text": response.Text}}
		if len(response.Messages) > 0 {
			for _, call := range response.Messages[0].ToolCalls {
				content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": jsonObject(call.Arguments)})
			}
		}
		payload = map[string]any{"id": valueOr(response.ID, "msg-translation"), "type": "message", "role": "assistant", "model": response.Model, "content": content, "stop_reason": valueOr(response.FinishReason, "end_turn"), "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens}}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(payload)
}
func usageMap(usage Usage) map[string]any {
	return map[string]any{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
}
func encodeStream(format Format, events EventStream, w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, event := range events.Events {
		var payload any
		switch format {
		case FormatChatCompletions:
			payload = map[string]any{"id": "chatcmpl-translation", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": event.Text}, "finish_reason": nil}}}
		case FormatResponses:
			payload = map[string]any{"type": "response.output_text.delta", "delta": event.Text}
		case FormatAnthropicMessages:
			payload = map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": event.Text}}
		}
		encoded, _ := json.Marshal(payload)
		_, _ = io.WriteString(w, "data: "+string(encoded)+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	return nil
}
