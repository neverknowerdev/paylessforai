package wire

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestConversionPreservesMultimodalToolsAndOptions(t *testing.T) {
	body := []byte(`{"model":"source","messages":[{"role":"system","content":"rules"},{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}},{"type":"input_audio","input_audio":{"data":"YWJj","format":"wav"}}]},{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call-1","content":"answer"}],"tools":[{"type":"function","function":{"name":"lookup","description":"find","parameters":{"type":"object"}}}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"}}},"max_tokens":42}`)
	request, err := DecodeRequest(FormatChatCompletions, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 4 || len(request.Tools) != 1 || len(request.Messages[1].Content) != 3 || len(request.Messages[2].ToolCalls) != 1 {
		t.Fatalf("canonical request lost content: %#v", request)
	}
	encoded, err := EncodeRequest(FormatResponses, request)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{"input_image", "input_audio", "function_call", "function_call_output", "json_schema", "42"} {
		if !strings.Contains(text, expected) {
			t.Errorf("responses request missing %q: %s", expected, text)
		}
	}
	withoutAudio := *request
	withoutAudio.Messages = append([]Message(nil), request.Messages...)
	withoutAudio.Messages[1].Content = append([]ContentBlock(nil), request.Messages[1].Content[:2]...)
	encoded, err = EncodeRequest(FormatAnthropicMessages, &withoutAudio)
	if err != nil {
		t.Fatal(err)
	}
	text = string(encoded)
	for _, expected := range []string{"tool_use", "tool_result", "base64", "lookup"} {
		if !strings.Contains(text, expected) {
			t.Errorf("anthropic request missing %q: %s", expected, text)
		}
	}
	if _, err := EncodeRequest(FormatAnthropicMessages, request); !IsIncompatibility(err) {
		t.Fatalf("unsupported audio was silently represented: %v", err)
	}
}

func TestRequestConversionMapsReasoningEffortAcrossOpenAIDialects(t *testing.T) {
	request, err := DecodeRequest(FormatChatCompletions, []byte(`{"model":"source","messages":[{"role":"user","content":"think"}],"reasoning_effort":"high"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeRequest(FormatResponses, request)
	if err != nil {
		t.Fatal(err)
	}
	var responses map[string]any
	if err := json.Unmarshal(encoded, &responses); err != nil {
		t.Fatal(err)
	}
	if _, exists := responses["reasoning_effort"]; exists {
		t.Fatalf("Responses request leaked Chat Completions option: %s", encoded)
	}
	reasoning, ok := responses["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("Responses request did not map reasoning effort: %s", encoded)
	}

	reverse, err := DecodeRequest(FormatResponses, []byte(`{"model":"source","input":"think","reasoning":{"effort":"low"}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = EncodeRequest(FormatChatCompletions, reverse)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(encoded, &chat); err != nil {
		t.Fatal(err)
	}
	if chat["reasoning_effort"] != "low" {
		t.Fatalf("Chat Completions request did not map reasoning effort: %s", encoded)
	}
	if _, exists := chat["reasoning"]; exists {
		t.Fatalf("Chat Completions request leaked Responses option: %s", encoded)
	}
}

func TestResponseConversionAndMalformedValidation(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"r1","model":"m","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))}
	events, err := DecodeResponse(FormatChatCompletions, response)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := EncodeResponse(FormatResponses, events, recorder); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"output_text":"hello"`) {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
	malformed := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}
	if _, err := DecodeResponse(FormatChatCompletions, malformed); !IsMalformed(err) {
		t.Fatalf("malformed response was accepted: %v", err)
	}
}

func IsMalformed(err error) bool { var target *MalformedResponseError; return errors.As(err, &target) }
