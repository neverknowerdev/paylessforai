package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/retry"
)

type fakeProvider struct {
	name       string
	models     []providers.Model
	mu         sync.Mutex
	protocols  []matcher.Protocol
	modelsSeen []string
	responses  []func(*http.Request) (*http.Response, error)
}

type failingReader struct {
	data []byte
	done bool
}

func (r *failingReader) Read(buffer []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(buffer, r.data), nil
	}
	return 0, errors.New("simulated stream disconnect")
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Discover(context.Context) ([]providers.Model, error) { return f.models, nil }

func (f *fakeProvider) Do(_ context.Context, protocol matcher.Protocol, model string, body []byte) (*http.Response, error) {
	f.mu.Lock()
	f.protocols = append(f.protocols, protocol)
	f.modelsSeen = append(f.modelsSeen, model)
	var response func(*http.Request) (*http.Response, error)
	if len(f.responses) > 0 {
		response = f.responses[0]
		f.responses = f.responses[1:]
	}
	f.mu.Unlock()
	if response == nil {
		return successResponse(`{"id":"ok","usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`), nil
	}
	request := httptest.NewRequest(http.MethodPost, "http://provider.invalid", strings.NewReader(string(body)))
	return response(request)
}

func successResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func testProxy(t *testing.T, clients ...providers.Client) (*Proxy, *db.Store, string) {
	t.Helper()
	db, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := db.CreateClientKey(context.Background(), "test")
	if err != nil || key.ID == "" {
		db.Close()
		t.Fatal(err)
	}
	manager := catalog.New(clients)
	if err := manager.Refresh(context.Background()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return New(manager, db), db, secret
}

func model(name string, input, output int64) providers.Model {
	return providers.Model{ID: name, Name: name, ContextLength: 10000, MaxCompletionTokens: 1000, Pricing: matcher.Price{InputPicoUSDPerToken: input, OutputPicoUSDPerToken: output}, PriceAvailable: true, SupportedParameters: []string{"tools", "response_format"}}
}

func TestProxySelectsCheapestRouteAndPersistsUsage(t *testing.T) {
	expensive := &fakeProvider{name: "openrouter", models: []providers.Model{model("model-a", 10, 10)}}
	cheap := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, expensive, cheap)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"ok"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	cheap.mu.Lock()
	defer cheap.mu.Unlock()
	if len(cheap.modelsSeen) != 1 || cheap.modelsSeen[0] != "model-a" {
		t.Fatalf("cheap provider was not selected: %#v", cheap.modelsSeen)
	}
	var state string
	if err := db.DB().QueryRow(`SELECT state FROM proxy_requests`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("request state %s", state)
	}
	var actualCost int64
	if err := db.DB().QueryRow(`SELECT actual_cost_pico_usd FROM request_usage`).Scan(&actualCost); err != nil {
		t.Fatal(err)
	}
	if actualCost != 3 {
		t.Fatalf("expected usage-derived actual cost 3, got %d", actualCost)
	}
	items, err := db.ListRequestStats(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].OfficialCostPico == nil || *items[0].OfficialCostPico != 30 || items[0].DiscountPico == nil || *items[0].DiscountPico != 27 {
		t.Fatalf("expected official cost and discount, got %#v, %v", items, err)
	}
}

func TestProxyUsesProviderReportedCostAsActualCost(t *testing.T) {
	provider := &fakeProvider{name: "openrouter", models: []providers.Model{model("model-a", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return successResponse(`{"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"cost":"0.000009"}}`), nil
		},
	}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	var actualCost int64
	if err := db.DB().QueryRow(`SELECT actual_cost_pico_usd FROM request_usage`).Scan(&actualCost); err != nil {
		t.Fatal(err)
	}
	if actualCost != 9_000_000 {
		t.Fatalf("expected provider-reported actual cost, got %d", actualCost)
	}
	var discount int64
	if err := db.DB().QueryRow(`SELECT discount_pico_usd FROM request_usage`).Scan(&discount); err != nil {
		t.Fatal(err)
	}
	if discount != 0 {
		t.Fatalf("overpriced request must report zero savings, got %d", discount)
	}
}

func TestOfficialPricingUsesProviderReferencePrice(t *testing.T) {
	route := matcher.Route{Provider: "surplus", Price: matcher.Price{InputPicoUSDPerToken: 1}, OfficialPrice: matcher.Price{InputPicoUSDPerToken: 100}, OfficialPriceAvailable: true}
	price, _ := officialPricing([]matcher.RankedRoute{{Route: route, ExpectedCost: 1}})
	if price.InputPicoUSDPerToken != 100 {
		t.Fatalf("expected provider reference price, got %#v", price)
	}
}

func TestProxyRetriesThenFailsOver(t *testing.T) {
	first := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "surplus", StatusCode: 500, Class: retry.ErrorServer, Message: "temporary"}
		},
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "surplus", StatusCode: 500, Class: retry.ErrorServer, Message: "temporary"}
		},
	}}
	second := &fakeProvider{name: "openrouter", models: []providers.Model{model("model-a", 2, 2)}}
	proxy, db, secret := testProxy(t, first, second)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	first.mu.Lock()
	firstCalls := len(first.modelsSeen)
	first.mu.Unlock()
	second.mu.Lock()
	secondCalls := len(second.modelsSeen)
	second.mu.Unlock()
	if firstCalls != 2 || secondCalls != 1 {
		t.Fatalf("unexpected call counts: first=%d second=%d", firstCalls, secondCalls)
	}
}

