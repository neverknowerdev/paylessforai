package wire

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type chatAdapter struct{}

func (chatAdapter) Format() Format { return FormatChatCompletions }
func (chatAdapter) DecodeRequest(body []byte) (*Request, error) {
	return decodeRequestBody(FormatChatCompletions, body, decodeChatOptions, knownKeys("tool_choice", "parallel_tool_calls", "response_format", "stop", "max_tokens", "max_completion_tokens", "modalities", "reasoning_effort", "verbosity"), decodeChatPayload)
}
func (chatAdapter) EncodeRequest(req *Request) ([]byte, error) { return encodeChat(req) }
func (chatAdapter) DecodeResponse(resp *http.Response) (EventStream, error) {
	return decodeResponseFor(FormatChatCompletions, resp, decodeChatResponsePayload, decodeChatStreamDelta)
}
func (chatAdapter) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return encodeResponseFor(FormatChatCompletions, events, dst, encodeChatResponse, encodeChatStreamEvent)
}

func decodeChatOptions(payload map[string]json.RawMessage) (RequestOptions, error) {
	options := decodeCommonOptions(payload)
	options.MaxOutputTokens = intPointer(payload, "max_tokens", "max_completion_tokens")
	options.Stop = stringSlice(payload["stop"])
	options.Modalities = stringSlice(payload["modalities"])
	options.StructuredOutput = decodeChatStructuredOutput(payload["response_format"])
	options.ToolChoice = decodeChatToolChoice(payload["tool_choice"])
	options.ParallelToolCall = boolPointer(payload, "parallel_tool_calls")
	if effort := stringValue(payload["reasoning_effort"]); effort != "" {
		options.Reasoning = &ReasoningOptions{Effort: effort}
	}
	if verbosity := stringValue(payload["verbosity"]); verbosity != "" {
		options.Text = &TextOptions{Verbosity: verbosity}
	}
	return options, nil
}

func decodeChatToolChoice(raw json.RawMessage) *ToolChoice {
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
	if function := value["function"]; len(function) > 0 {
		var fn map[string]json.RawMessage
		if json.Unmarshal(function, &fn) == nil {
			choice.Mode = "named"
			choice.Name = stringValue(fn["name"])
		}
	}
	if choice.Mode == "function" {
		choice.Mode = "named"
		choice.Name = stringValue(value["name"])
	}
	if choice.Mode == "" {
		choice.Mode = "unsupported"
	}
	return choice
}

func decodeChatStructuredOutput(raw json.RawMessage) *StructuredOutput {
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		return nil
	}
	if stringValue(source["type"]) == "json_object" {
		return &StructuredOutput{Type: "json_object"}
	}
	var schema map[string]json.RawMessage
	if stringValue(source["type"]) != "json_schema" || json.Unmarshal(source["json_schema"], &schema) != nil {
		return nil
	}
	result := &StructuredOutput{Type: "json_schema", Schema: rawCopy(schema["schema"]), Name: stringValue(schema["name"])}
	if raw := schema["strict"]; len(raw) > 0 {
		var strict bool
		if json.Unmarshal(raw, &strict) == nil {
			result.Strict = &strict
		}
	}
	return result
}

func encodeChatToolChoice(choice *ToolChoice) (any, error) {
	if choice == nil {
		return nil, nil
	}
	switch choice.Mode {
	case "auto", "none", "required":
		return choice.Mode, nil
	case "named":
		return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}, nil
	default:
		return nil, newIncompatibility(FormatChatCompletions, "tool_choice")
	}
}

func encodeChatStructuredOutput(value *StructuredOutput) (any, error) {
	if value == nil {
		return nil, nil
	}
	if value.Type != "json_schema" && value.Type != "json_object" {
		return nil, newIncompatibility(FormatChatCompletions, "structured output")
	}
	if value.Type == "json_object" {
		return map[string]any{"type": "json_object"}, nil
	}
	schema := map[string]any{"name": value.Name, "schema": jsonRawValue(value.Schema)}
	if value.Strict != nil {
		schema["strict"] = *value.Strict
	}
	return map[string]any{"type": "json_schema", "json_schema": schema}, nil
}

func decodeChatPayload(payload map[string]json.RawMessage, request *Request) error {
	if len(payload["messages"]) == 0 {
		return errors.New("messages is required")
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
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
	decodeChatTools(payload["tools"], request)
	return nil
}

func decodeChatTools(raw json.RawMessage, request *Request) {
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
				if raw := fn["strict"]; len(raw) > 0 {
					var strict bool
					if json.Unmarshal(raw, &strict) == nil {
						tool.Strict = &strict
					}
				}
			}
		} else {
			_ = json.Unmarshal(value["name"], &tool.Name)
			tool.Parameters = ensureJSONObject(value["parameters"])
			if raw := value["strict"]; len(raw) > 0 {
				var strict bool
				if json.Unmarshal(raw, &strict) == nil {
					tool.Strict = &strict
				}
			}
		}
		request.Tools = append(request.Tools, tool)
	}
}

func encodeChat(request *Request) ([]byte, error) {
	payload := map[string]any{"model": request.Model, "messages": make([]any, 0, len(request.Messages)), "stream": request.Options.Stream}
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
		contentBlocks := make([]ContentBlock, 0, len(message.Content))
		reasoning := append([]ContentBlock(nil), message.Reasoning...)
		for _, block := range message.Content {
			if block.Type == "reasoning" {
				reasoning = append(reasoning, block)
			} else {
				contentBlocks = append(contentBlocks, block)
			}
		}
		encoded, err := encodeChatContent(contentBlocks)
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
	if err := writeChatOptions(payload, request); err != nil {
		return nil, err
	}
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			function := map[string]any{"name": tool.Name, "description": tool.Description, "parameters": jsonOrEmpty(tool.Parameters)}
			if tool.Strict != nil {
				function["strict"] = *tool.Strict
			}
			tools = append(tools, map[string]any{"type": "function", "function": function})
		}
		payload["tools"] = tools
	}
	return json.Marshal(payload)
}

