package proxy

import (
	"context"
	"encoding/json"
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
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/session"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

type fakeProvider struct {
	name       string
	models     []providers.Model
	mu         sync.Mutex
	protocols  []matcher.Protocol
	modelsSeen []string
	sessionIDs []string
	responses  []func(*http.Request) (*http.Response, error)
}

type metadataProvider struct {
	*fakeProvider
	executionKey string
	account      string
	billing      matcher.BillingClass
}

type translatingProvider struct {
	*providers.HTTPClient
	models []providers.Model
	mu     sync.Mutex
	paths  []string
}

func (p *translatingProvider) Discover(context.Context) ([]providers.Model, error) {
	return p.models, nil
}

func (p *translatingProvider) DoPrepared(ctx context.Context, request providers.PreparedRequest) (*http.Response, error) {
	p.mu.Lock()
	p.paths = append(p.paths, request.URL.Path)
	p.mu.Unlock()
	return p.HTTPClient.DoPrepared(ctx, request)
}

func (p metadataProvider) ExecutionKey() string               { return p.executionKey }
func (p metadataProvider) CredentialID() string               { return p.executionKey }
func (p metadataProvider) AccountLabel() string               { return p.account }
func (p metadataProvider) BillingClass() matcher.BillingClass { return p.billing }

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

func (f *fakeProvider) Do(_ context.Context, protocol matcher.Protocol, model string, body []byte, sessionID string) (*http.Response, error) {
	f.mu.Lock()
	f.protocols = append(f.protocols, protocol)
	f.modelsSeen = append(f.modelsSeen, model)
	f.sessionIDs = append(f.sessionIDs, sessionID)
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

func TestProxyPropagatesAndRecordsDetectedSessionID(t *testing.T) {
	provider := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-PayLess-Chat-Id", "chat-123")
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.sessionIDs) != 1 || provider.sessionIDs[0] != "chat-123" {
		t.Fatalf("provider session metadata: %#v", provider.sessionIDs)
	}
	var sessionID string
	if err := db.DB().QueryRow(`SELECT session_id FROM proxy_requests`).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if sessionID != "chat-123" {
		t.Fatalf("recorded session id: %q", sessionID)
	}
}

func TestProxyHeaderlessHistoryReusesSessionAcrossRequests(t *testing.T) {
	provider := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	proxy.SessionDetector = session.NewDetector(db.Sessions, []byte("test-installation-key"))
	body := `{"model":"model-a","messages":[{"role":"user","content":"same conversation start"}]}`
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.sessionIDs) != 2 || provider.sessionIDs[0] == "" || provider.sessionIDs[0] != provider.sessionIDs[1] {
		t.Fatalf("headerless session IDs did not converge: %#v", provider.sessionIDs)
	}
	var distinct int
	if err := db.DB().QueryRow(`SELECT count(DISTINCT session_id) FROM proxy_requests`).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct != 1 {
		t.Fatalf("request recording split a reused history session: %d", distinct)
	}
}

func successResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func testProxy(t *testing.T, clients ...providers.Client) (*Proxy, *repositories.Repositories, string) {
	t.Helper()
	db, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := db.ClientAPIKeys.Create(context.Background(), "test")
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
	items, err := db.Stats.ListRequestStats(context.Background(), 10)
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
	items, err := db.Stats.ListRequestStats(context.Background(), 10)
	if err != nil || len(items) != 1 || len(items[0].AttemptDetails) != 3 {
		t.Fatalf("expected persisted attempts, got %#v, %v", items, err)
	}
	statuses := items[0].AttemptDetails
	if statuses[0].HTTPStatus == nil || *statuses[0].HTTPStatus != 500 || statuses[1].HTTPStatus == nil || *statuses[1].HTTPStatus != 500 || statuses[2].HTTPStatus == nil || *statuses[2].HTTPStatus != 200 {
		t.Fatalf("unexpected persisted upstream statuses: %#v", statuses)
	}
}