func TestProxyFailsOverImmediatelyFromFreeRoute(t *testing.T) {
	freeModel := model("model-a:free", 0, 0)
	freeModel.Free = true
	free := &fakeProvider{name: "openrouter", models: []providers.Model{freeModel}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "openrouter", StatusCode: http.StatusServiceUnavailable, Class: retry.ErrorServer, Message: "free capacity exhausted"}
		},
	}}
	paid := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, free, paid)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	free.mu.Lock()
	freeCalls := len(free.modelsSeen)
	free.mu.Unlock()
	paid.mu.Lock()
	paidCalls := len(paid.modelsSeen)
	paid.mu.Unlock()
	if freeCalls != 1 || paidCalls != 1 {
		t.Fatalf("free route should fail over without retry: free=%d paid=%d", freeCalls, paidCalls)
	}
	items, err := db.ListRequestStats(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].Provider != "surplus" || items[0].Attempts != 2 || len(items[0].AttemptDetails) != 2 || items[0].AttemptDetails[0].State != "failed" || items[0].AttemptDetails[1].Provider != "surplus" {
		t.Fatalf("expected durable failover metadata, got %#v, %v", items, err)
	}
}

func TestHumanErrorMessageExtractsNestedProviderDetail(t *testing.T) {
	err := &providers.UpstreamError{Provider: "openrouter", Message: `{"error":{"message":"Provider returned error","metadata":{"raw":"temporarily rate-limited upstream"}}}`}
	if got := humanErrorMessage(err); got != "temporarily rate-limited upstream" {
		t.Fatalf("got %q", got)
	}
	if got := rawErrorMessage(err); got == "temporarily rate-limited upstream" || !strings.Contains(got, `"metadata"`) {
		t.Fatalf("raw error was not preserved: %q", got)
	}
}

func TestParseRequestDetectsInputAndOutputModalities(t *testing.T) {
	request, err := parseRequest([]byte(`{"model":"model-a","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}},{"type":"input_audio","input_audio":{"data":"abc"}}]}],"modalities":["text"]}`), matcher.ProtocolChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(request.RequiredInputModalities, ",") != "text,image,audio" || strings.Join(request.RequiredOutputModalities, ",") != "text" {
		t.Fatalf("unexpected modalities: %#v", request)
	}
}

func TestProxySupportsResponsesAndAnthropicMessages(t *testing.T) {
	provider := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	for _, test := range []struct {
		protocol matcher.Protocol
		path     string
		body     string
	}{
		{matcher.ProtocolResponses, "/v1/responses", `{"model":"model-a","input":"hello"}`},
		{matcher.ProtocolAnthropic, "/v1/messages", `{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request, test.protocol)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: unexpected response %d %s", test.protocol, response.Code, response.Body.String())
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.protocols) != 2 || provider.protocols[0] != matcher.ProtocolResponses || provider.protocols[1] != matcher.ProtocolAnthropic {
		t.Fatalf("unexpected protocols: %#v", provider.protocols)
	}
}

func TestProxyRejectsInvalidClientKey(t *testing.T) {
	provider := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, _ := testProxy(t, provider)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`))
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d", response.Code)
	}
}

func TestProxyDoesNotFailOverAfterStreamBytes(t *testing.T) {
	first := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(&failingReader{data: []byte("data: {\"choices\":[]}\n\n")})}, nil
		},
	}}
	second := &fakeProvider{name: "openrouter", models: []providers.Model{model("model-a", 2, 2)}}
	proxy, db, secret := testProxy(t, first, second)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[],"stream":true}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "data:") {
		t.Fatalf("unexpected partial stream response: %d %s", response.Code, response.Body.String())
	}
	second.mu.Lock()
	defer second.mu.Unlock()
	if len(second.modelsSeen) != 0 {
		t.Fatalf("stream failure incorrectly failed over: %#v", second.modelsSeen)
	}
	var state string
	if err := db.DB().QueryRow(`SELECT state FROM proxy_requests`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "partial" {
		t.Fatalf("expected partial state, got %s", state)
	}
}
