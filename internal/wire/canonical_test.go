package wire

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestFormatAdaptersTranslateCanonicalOptionsWithoutLeakingSourceFields(t *testing.T) {
	body := []byte(`{"model":"source","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high","verbosity":"low","max_completion_tokens":42,"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"strict":true}},"tool_choice":{"type":"function","function":{"name":"lookup"}},"parallel_tool_calls":false,"store":true}`)
	request, err := DecodeRequest(FormatChatCompletions, body)
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
	if responses["max_output_tokens"] != float64(42) {
		t.Fatalf("wrong Responses token option: %s", encoded)
	}
	if _, exists := responses["max_completion_tokens"]; exists {
		t.Fatalf("Chat token option leaked into Responses: %s", encoded)
	}
	if _, exists := responses["reasoning_effort"]; exists {
		t.Fatalf("reasoning_effort leaked into Responses: %s", encoded)
	}
	if _, exists := responses["verbosity"]; exists {
		t.Fatalf("verbosity leaked into Responses: %s", encoded)
	}
	if _, exists := responses["store"]; exists {
		t.Fatalf("source extension leaked into Responses: %s", encoded)
	}
	textOptions, ok := responses["text"].(map[string]any)
	if !ok || textOptions["verbosity"] != "low" {
		t.Fatalf("missing Responses text verbosity: %s", encoded)
	}
	reasoning, ok := responses["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("missing Responses reasoning effort: %s", encoded)
	}
	choice, ok := responses["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("wrong Responses tool choice: %s", encoded)
	}
	format, ok := textOptions["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["strict"] != true {
		t.Fatalf("wrong Responses structured output: %s", encoded)
	}
}

func TestFormatAdaptersTranslateAnthropicOptionsToChat(t *testing.T) {
	body := []byte(`{"model":"source","messages":[{"role":"user","content":"hello"}],"max_tokens":42,"stop_sequences":["DONE"],"output_config":{"effort":"medium","format":{"type":"json_schema","schema":{"type":"object"}}},"tool_choice":{"type":"tool","name":"lookup","disable_parallel_tool_use":true}}`)
	request, err := DecodeRequest(FormatAnthropicMessages, body)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeRequest(FormatChatCompletions, request)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(encoded, &chat); err != nil {
		t.Fatal(err)
	}
	if chat["max_completion_tokens"] != float64(42) || chat["stop"].([]any)[0] != "DONE" || chat["reasoning_effort"] != "medium" {
		t.Fatalf("Anthropic options were not translated to Chat: %s", encoded)
	}
	if _, exists := chat["max_tokens"]; exists {
		t.Fatalf("deprecated Chat token option emitted: %s", encoded)
	}
	if _, exists := chat["stop_sequences"]; exists {
		t.Fatalf("Anthropic stop option leaked into Chat: %s", encoded)
	}
	choice, ok := chat["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" || choice["function"].(map[string]any)["name"] != "lookup" {
		t.Fatalf("wrong Chat tool choice: %s", encoded)
	}
}

func TestFormatAdaptersRejectOptionsWithoutSemanticEquivalent(t *testing.T) {
	request, err := DecodeRequest(FormatChatCompletions, []byte(`{"model":"source","messages":[{"role":"user","content":"hello"}],"verbosity":"low"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeRequest(FormatAnthropicMessages, request); !IsIncompatibility(err) {
		t.Fatalf("Anthropic accepted unsupported verbosity: %v", err)
	}
	request, err = DecodeRequest(FormatChatCompletions, []byte(`{"model":"source","messages":[{"role":"user","content":"hello"}],"stop":["DONE"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeRequest(FormatResponses, request); !IsIncompatibility(err) {
		t.Fatalf("Responses accepted unsupported stop sequences: %v", err)
	}
	request, err = DecodeRequest(FormatAnthropicMessages, []byte(`{"model":"source","max_tokens":10,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeRequest(FormatChatCompletions, request); !IsIncompatibility(err) {
		t.Fatalf("Chat accepted Anthropic thinking without an equivalent: %v", err)
	}
}

func TestCodecForReturnsFormatSpecificAdapters(t *testing.T) {
	for _, format := range []Format{FormatChatCompletions, FormatResponses, FormatAnthropicMessages} {
		codec, err := CodecFor(format)
		if err != nil {
			t.Fatal(err)
		}
		if codec.Format() != format {
			t.Fatalf("adapter format = %s, want %s", codec.Format(), format)
		}
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

func TestStreamResponseDeliversBeforeUpstreamCompletes(t *testing.T) {
	reader, writer := io.Pipe()
	release := make(chan struct{})
	go func() {
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		<-release
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
		_ = writer.Close()
	}()
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
	got := make(chan Event, 1)
	done := make(chan error, 1)
	go func() {
		_, err := StreamResponse(FormatChatCompletions, response, func(event Event) error {
			got <- event
			return nil
		})
		done <- err
	}()
	select {
	case event := <-got:
		if event.Type != EventTextDelta || event.Text != "first" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("first stream event was buffered until upstream completion")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEndStreamUsesProtocolSpecificTerminal(t *testing.T) {
	for _, format := range []Format{FormatChatCompletions, FormatResponses, FormatAnthropicMessages} {
		t.Run(string(format), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			flusher, err := StartStream(format, recorder)
			if err != nil {
				t.Fatal(err)
			}
			if err := EndStream(format, recorder, flusher); err != nil {
				t.Fatal(err)
			}
			body := recorder.Body.String()
			if format == FormatChatCompletions && !strings.Contains(body, "[DONE]") {
				t.Fatalf("missing Chat terminal: %s", body)
			}
			if format != FormatChatCompletions && strings.Contains(body, "[DONE]") {
				t.Fatalf("non-Chat stream used Chat terminal: %s", body)
			}
		})
	}
}
