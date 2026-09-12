package wire

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type anthropicAdapter struct{}

func (anthropicAdapter) Format() Format { return FormatAnthropicMessages }
func (anthropicAdapter) DecodeRequest(body []byte) (*Request, error) {
	return decodeRequestBody(FormatAnthropicMessages, body, decodeAnthropicOptions, knownKeys("max_tokens", "output_config", "thinking", "tool_choice", "stop_sequences"), decodeAnthropicPayload)
}
func (anthropicAdapter) EncodeRequest(req *Request) ([]byte, error) { return encodeAnthropic(req) }
func (anthropicAdapter) DecodeResponse(resp *http.Response) (EventStream, error) {
	return decodeResponseFor(FormatAnthropicMessages, resp, decodeAnthropicResponsePayload, decodeAnthropicStreamDelta)
}
func (anthropicAdapter) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return encodeResponseFor(FormatAnthropicMessages, events, dst, encodeAnthropicResponse, encodeAnthropicStreamEvent)
}

func decodeAnthropicOptions(payload map[string]json.RawMessage) (RequestOptions, error) {
	options := decodeCommonOptions(payload)
	options.MaxOutputTokens = intPointer(payload, "max_tokens")
	options.Stop = stringSlice(payload["stop_sequences"])
	options.StructuredOutput = decodeAnthropicStructuredOutput(payload["output_config"])
	options.Reasoning = decodeAnthropicReasoning(payload)
	options.ToolChoice, options.ParallelToolCall = decodeAnthropicToolChoice(payload["tool_choice"])
	return options, nil
}

func decodeAnthropicPayload(payload map[string]json.RawMessage, request *Request) error {
	if len(payload["messages"]) == 0 {
		return errors.New("messages is required")
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		return err
	}
	if raw := payload["system"]; len(raw) > 0 {
		blocks, err := decodeStringOrBlocks(raw, FormatAnthropicMessages)
		if err != nil {
			return err
		}
		request.Messages = append(request.Messages, Message{Role: RoleSystem, Content: blocks})
	}
	for _, item := range messages {
		role := Role(strings.ToLower(stringValue(item["role"])))
		blocks, err := decodeStringOrBlocks(item["content"], FormatAnthropicMessages)
		if err != nil {
			return err
		}
		message := Message{Role: role}
		for _, block := range blocks {
			if block.Type == "tool_call" {
				message.ToolCalls = append(message.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Arguments, Type: "function"})
				continue
			}
			if block.Type == "tool_result" && message.ToolCallID == "" {
				message.ToolCallID = block.ID
			}
			message.Content = append(message.Content, block)
		}
		request.Messages = append(request.Messages, message)
	}
	decodeAnthropicTools(payload["tools"], request)
	return nil
}

func decodeAnthropicTools(raw json.RawMessage, request *Request) {
	var values []map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	for _, value := range values {
		tool := Tool{Type: "function", Name: stringValue(value["name"]), Description: stringValue(value["description"]), Parameters: ensureJSONObject(value["input_schema"])}
		if raw := value["strict"]; len(raw) > 0 {
			var strict bool
			if json.Unmarshal(raw, &strict) == nil {
				tool.Strict = &strict
			}
		}
		request.Tools = append(request.Tools, tool)
	}
}

func decodeAnthropicReasoning(payload map[string]json.RawMessage) *ReasoningOptions {
	result := &ReasoningOptions{}
	hasValue := false
	var output map[string]json.RawMessage
	if json.Unmarshal(payload["output_config"], &output) == nil {
		if effort := stringValue(output["effort"]); effort != "" {
			result.Effort = effort
			hasValue = true
		}
	}
	var thinking map[string]json.RawMessage
	if len(payload["thinking"]) > 0 && json.Unmarshal(payload["thinking"], &thinking) == nil {
		result.Thinking = true
		hasValue = true
	}
	if !hasValue {
		return nil
	}
	return result
}

func decodeAnthropicToolChoice(raw json.RawMessage) (*ToolChoice, *bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil, nil
	}
	choice := &ToolChoice{Mode: strings.ToLower(stringValue(value["type"])), Name: stringValue(value["name"])}
	if choice.Mode == "any" {
		choice.Mode = "required"
	}
	if choice.Mode == "tool" {
		choice.Mode = "named"
	}
	if choice.Mode == "" {
		choice.Mode = "unsupported"
	}
	var parallel *bool
	if raw := value["disable_parallel_tool_use"]; len(raw) > 0 {
		var disabled bool
		if json.Unmarshal(raw, &disabled) == nil {
			enabled := !disabled
			parallel = &enabled
		}
	}
	return choice, parallel
}

func decodeAnthropicStructuredOutput(raw json.RawMessage) *StructuredOutput {
	var config map[string]json.RawMessage
	if json.Unmarshal(raw, &config) != nil {
		return nil
	}
	var format map[string]json.RawMessage
	if json.Unmarshal(config["format"], &format) != nil {
		return nil
	}
	return structuredOutputFromMap(format)
}

