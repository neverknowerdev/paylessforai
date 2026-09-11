package wire

import (
	"encoding/json"
	"errors"
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
	result := Response{ID: stringValue(payload["id"]), Model: stringValue(payload["model"]), Text: stringValue(payload["output_text"]), FinishReason: stringValue(payload["status"])}
	if len(payload["output"]) > 0 {
		var output []map[string]json.RawMessage
		if json.Unmarshal(payload["output"], &output) == nil {
			for _, item := range output {
				if typ := stringValue(item["type"]); typ == "function_call" || typ == "custom_tool_call" {
					id := stringValue(item["call_id"])
					if id == "" {
						id = stringValue(item["id"])
					}
					result.Messages = append(result.Messages, Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Type: typ, Name: stringValue(item["name"]), Arguments: jsonArgument(item["arguments"])}}})
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

func decodeResponsesStreamDelta(payload map[string]json.RawMessage) *Event {
	if text := stringValue(payload["delta"]); text != "" {
		return &Event{Type: EventTextDelta, Text: text}
	}
	return nil
}

func encodeResponsesResponse(response Response) any {
	output := []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": response.Text}}}}
	if len(response.Messages) > 0 {
		for _, call := range response.Messages[0].ToolCalls {
			output = append(output, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
		}
	}
	return map[string]any{"id": valueOr(response.ID, "resp-translation"), "object": "response", "model": response.Model, "status": "completed", "output_text": response.Text, "output": output, "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.TotalTokens}}
}

func encodeResponsesStreamEvent(event Event) any {
	return map[string]any{"type": "response.output_text.delta", "delta": event.Text}
}