func writeChatOptions(payload map[string]any, request *Request) error {
	o := request.Options
	if err := validateChatOptions(o); err != nil {
		return err
	}
	if o.MaxOutputTokens != nil {
		payload["max_completion_tokens"] = *o.MaxOutputTokens
	}
	if len(o.Modalities) > 0 {
		payload["modalities"] = o.Modalities
	}
	if o.Temperature != nil {
		payload["temperature"] = *o.Temperature
	}
	if o.TopP != nil {
		payload["top_p"] = *o.TopP
	}
	if len(o.Stop) > 0 {
		payload["stop"] = o.Stop
	}
	if choice, err := encodeChatToolChoice(o.ToolChoice); err != nil {
		return err
	} else if choice != nil {
		payload["tool_choice"] = choice
	}
	if o.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *o.ParallelToolCall
	}
	if output, err := encodeChatStructuredOutput(o.StructuredOutput); err != nil {
		return err
	} else if output != nil {
		payload["response_format"] = output
	}
	if o.Reasoning != nil && o.Reasoning.Effort != "" {
		payload["reasoning_effort"] = o.Reasoning.Effort
	}
	if o.Text != nil && o.Text.Verbosity != "" {
		payload["verbosity"] = o.Text.Verbosity
	}
	copyExtensions(payload, request, FormatChatCompletions)
	return nil
}

func validateChatOptions(o RequestOptions) error {
	if o.Reasoning != nil && (o.Reasoning.Summary != "" || o.Reasoning.Context != "" || o.Reasoning.GenerateSummary != nil) {
		return newIncompatibility(FormatChatCompletions, "Responses reasoning options")
	}
	if o.Reasoning != nil && o.Reasoning.Thinking {
		return newIncompatibility(FormatChatCompletions, "Anthropic thinking")
	}
	if len(o.Modalities) == 0 {
		return nil
	}
	return nil
}

func encodeChatContent(blocks []ContentBlock) (any, error) {
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
				return nil, newIncompatibility(FormatChatCompletions, "image")
			}
			result = append(result, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		case "audio":
			if block.Data == "" {
				return nil, newIncompatibility(FormatChatCompletions, "audio")
			}
			result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": block.Data, "format": valueOr(block.MediaType, "wav")}})
		case "file":
			result = append(result, map[string]any{"type": "file", "file": map[string]any{"file_id": block.ID, "filename": block.FileName, "file_data": mediaDataURL(block)}})
		case "reasoning":
			return nil, newIncompatibility(FormatChatCompletions, "reasoning")
		case "tool_result":
			result = append(result, map[string]any{"type": "text", "text": blocksText([]ContentBlock{block})})
		default:
			return nil, newIncompatibility(FormatChatCompletions, block.Type)
		}
	}
	return result, nil
}

func decodeChatResponsePayload(payload map[string]json.RawMessage) (Response, error) {
	result := Response{ID: stringValue(payload["id"]), Model: stringValue(payload["model"])}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(payload["choices"], &choices) != nil || len(choices) == 0 {
		return result, errors.New("choices is missing")
	}
	choice := choices[0]
	result.FinishReason = stringValue(choice["finish_reason"])
	var message map[string]json.RawMessage
	if json.Unmarshal(choice["message"], &message) == nil {
		blocks, err := decodeStringOrBlocks(message["content"], FormatChatCompletions)
		if err != nil {
			return result, err
		}
		result.Messages = []Message{{Role: Role(stringValue(message["role"])), Content: blocks, ToolCalls: decodeChatToolCalls(message["tool_calls"])}}
		if reasoning := stringValue(message["reasoning_content"]); reasoning != "" {
			result.Messages[0].Reasoning = []ContentBlock{{Type: "reasoning", Text: reasoning}}
		}
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

func decodeChatStreamDelta(payload map[string]json.RawMessage) *Event {
	var choices []map[string]json.RawMessage
	if json.Unmarshal(payload["choices"], &choices) != nil || len(choices) == 0 {
		return nil
	}
	var delta map[string]json.RawMessage
	if json.Unmarshal(choices[0]["delta"], &delta) != nil {
		return nil
	}
	if text := stringValue(delta["content"]); text != "" {
		return &Event{Type: EventTextDelta, Text: text}
	}
	if text := stringValue(delta["reasoning_content"]); text != "" {
		return &Event{Type: EventReasoningDelta, Reasoning: text}
	}
	return nil
}

func encodeChatResponse(response Response) any {
	message := map[string]any{"role": "assistant", "content": response.Text}
	if len(response.Messages) > 0 && len(response.Messages[0].ToolCalls) > 0 {
		calls := make([]any, 0, len(response.Messages[0].ToolCalls))
		for _, call := range response.Messages[0].ToolCalls {
			calls = append(calls, map[string]any{"id": call.ID, "type": valueOr(call.Type, "function"), "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
		}
		message["tool_calls"] = calls
		message["content"] = nil
	}
	return map[string]any{"id": valueOr(response.ID, "chatcmpl-translation"), "object": "chat.completion", "model": response.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": valueOr(response.FinishReason, "stop")}}, "usage": usageMap(response.Usage)}
}

func encodeChatStreamEvent(event Event) any {
	return map[string]any{"id": "chatcmpl-translation", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": event.Text}, "finish_reason": nil}}}
}