func encodeAnthropic(request *Request) ([]byte, error) {
	payload := map[string]any{"model": request.Model, "messages": make([]any, 0), "stream": request.Options.Stream}
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
			content = append(content, map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": blocksText(message.Content)})
			messages = append(messages, map[string]any{"role": "user", "content": content})
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
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			item := map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": jsonOrEmpty(tool.Parameters)}
			if tool.Strict != nil {
				item["strict"] = *tool.Strict
			}
			tools = append(tools, item)
		}
		payload["tools"] = tools
	}
	if err := writeAnthropicOptions(payload, request); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func writeAnthropicOptions(payload map[string]any, request *Request) error {
	o := request.Options
	if err := validateAnthropicOptions(o); err != nil {
		return err
	}
	if o.MaxOutputTokens != nil {
		payload["max_tokens"] = *o.MaxOutputTokens
	}
	if o.Temperature != nil {
		payload["temperature"] = *o.Temperature
	}
	if o.TopP != nil {
		payload["top_p"] = *o.TopP
	}
	if len(o.Stop) > 0 {
		payload["stop_sequences"] = o.Stop
	}
	if choice, err := encodeAnthropicToolChoice(o.ToolChoice, o.ParallelToolCall); err != nil {
		return err
	} else if choice != nil {
		payload["tool_choice"] = choice
	}
	if o.StructuredOutput != nil || (o.Reasoning != nil && o.Reasoning.Effort != "") {
		config := map[string]any{}
		if o.Reasoning != nil && o.Reasoning.Effort != "" {
			config["effort"] = o.Reasoning.Effort
		}
		if output, err := encodeAnthropicStructuredOutput(o.StructuredOutput); err != nil {
			return err
		} else if output != nil {
			config["format"] = output
		}
		payload["output_config"] = config
	}
	if o.Reasoning != nil && o.Reasoning.Thinking {
		payload["thinking"] = map[string]any{"type": "enabled"}
	}
	copyExtensions(payload, request, FormatAnthropicMessages)
	return nil
}

func validateAnthropicOptions(o RequestOptions) error {
	if o.Text != nil && o.Text.Verbosity != "" {
		return newIncompatibility(FormatAnthropicMessages, "text verbosity")
	}
	if len(o.Modalities) > 0 {
		return newIncompatibility(FormatAnthropicMessages, "output modalities")
	}
	if o.Reasoning != nil && o.Reasoning.Summary != "" {
		return newIncompatibility(FormatAnthropicMessages, "reasoning summary")
	}
	if o.Reasoning != nil && o.Reasoning.Context != "" {
		return newIncompatibility(FormatAnthropicMessages, "reasoning context")
	}
	if o.Reasoning != nil && o.Reasoning.GenerateSummary != nil {
		return newIncompatibility(FormatAnthropicMessages, "reasoning summary generation")
	}
	return nil
}

func encodeAnthropicToolChoice(choice *ToolChoice, parallel *bool) (any, error) {
	if choice == nil && parallel == nil {
		return nil, nil
	}
	mode := "auto"
	if choice != nil {
		mode = choice.Mode
	}
	result := map[string]any{}
	switch mode {
	case "auto", "none":
		result["type"] = mode
	case "required":
		result["type"] = "any"
	case "named":
		result["type"], result["name"] = "tool", choice.Name
	default:
		return nil, newIncompatibility(FormatAnthropicMessages, "tool_choice")
	}
	if parallel != nil {
		result["disable_parallel_tool_use"] = !*parallel
	}
	return result, nil
}

func encodeAnthropicStructuredOutput(value *StructuredOutput) (any, error) {
	if value == nil {
		return nil, nil
	}
	if value.Type != "json_schema" {
		return nil, newIncompatibility(FormatAnthropicMessages, "structured output")
	}
	return map[string]any{"type": "json_schema", "schema": jsonRawValue(value.Schema)}, nil
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
			if len(parts) != 2 {
				return nil, newIncompatibility(FormatAnthropicMessages, "image")
			}
			media := strings.TrimSuffix(strings.TrimPrefix(parts[0], "data:"), ";base64")
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

func decodeAnthropicResponsePayload(payload map[string]json.RawMessage) (Response, error) {
	result := Response{ID: stringValue(payload["id"]), Model: stringValue(payload["model"]), FinishReason: stringValue(payload["stop_reason"])}
	blocks, err := decodeStringOrBlocks(payload["content"], FormatAnthropicMessages)
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
	decodeUsage(payload, &result.Usage)
	if len(result.Messages) == 0 && result.Text == "" {
		return result, errors.New("response has no semantic content")
	}
	return result, nil
}

func decodeAnthropicStreamDelta(payload map[string]json.RawMessage) *Event {
	var delta map[string]json.RawMessage
	if json.Unmarshal(payload["delta"], &delta) == nil {
		if text := stringValue(delta["text"]); text != "" {
			return &Event{Type: EventTextDelta, Text: text}
		}
	}
	return nil
}

func encodeAnthropicResponse(response Response) any {
	content := []any{map[string]any{"type": "text", "text": response.Text}}
	for _, message := range response.Messages {
		for _, call := range message.ToolCalls {
			content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": jsonObject(call.Arguments)})
		}
	}
	usage := map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens}
	if response.Usage.CachedReadTokens != 0 {
		usage["cache_read_input_tokens"] = response.Usage.CachedReadTokens
	}
	if response.Usage.CacheWriteTokens != 0 {
		usage["cache_creation_input_tokens"] = response.Usage.CacheWriteTokens
	}
	if response.Usage.ReasoningTokens != 0 {
		usage["output_tokens_details"] = map[string]any{"thinking_tokens": response.Usage.ReasoningTokens}
	}
	return map[string]any{"id": valueOr(response.ID, "msg-translation"), "type": "message", "role": "assistant", "model": response.Model, "content": content, "stop_reason": valueOr(response.FinishReason, "end_turn"), "usage": usage}
}

func encodeAnthropicStreamEvent(event Event) any {
	return map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": event.Text}}
}
