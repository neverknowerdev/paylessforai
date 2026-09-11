package wire

import (
	"encoding/json"
	"strings"
)

// RequestOptions contains semantics that can be represented by more than one
// supported wire format. Format adapters own the serialization of these
// options; raw provider-specific fields remain in Extensions.
type RequestOptions struct {
	MaxOutputTokens  *int64
	Temperature      *float64
	TopP             *float64
	Stop             []string
	ToolChoice       *ToolChoice
	ParallelToolCall *bool
	StructuredOutput *StructuredOutput
	Reasoning        *ReasoningOptions
	Text             *TextOptions
	Modalities       []string
	Stream           bool
}

type ToolChoice struct {
	Mode string
	Name string
}

type StructuredOutput struct {
	Type   string
	Name   string
	Schema json.RawMessage
	Strict *bool
}

type ReasoningOptions struct {
	Effort          string
	Summary         string
	Context         string
	GenerateSummary *bool
	Thinking        bool
}

type TextOptions struct {
	Verbosity string
}

func decodeOptions(format Format, payload map[string]json.RawMessage) (RequestOptions, error) {
	options := RequestOptions{Stream: boolField(payload, "stream")}
	options.Temperature = floatPointer(payload, "temperature")
	options.TopP = floatPointer(payload, "top_p")
	switch format {
	case FormatChatCompletions:
		options.MaxOutputTokens = intPointer(payload, "max_tokens", "max_completion_tokens")
		options.Stop = stringSlice(payload["stop"])
		options.Modalities = stringSlice(payload["modalities"])
		options.StructuredOutput = decodeChatStructuredOutput(payload["response_format"])
		options.ToolChoice = decodeToolChoice(payload["tool_choice"])
		options.ParallelToolCall = boolPointer(payload, "parallel_tool_calls")
		if effort := stringValue(payload["reasoning_effort"]); effort != "" {
			options.Reasoning = &ReasoningOptions{Effort: effort}
		}
		if verbosity := stringValue(payload["verbosity"]); verbosity != "" {
			options.Text = &TextOptions{Verbosity: verbosity}
		}
	case FormatResponses:
		options.MaxOutputTokens = intPointer(payload, "max_output_tokens")
		options.StructuredOutput = decodeResponsesStructuredOutput(payload["text"])
		options.ToolChoice = decodeToolChoice(payload["tool_choice"])
		options.ParallelToolCall = boolPointer(payload, "parallel_tool_calls")
		options.Reasoning = decodeResponsesReasoning(payload["reasoning"])
		options.Text = decodeResponsesText(payload["text"])
	case FormatAnthropicMessages:
		options.MaxOutputTokens = intPointer(payload, "max_tokens", "max_output_tokens")
		options.Stop = stringSlice(payload["stop_sequences"])
		options.StructuredOutput = decodeAnthropicStructuredOutput(payload["output_config"])
		options.Reasoning = decodeAnthropicReasoning(payload)
		options.ToolChoice, options.ParallelToolCall = decodeAnthropicToolChoice(payload["tool_choice"])
	default:
		return options, format.Validate()
	}
	return options, nil
}

func knownOptionKeys(format Format) map[string]bool {
	known := map[string]bool{"model": true, "messages": true, "input": true, "instructions": true, "system": true, "tools": true, "stream": true, "temperature": true, "top_p": true}
	switch format {
	case FormatChatCompletions:
		for _, key := range []string{"tool_choice", "parallel_tool_calls", "response_format", "stop", "max_tokens", "max_completion_tokens", "modalities", "reasoning_effort", "verbosity"} {
			known[key] = true
		}
	case FormatResponses:
		for _, key := range []string{"tool_choice", "parallel_tool_calls", "text", "reasoning", "max_output_tokens"} {
			known[key] = true
		}
	case FormatAnthropicMessages:
		for _, key := range []string{"tool_choice", "max_tokens", "max_output_tokens", "stop_sequences", "output_config", "thinking"} {
			known[key] = true
		}
	}
	return known
}

func decodeToolChoice(raw json.RawMessage) *ToolChoice {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		return &ToolChoice{Mode: strings.ToLower(mode)}
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return &ToolChoice{}
	}
	var typ string
	_ = json.Unmarshal(value["type"], &typ)
	choice := &ToolChoice{Mode: strings.ToLower(typ)}
	if function := value["function"]; len(function) > 0 {
		var fn map[string]json.RawMessage
		if json.Unmarshal(function, &fn) == nil {
			choice.Mode = "named"
			_ = json.Unmarshal(fn["name"], &choice.Name)
		}
	}
	if typ == "function" {
		_ = json.Unmarshal(value["name"], &choice.Name)
		choice.Mode = "named"
	}
	if choice.Mode == "" {
		choice.Mode = "unsupported"
	}
	return choice
}