func TestProxyReturnsProviderErrorsAfterAllAttemptsFail(t *testing.T) {
	freeModel := model("model-a:free", 0, 0)
	freeModel.Free = true
	free := &fakeProvider{name: "opencode", models: []providers.Model{freeModel}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "opencode", StatusCode: http.StatusServiceUnavailable, Class: retry.ErrorServer, Message: "free capacity exhausted"}
		},
	}}
	paid := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "surplus", StatusCode: http.StatusBadGateway, Class: retry.ErrorServer, Message: "first paid attempt failed"}
		},
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "surplus", StatusCode: http.StatusBadGateway, Class: retry.ErrorServer, Message: "second paid attempt failed"}
		},
	}}
	proxy, db, secret := testProxy(t,
		metadataProvider{fakeProvider: free, executionKey: "opencode-free", account: "Free account", billing: matcher.BillingFree},
		metadataProvider{fakeProvider: paid, executionKey: "surplus-paid", account: "Primary account", billing: matcher.BillingMetered},
	)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Error struct {
			Type     string          `json:"type"`
			Code     string          `json:"code"`
			Message  string          `json:"message"`
			Attempts int             `json:"attempts"`
			Errors   []providerError `json:"errors"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Type != "payless_error" || payload.Error.Code != "all_provider_attempts_failed" || payload.Error.Message != "all provider attempts failed" {
		t.Fatalf("expected generic terminal error, got %#v", payload.Error)
	}
	if payload.Error.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", payload.Error.Attempts)
	}
	want := []providerError{
		{Provider: "opencode", Account: "Free account", Error: "free capacity exhausted"},
		{Provider: "surplus", Account: "Primary account", Error: "first paid attempt failed"},
		{Provider: "surplus", Account: "Primary account", Error: "second paid attempt failed"},
	}
	if len(payload.Error.Errors) != len(want) {
		t.Fatalf("provider errors: got %#v want %#v", payload.Error.Errors, want)
	}
	for i := range want {
		if payload.Error.Errors[i] != want[i] {
			t.Fatalf("provider error %d: got %#v want %#v", i, payload.Error.Errors[i], want[i])
		}
	}
	var code, message string
	if err := db.DB().QueryRow(`SELECT error_code, error_message FROM proxy_requests`).Scan(&code, &message); err != nil {
		t.Fatal(err)
	}
	if code != "upstream_error" || message != "all provider attempts failed" {
		t.Fatalf("persisted terminal error: code=%q message=%q", code, message)
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
	items, err := db.Stats.ListRequestStats(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].Provider != "surplus" || items[0].Attempts != 2 || len(items[0].AttemptDetails) != 2 || items[0].AttemptDetails[0].State != "failed" || items[0].AttemptDetails[1].Provider != "surplus" {
		t.Fatalf("expected durable failover metadata, got %#v, %v", items, err)
	}
}

func TestProxyUsesUnpricedSubscriptionBeforeMeteredRoutes(t *testing.T) {
	free := &fakeProvider{name: "opencode", models: []providers.Model{model("model-a:free", 0, 0)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "opencode", StatusCode: http.StatusServiceUnavailable, Class: retry.ErrorServer, Message: "free capacity exhausted"}
		},
	}}
	unpricedSubscriptionModel := model("model-a", 0, 0)
	unpricedSubscriptionModel.PriceAvailable = false
	unpricedSubscriptionModel.SupportedParameters = nil
	subscription := &fakeProvider{name: "opencode-go", models: []providers.Model{unpricedSubscriptionModel}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "opencode-go", StatusCode: http.StatusBadGateway, Class: retry.ErrorUnknown, Message: "subscription provider failed"}
		},
	}}
	openRouter := &fakeProvider{name: "openrouter", models: []providers.Model{model("meta/model-a", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "openrouter", StatusCode: http.StatusNotFound, Class: retry.ErrorModelNotFound, Message: "model not found"}
		},
	}}
	surplus := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 2, 2)}}

	proxy, db, secret := testProxy(t,
		metadataProvider{fakeProvider: free, executionKey: "free-credential", billing: matcher.BillingFree},
		metadataProvider{fakeProvider: subscription, executionKey: "subscription-credential", billing: matcher.BillingSubscription},
		openRouter,
		surplus,
	)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}

	rows, err := db.DB().Query(`SELECT provider FROM proxy_attempts ORDER BY attempt_number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var order []string
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			t.Fatal(err)
		}
		order = append(order, provider)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"opencode", "opencode-go", "openrouter", "surplus"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected provider execution order: got %v want %v", order, want)
	}
}

