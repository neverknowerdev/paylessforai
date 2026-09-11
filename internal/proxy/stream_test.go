package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/usage"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

type failingStreamWriter struct {
	*httptest.ResponseRecorder
	failOn string
	cancel context.CancelFunc
}

func (w *failingStreamWriter) WriteString(value string) (int, error) { return w.Write([]byte(value)) }

func (w *failingStreamWriter) Write(data []byte) (int, error) {
	if w.failOn == "" || strings.Contains(string(data), w.failOn) {
		if w.cancel != nil {
			w.cancel()
			return 0, context.Canceled
		}
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(data)
}

func TestStreamingFailuresPersistUsageAndPreventFailover(t *testing.T) {
	const usageFrame = "data: {\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n"
	const textFrame = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
	for _, tc := range []struct {
		name       string
		translated bool
		json       bool
		body       string
		failOn     string
		disconnect bool
	}{
		{name: "translated startup write", translated: true, body: usageFrame + textFrame + "data: [DONE]\n\n", failOn: "response.created"},
		{name: "translated terminal write", translated: true, body: usageFrame + textFrame + "data: [DONE]\n\n", failOn: "response.completed"},
		{name: "translated client disconnect", translated: true, body: usageFrame + textFrame + "data: [DONE]\n\n", failOn: "response.output_text.delta", disconnect: true},
		{name: "translated truncated", translated: true, body: usageFrame + textFrame},
		{name: "buffered JSON write", translated: true, json: true, body: `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`, failOn: "response.created"},
		{name: "passthrough write", body: usageFrame + textFrame + "data: [DONE]\n\n", failOn: "data:"},
		{name: "passthrough disconnect", body: usageFrame + textFrame + "data: [DONE]\n\n", failOn: "data:", disconnect: true},
		{name: "passthrough truncated", body: usageFrame + textFrame},
		{name: "passthrough upstream failure", body: usageFrame + "data: {\"type\":\"response.failed\"}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first providers.Client
			if tc.translated {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.json {
						w.Header().Set("Content-Type", "application/json")
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					_, _ = io.WriteString(w, tc.body)
				}))
				defer server.Close()
				// An explicit endpoint pins the upstream wire format to Chat.
				first = &translatingProvider{HTTPClient: providers.NewHTTPClient("surplus", server.URL+"/v1/chat/completions", "secret"), models: []providers.Model{model("model-a", 1, 1)}}
			} else {
				first = &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				}}}
			}
			second := &fakeProvider{name: "openrouter", models: []providers.Model{model("model-a", 2, 2)}}
			proxy, repos, secret := testProxy(t, first, second)
			defer repos.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			protocol := matcher.ProtocolChatCompletions
			path, body := "/v1/chat/completions", `{"model":"model-a","messages":[{"role":"user","content":"hello"}],"stream":true}`
			if tc.translated {
				protocol = matcher.ProtocolResponses
				path = "/v1/responses"
				body = `{"model":"model-a","input":"hello","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer "+secret)
			recorder := httptest.NewRecorder()
			var writer http.ResponseWriter = recorder
			if tc.failOn != "" {
				failing := &failingStreamWriter{ResponseRecorder: recorder, failOn: tc.failOn}
				if tc.disconnect {
					failing.cancel = cancel
				}
				writer = failing
			}
			proxy.ServeHTTP(writer, request, protocol)
			second.mu.Lock()
			attempts := len(second.modelsSeen)
			second.mu.Unlock()
			if attempts != 0 {
				t.Fatalf("failed over after commitment: %d", attempts)
			}
			var state, code, attemptState string
			var input, output, total int
			err := repos.DB().QueryRow(`SELECT r.state, r.error_code, a.state, u.input_tokens,u.output_tokens,u.total_tokens FROM proxy_requests r JOIN proxy_attempts a ON a.request_id=r.id JOIN request_usage u ON u.request_id=r.id`).Scan(&state, &code, &attemptState, &input, &output, &total)
			if err != nil {
				t.Fatal(err)
			}
			wantCode := "stream_error"
			if tc.disconnect {
				wantCode = "client_disconnected"
			}
			if state != "partial" || attemptState != "partial" || code != wantCode || input != 7 || output != 3 || total != 10 {
				t.Fatalf("state=%s code=%s attempt=%s usage=%d/%d/%d body=%s", state, code, attemptState, input, output, total, recorder.Body.String())
			}
		})
	}
}

func TestStreamFinalizeContextSurvivesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finalCtx, finish := streamFinalizeContext(ctx)
	defer finish()
	if errors.Is(finalCtx.Err(), context.Canceled) {
		t.Fatal("finalization inherited cancellation")
	}
	if _, ok := finalCtx.Deadline(); !ok {
		t.Fatal("finalization has no time bound")
	}
}

func TestSparseStreamUsageIncludesCacheAndOutput(t *testing.T) {
	var stats usage.Stats
	observeWireEvent(&stats, wire.Event{Type: wire.EventUsage, Usage: &wire.Usage{InputTokens: 7, CachedReadTokens: 10, InputTokensNetOfCache: true, TotalTokens: 17}})
	observeWireEvent(&stats, wire.Event{Type: wire.EventUsage, Usage: &wire.Usage{OutputTokens: 3, TotalTokens: 3}})
	if stats.InputTokens != 7 || stats.OutputTokens != 3 || stats.TotalTokens != 20 {
		t.Fatalf("usage: %+v", stats)
	}
}

func TestBufferedPartialStreamDoesNotEmitSuccessTerminal(t *testing.T) {
	proxy := &Proxy{}
	recorder := httptest.NewRecorder()
	upstreamErr := &wire.PartialResponseError{Err: io.ErrUnexpectedEOF}
	err := proxy.completeTranslatedStream(context.Background(), recorder, "request", wire.EventStream{Stream: true, Events: []wire.Event{{Type: wire.EventTextDelta, Text: "partial"}}}, 0, 0, matcher.Price{}, matcher.Price{}, wire.FormatResponses, upstreamErr)
	var partial *partialStreamError
	if !errors.As(err, &partial) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "partial") || strings.Contains(body, "response.completed") {
		t.Fatalf("partial delivery: %s", body)
	}
}
