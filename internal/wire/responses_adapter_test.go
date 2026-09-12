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

func TestResponsesStreamingCompletedEnvelopePreservesUsageDetails(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output_text\":\"hello\",\"usage\":{\"input_tokens\":100,\"output_tokens\":20,\"total_tokens\":120,\"input_tokens_details\":{\"cached_tokens\":60},\"output_tokens_details\":{\"reasoning_tokens\":5}}}}\n\n"
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	events, err := DecodeResponse(FormatResponses, response)
	if err != nil {
		t.Fatal(err)
	}
	var got Usage
	for _, event := range events.Events {
		if event.Usage != nil {
			got = *event.Usage
		}
		if event.Response != nil {
			got = event.Response.Usage
		}
	}
	if got.InputTokens != 100 || got.OutputTokens != 20 || got.TotalTokens != 120 || got.CachedReadTokens != 60 || got.ReasoningTokens != 5 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestResponsesStreamingToolCallPreservesItemAndCallIdentity(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc-1\",\"call_id\":\"call-1\",\"name\":\"tool_search\",\"arguments\":\"\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc-1\",\"delta\":\"{\\\"name\\\":\"}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc-1\",\"delta\":\"\\\"hermes-agent\\\"}\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"model\":\"m\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc-1\",\"call_id\":\"call-1\",\"name\":\"tool_search\",\"arguments\":\"{\\\"name\\\":\\\"hermes-agent\\\"}\"}]}}\n\n"
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	events, err := DecodeResponse(FormatResponses, response)
	if err != nil {
		t.Fatal(err)
	}
	var got Response
	for _, event := range events.Events {
		if event.Response != nil {
			got = *event.Response
		}
	}
	if len(got.Messages) != 1 || len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("response tools = %#v", got.Messages)
	}
	call := got.Messages[0].ToolCalls[0]
	if call.ID != "call-1" || call.Name != "tool_search" || string(call.Arguments) != `{"name":"hermes-agent"}` {
		t.Fatalf("call = %#v", call)
	}
	w := httptest.NewRecorder()
	if err := EncodeResponse(FormatChatCompletions, events, w); err != nil {
		t.Fatal(err)
	}
	encoded := w.Body.String()
	if !strings.Contains(encoded, `"name":"tool_search"`) || !strings.Contains(encoded, `"id":"call-1"`) || !strings.Contains(encoded, `hermes-agent`) {
		t.Fatalf("encoded = %s", encoded)
	}
	if !strings.Contains(encoded, `"finish_reason":"tool_calls"`) {
		t.Fatalf("missing tool_calls finish reason: %s", encoded)
	}
}

func TestResponsesFullResponseKeepsAllFunctionCalls(t *testing.T) {
	body := `{"id":"r","model":"m","status":"completed","output":[{"type":"function_call","id":"fc-1","call_id":"call-1","name":"one","arguments":"{}"},{"type":"function_call","id":"fc-2","call_id":"call-2","name":"two","arguments":"{}"}]}`
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	events, err := DecodeResponse(FormatResponses, response)
	if err != nil {
		t.Fatal(err)
	}
	message := events.Events[0].Response.Messages
	if len(message) != 1 || len(message[0].ToolCalls) != 2 {
		t.Fatalf("messages = %#v", message)
	}
}

func TestResponsesStreamingRequiresTerminalEvent(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))}
	events, err := DecodeResponse(FormatResponses, response)
	var partial *PartialResponseError
	if !errors.As(err, &partial) || len(events.Events) != 1 || events.Events[0].Text != "partial" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestAllFormatsDecodeCacheAndReasoningDetails(t *testing.T) {
	fixtures := []struct {
		format Format
		body   string
		want   Usage
	}{
		{FormatChatCompletions, `{"model":"m","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":10},"completion_tokens_details":{"reasoning_tokens":5}}}`, Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120, CachedReadTokens: 60, CacheWriteTokens: 10, ReasoningTokens: 5}},
		{FormatResponses, `{"id":"r","output_text":"ok","usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":10},"output_tokens_details":{"reasoning_tokens":5}}}`, Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120, CachedReadTokens: 60, CacheWriteTokens: 10, ReasoningTokens: 5}},
		{FormatAnthropicMessages, `{"id":"m","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":40,"output_tokens":20,"cache_read_input_tokens":60,"cache_creation_input_tokens":10,"output_tokens_details":{"thinking_tokens":5}}}`, Usage{InputTokens: 40, OutputTokens: 20, TotalTokens: 130, CachedReadTokens: 60, CacheWriteTokens: 10, ReasoningTokens: 5, InputTokensNetOfCache: true}},
	}
	for _, tc := range fixtures {
		t.Run(string(tc.format), func(t *testing.T) {
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			events, err := DecodeResponse(tc.format, response)
			if err != nil {
				t.Fatal(err)
			}
			got := events.Events[0].Response.Usage
			if got != tc.want {
				t.Fatalf("usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResponsesHistoryUsesRoleSpecificTextTypes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format Format
		body   string
	}{
		{"chat strings", FormatChatCompletions, `{"model":"m","messages":[{"role":"system","content":"rules"},{"role":"developer","content":"more rules"},{"role":"user","content":"hello"},{"role":"assistant","content":"hello back"},{"role":"user","content":"continue"}]}`},
		{"chat blocks", FormatChatCompletions, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"text","text":"hello back"}]},{"role":"user","content":"continue"}]}`},
		{"anthropic", FormatAnthropicMessages, `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"text","text":"hello back"}]},{"role":"user","content":"continue"}]}`},
		{"responses", FormatResponses, `{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]},{"role":"assistant","content":[{"type":"output_text","text":"hello back"}]},{"role":"user","content":"continue"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := DecodeRequest(tc.format, []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeRequest(FormatResponses, req)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Input []struct {
					Role    string
					Content []struct {
						Type string
						Text string
					}
				}
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Input) != len(req.Messages) {
				t.Fatalf("lost history: %s", encoded)
			}
			for i, msg := range payload.Input {
				want := "input_text"
				if msg.Role == "assistant" {
					want = "output_text"
				}
				if len(msg.Content) != 1 || msg.Content[0].Type != want || msg.Content[0].Text != req.Messages[i].Content[0].Text {
					t.Fatalf("invalid history at %d: %s", i, encoded)
				}
			}
		})
	}
}