func TestProxyRoutesCanonicalMuseModelAcrossAllProviderAliases(t *testing.T) {
	free := &fakeProvider{name: "opencode", models: []providers.Model{model("muse-spark-1.3-contributor-free", 0, 0)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "opencode", StatusCode: http.StatusServiceUnavailable, Class: retry.ErrorServer, Message: "free capacity exhausted"}
		},
	}}
	goProvider := &fakeProvider{name: "opencode-go", models: []providers.Model{model("muse-spark-1.3-contributor", 1, 1)}, responses: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return nil, &providers.UpstreamError{Provider: "opencode-go", StatusCode: http.StatusNotFound, Class: retry.ErrorModelNotFound, Message: "model unavailable"}
		},
	}}
	openRouter := &fakeProvider{name: "openrouter", models: []providers.Model{model("meta/muse-spark-1.3-contributor", 2, 2)}}
	proxy, db, secret := testProxy(t, free, goProvider, openRouter)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"muse-spark-1.3-contributor","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	free.mu.Lock()
	freeSeen := append([]string(nil), free.modelsSeen...)
	free.mu.Unlock()
	goProvider.mu.Lock()
	goSeen := append([]string(nil), goProvider.modelsSeen...)
	goProvider.mu.Unlock()
	openRouter.mu.Lock()
	openRouterSeen := append([]string(nil), openRouter.modelsSeen...)
	openRouter.mu.Unlock()
	if len(freeSeen) != 1 || freeSeen[0] != "muse-spark-1.3-contributor-free" || len(goSeen) != 1 || goSeen[0] != "muse-spark-1.3-contributor" || len(openRouterSeen) != 1 || openRouterSeen[0] != "meta/muse-spark-1.3-contributor" {
		t.Fatalf("canonical request did not fail over through all aliases: free=%v go=%v openrouter=%v", freeSeen, goSeen, openRouterSeen)
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
	body := []byte(`{"model":"model-a","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}},{"type":"input_audio","input_audio":{"data":"abc"}}]}],"modalities":["text"]}`)
	canonical, err := wire.DecodeRequest(wire.FormatChatCompletions, body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := parseRequest(body, matcher.ProtocolChatCompletions, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(request.RequiredInputModalities, ",") != "text,image,audio" || strings.Join(request.RequiredOutputModalities, ",") != "text" {
		t.Fatalf("unexpected modalities: %#v", request)
	}
}

func TestParseRequestDetectsStructuredOutputInAllFormats(t *testing.T) {
	for _, test := range []struct {
		format   wire.Format
		protocol matcher.Protocol
		body     []byte
	}{
		{wire.FormatChatCompletions, matcher.ProtocolChatCompletions, []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"response_format":{"type":"json_object"}}`)},
		{wire.FormatResponses, matcher.ProtocolResponses, []byte(`{"model":"m","input":"hello","text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}}}`)},
		{wire.FormatAnthropicMessages, matcher.ProtocolAnthropic, []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}],"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`)},
	} {
		canonical, err := wire.DecodeRequest(test.format, test.body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := parseRequest(test.body, test.protocol, canonical)
		if err != nil {
			t.Fatal(err)
		}
		if !request.RequireStructured {
			t.Fatalf("structured output was not detected for %s", test.format)
		}
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

func TestProxyLearnsUpstreamFormatAndTranslatesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"wrong endpoint"}}`)
			return
		}
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp-1","model":"upstream","status":"completed","output_text":"translated","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"translated"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
	}))
	defer server.Close()
	provider := &translatingProvider{HTTPClient: providers.NewHTTPClient("translated", server.URL+"/v1", "secret"), models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"translated"`) {
		t.Fatalf("translated response: %d %s", response.Code, response.Body.String())
	}
	provider.mu.Lock()
	paths := append([]string(nil), provider.paths...)
	provider.mu.Unlock()
	if strings.Join(paths, ",") != "/v1/chat/completions,/v1/responses" {
		t.Fatalf("unexpected discovery paths: %v", paths)
	}
	var clientFormat, providerFormat string
	if err := db.DB().QueryRow(`SELECT client_format, provider_format FROM proxy_attempts WHERE attempt_number=2`).Scan(&clientFormat, &providerFormat); err != nil {
		t.Fatal(err)
	}
	if clientFormat != string(wire.FormatChatCompletions) || providerFormat != string(wire.FormatResponses) {
		t.Fatalf("unexpected attempt formats: %q -> %q", clientFormat, providerFormat)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[{"role":"user","content":"again"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response = httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	provider.mu.Lock()
	paths = append([]string(nil), provider.paths...)
	provider.mu.Unlock()
	if len(paths) != 3 || paths[2] != "/v1/responses" {
		t.Fatalf("learned format was not reused: %v", paths)
	}
}

func TestShouldTryNextFormatUsesEndpointAndServerStatusesOnlyForUnknownFormats(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
		http.StatusInternalServerError,
		http.StatusNotImplemented,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		err := &providers.UpstreamError{StatusCode: status}
		if !shouldTryNextFormat(err, true) {
			t.Errorf("status %d should be format-probeable when format is unknown", status)
		}
		if shouldTryNextFormat(err, false) {
			t.Errorf("status %d should not probe another format when format is known", status)
		}
	}
	for _, status := range []int{
		http.StatusMultipleChoices,
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusTooManyRequests,
	} {
		if shouldTryNextFormat(&providers.UpstreamError{StatusCode: status}, true) {
			t.Errorf("status %d must not trigger format probing", status)
		}
	}
	if shouldTryNextFormat(errors.New("transport failed"), true) {
		t.Error("transport failures must not trigger another format request")
	}
}

func TestProxyAggregatesTerminalErrorsFromTranslatedRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"model-a","name":"Model A","context_length":10000,"max_completion_tokens":1000,"pricing":{"prompt":"0.000001","completion":"0.000001"}}]}`)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"monthly usage quota exceeded","type":"mock_error"}}`)
	}))
	defer server.Close()

	provider := &translatingProvider{HTTPClient: providers.NewHTTPClient("subscription-mock", server.URL+"/v1", "secret"), models: []providers.Model{model("model-a", 1, 1)}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Error struct {
			Type     string          `json:"type"`
			Code     string          `json:"code"`
			Message  string          `json:"message"`
			Attempts int             `json:"attempts"`
			Errors   []providerError `json:"errors"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Type != "payless_error" || payload.Error.Code != "all_provider_attempts_failed" || payload.Error.Message != "all provider attempts failed" || payload.Error.Attempts != 1 {
		t.Fatalf("unexpected terminal error: %#v", payload.Error)
	}
	want := []providerError{{Provider: "subscription-mock", Error: "monthly usage quota exceeded"}}
	if len(payload.Error.Errors) != len(want) || payload.Error.Errors[0] != want[0] {
		t.Fatalf("unexpected provider errors: got %#v want %#v", payload.Error.Errors, want)
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
