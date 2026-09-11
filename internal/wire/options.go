package wire

import "encoding/json"

// RequestOptions contains semantics that can be represented by more than one
// supported wire format. Format adapters own serialization and parsing.
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

type TextOptions struct{ Verbosity string }

func decodeCommonOptions(payload map[string]json.RawMessage) RequestOptions {
	return RequestOptions{Temperature: floatPointer(payload, "temperature"), TopP: floatPointer(payload, "top_p"), Stream: boolField(payload, "stream")}
}

func knownKeys(keys ...string) map[string]bool {
	known := map[string]bool{"model": true, "messages": true, "input": true, "instructions": true, "system": true, "tools": true, "stream": true, "temperature": true, "top_p": true}
	for _, key := range keys {
		known[key] = true
	}
	return known
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

func structuredOutputFromMap(value map[string]json.RawMessage) *StructuredOutput {
	result := &StructuredOutput{Type: stringValue(value["type"]), Name: stringValue(value["name"]), Schema: rawCopy(value["schema"])}
	if raw := value["strict"]; len(raw) > 0 {
		var strict bool
		if json.Unmarshal(raw, &strict) == nil {
			result.Strict = &strict
		}
	}
	if result.Type == "" {
		return nil
	}
	return result
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