func encodeToolChoice(choice *ToolChoice, format Format, parallel *bool) (any, error) {
	if choice == nil && parallel == nil {
		return nil, nil
	}
	mode := "auto"
	if choice != nil {
		mode = choice.Mode
	}
	switch format {
	case FormatChatCompletions:
		if parallel != nil && *parallel == false && choice == nil {
			return "auto", nil
		}
		switch mode {
		case "auto", "none", "required":
			return mode, nil
		case "named":
			return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}, nil
		default:
			return nil, newIncompatibility(format, "tool_choice")
		}
	case FormatResponses:
		switch mode {
		case "auto", "none", "required":
			return mode, nil
		case "named":
			return map[string]any{"type": "function", "name": choice.Name}, nil
		default:
			return nil, newIncompatibility(format, "tool_choice")
		}
	case FormatAnthropicMessages:
		result := map[string]any{}
		switch mode {
		case "auto", "none":
			result["type"] = mode
		case "required":
			result["type"] = "any"
		case "named":
			result["type"], result["name"] = "tool", choice.Name
		default:
			return nil, newIncompatibility(format, "tool_choice")
		}
		if parallel != nil {
			result["disable_parallel_tool_use"] = !*parallel
		}
		return result, nil
	default:
		return nil, format.Validate()
	}
}

func structuredOutputValue(value *StructuredOutput, format Format) (any, error) {
	if value == nil {
		return nil, nil
	}
	if value.Type != "json_schema" && value.Type != "json_object" {
		return nil, newIncompatibility(format, "structured output")
	}
	if format == FormatAnthropicMessages {
		if value.Type != "json_schema" {
			return nil, newIncompatibility(format, "structured output")
		}
		return map[string]any{"type": "json_schema", "schema": jsonRawValue(value.Schema)}, nil
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

func decodeChatStructuredOutput(raw json.RawMessage) *StructuredOutput {
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		return nil
	}
	var typ string
	_ = json.Unmarshal(source["type"], &typ)
	if typ == "json_object" {
		return &StructuredOutput{Type: typ}
	}
	var schema map[string]json.RawMessage
	if typ != "json_schema" || json.Unmarshal(source["json_schema"], &schema) != nil {
		return nil
	}
	result := &StructuredOutput{Type: "json_schema", Schema: rawCopy(schema["schema"])}
	_ = json.Unmarshal(schema["name"], &result.Name)
	if raw := schema["strict"]; len(raw) > 0 {
		var strict bool
		if json.Unmarshal(raw, &strict) == nil {
			result.Strict = &strict
		}
	}
	return result
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

func structuredOutputFromMap(value map[string]json.RawMessage) *StructuredOutput {
	var result StructuredOutput
	_ = json.Unmarshal(value["type"], &result.Type)
	_ = json.Unmarshal(value["name"], &result.Name)
	result.Schema = rawCopy(value["schema"])
	if raw := value["strict"]; len(raw) > 0 {
		var strict bool
		if json.Unmarshal(raw, &strict) == nil {
			result.Strict = &strict
		}
	}
	if result.Type == "" {
		return nil
	}
	return &result
}

func decodeResponsesReasoning(raw json.RawMessage) *ReasoningOptions {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	result := &ReasoningOptions{}
	_ = json.Unmarshal(value["effort"], &result.Effort)
	_ = json.Unmarshal(value["summary"], &result.Summary)
	_ = json.Unmarshal(value["context"], &result.Context)
	if raw := value["generate_summary"]; len(raw) > 0 {
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			result.GenerateSummary = &b
		}
	}
	return result
}

func decodeResponsesText(raw json.RawMessage) *TextOptions {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	result := &TextOptions{}
	_ = json.Unmarshal(value["verbosity"], &result.Verbosity)
	if result.Verbosity == "" {
		return nil
	}
	return result
}

func decodeAnthropicReasoning(payload map[string]json.RawMessage) *ReasoningOptions {
	result := &ReasoningOptions{}
	hasValue := false
	var output map[string]json.RawMessage
	if json.Unmarshal(payload["output_config"], &output) == nil {
		var effort string
		_ = json.Unmarshal(output["effort"], &effort)
		if effort != "" {
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
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return nil, nil
	}
	var typ, name string
	_ = json.Unmarshal(value["type"], &typ)
	_ = json.Unmarshal(value["name"], &name)
	choice := &ToolChoice{Mode: strings.ToLower(typ), Name: name}
	if typ == "any" {
		choice.Mode = "required"
	}
	if typ == "tool" {
		choice.Mode = "named"
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

func stringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return values
	}
	var value string
	if json.Unmarshal(raw, &value) == nil && value != "" {
		return []string{value}
	}
	return nil
}

func (o RequestOptions) validateTarget(format Format) error {
	if o.Text != nil && o.Text.Verbosity != "" && format == FormatAnthropicMessages {
		return newIncompatibility(format, "text verbosity")
	}
	if len(o.Modalities) > 0 && format != FormatChatCompletions {
		return newIncompatibility(format, "output modalities")
	}
	if len(o.Stop) > 0 && format == FormatResponses {
		return newIncompatibility(format, "stop sequences")
	}
	if o.Reasoning != nil && o.Reasoning.Summary != "" && format != FormatResponses {
		return newIncompatibility(format, "reasoning summary")
	}
	if o.Reasoning != nil && o.Reasoning.Thinking && format != FormatAnthropicMessages {
		return newIncompatibility(format, "Anthropic thinking")
	}
	if o.Reasoning != nil && o.Reasoning.Context != "" && format != FormatResponses {
		return newIncompatibility(format, "reasoning context")
	}
	if o.Reasoning != nil && o.Reasoning.GenerateSummary != nil && format != FormatResponses {
		return newIncompatibility(format, "reasoning summary generation")
	}
	return nil
}

func writeChatOptions(payload map[string]any, request *Request) error {
	o := request.Options
	if err := o.validateTarget(FormatChatCompletions); err != nil {
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
	if choice, err := encodeToolChoice(o.ToolChoice, FormatChatCompletions, o.ParallelToolCall); err != nil {
		return err
	} else if choice != nil {
		payload["tool_choice"] = choice
	}
	if o.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *o.ParallelToolCall
	}
	if output, err := structuredOutputValue(o.StructuredOutput, FormatChatCompletions); err != nil {
		return err
	} else if output != nil {
		payload["response_format"] = map[string]any{"type": output.(map[string]any)["type"]}
		if outputMap, ok := output.(map[string]any); ok && outputMap["type"] == "json_schema" {
			schema := map[string]any{"name": outputMap["name"], "schema": outputMap["schema"]}
			if strict, exists := outputMap["strict"]; exists {
				schema["strict"] = strict
			}
			payload["response_format"] = map[string]any{"type": "json_schema", "json_schema": schema}
		}
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

func writeResponsesOptions(payload map[string]any, request *Request) error {
	o := request.Options
	if err := o.validateTarget(FormatResponses); err != nil {
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
	if choice, err := encodeToolChoice(o.ToolChoice, FormatResponses, o.ParallelToolCall); err != nil {
		return err
	} else if choice != nil {
		payload["tool_choice"] = choice
	}
	if o.ParallelToolCall != nil {
		payload["parallel_tool_calls"] = *o.ParallelToolCall
	}
	text := map[string]any{}
	if output, err := structuredOutputValue(o.StructuredOutput, FormatResponses); err != nil {
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

func writeAnthropicOptions(payload map[string]any, request *Request) error {
	o := request.Options
	if err := o.validateTarget(FormatAnthropicMessages); err != nil {
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
	if choice, err := encodeToolChoice(o.ToolChoice, FormatAnthropicMessages, o.ParallelToolCall); err != nil {
		return err
	} else if choice != nil {
		payload["tool_choice"] = choice
	}
	if o.StructuredOutput != nil || (o.Reasoning != nil && o.Reasoning.Effort != "") {
		config := map[string]any{}
		if o.Reasoning != nil && o.Reasoning.Effort != "" {
			config["effort"] = o.Reasoning.Effort
		}
		if output, err := structuredOutputValue(o.StructuredOutput, FormatAnthropicMessages); err != nil {
			return err
		} else if output != nil {
			config["format"] = output
		}
		payload["output_config"] = config
	}
	copyExtensions(payload, request, FormatAnthropicMessages)
	return nil
}

func copyExtensions(payload map[string]any, request *Request, target Format) {
	if request.SourceFormat != target {
		return
	}
	for key, value := range request.Extensions {
		if _, exists := payload[key]; !exists {
			payload[key] = jsonRawValue(value)
		}
	}
}
