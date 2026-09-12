package proxy

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

const structuredOutputIntegrationBody = `{"model":"model-a","messages":[{"role":"user","content":"hello"}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object"}}}}`

func unknownStructuredModel(id string, input, output int64) providers.Model {
	return providers.Model{ID: id, Format: wire.FormatChatCompletions, Pricing: matcher.Price{InputPicoUSDPerToken: input, OutputPicoUSDPerToken: output}, PriceAvailable: true}
}

func sendStructuredIntegrationRequest(t *testing.T, proxy *Proxy, secret string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(structuredOutputIntegrationBody))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	return response
}

func integrationProvider(t *testing.T, name string, handler http.HandlerFunc, model providers.Model) (*translatingProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := providers.NewHTTPClient(name, server.URL+"/v1", "provider-key")
	return &translatingProvider{HTTPClient: client, models: []providers.Model{model}}, server
}

func TestStructuredOutputIntegrationProbePersistAndExecute(t *testing.T) {
	calls := 0
	provider, server := integrationProvider(t, "structured-success", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"response_format"`) {
			t.Fatalf("structured request was not sent upstream: %s", body)
		}
		if calls == 1 {
			if !strings.Contains(string(body), "Return the requested object.") {
				t.Fatalf("first upstream call was not the probe: %s", body)
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"value\":\"ok\"}"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"inference","choices":[{"message":{"role":"assistant","content":"{\"answer\":\"done\"}"}}]}`)
	}), unknownStructuredModel("model-a", 1, 1))
	defer server.Close()
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()

	first := sendStructuredIntegrationRequest(t, proxy, secret)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `\"answer\":\"done\"`) {
		t.Fatalf("first structured request failed: %d %s", first.Code, first.Body.String())
	}
	supported, known, err := db.ModelRoutes.GetStructuredOutputCapability(context.Background(), "structured-success:model-a")
	if err != nil || !known || !supported {
		t.Fatalf("successful probe was not persisted: supported=%v known=%v err=%v", supported, known, err)
	}

	second := sendStructuredIntegrationRequest(t, proxy, secret)
	if second.Code != http.StatusOK || calls != 3 {
		t.Fatalf("second request did not reuse persisted capability: code=%d calls=%d body=%s", second.Code, calls, second.Body.String())
	}
}

func TestStructuredOutputIntegrationUnsupportedProbeReturnsErrorAndPersistsFalse(t *testing.T) {
	calls := 0
	provider, server := integrationProvider(t, "structured-unsupported", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"not json"}}]}`)
	}), unknownStructuredModel("model-a", 1, 1))
	defer server.Close()
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()

	first := sendStructuredIntegrationRequest(t, proxy, secret)
	if first.Code != http.StatusServiceUnavailable || !strings.Contains(first.Body.String(), "no_eligible_route") {
		t.Fatalf("unsupported probe did not return requester error: %d %s", first.Code, first.Body.String())
	}
	supported, known, err := db.ModelRoutes.GetStructuredOutputCapability(context.Background(), "structured-unsupported:model-a")
	if err != nil || !known || supported {
		t.Fatalf("unsupported probe result was not persisted: supported=%v known=%v err=%v", supported, known, err)
	}
	second := sendStructuredIntegrationRequest(t, proxy, secret)
	if second.Code != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("persisted negative result did not suppress a repeat probe: code=%d calls=%d", second.Code, calls)
	}
}

func TestStructuredOutputIntegrationTransientProbeErrorIsReturnedWithoutCapabilityUpdate(t *testing.T) {
	calls := 0
	provider, server := integrationProvider(t, "structured-transient", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid provider key"}`)
	}), unknownStructuredModel("model-a", 1, 1))
	defer server.Close()
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()

	response := sendStructuredIntegrationRequest(t, proxy, secret)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "no_eligible_route") {
		t.Fatalf("transient probe error was not returned to requester: %d %s", response.Code, response.Body.String())
	}
	if supported, known, err := db.ModelRoutes.GetStructuredOutputCapability(context.Background(), "structured-transient:model-a"); (err != nil && !errors.Is(err, sql.ErrNoRows)) || known || supported {
		t.Fatalf("transient probe error poisoned capability state: supported=%v known=%v err=%v", supported, known, err)
	}
	_ = sendStructuredIntegrationRequest(t, proxy, secret)
	if calls != 2 {
		t.Fatalf("transient probe was incorrectly cached: %d upstream calls", calls)
	}
}

func TestStructuredOutputIntegrationFallbackProbesOnlyWhenReached(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first, firstServer := integrationProvider(t, "a-first", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls++
		if firstCalls == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"value\":\"ok\"}"}}]}`)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"first route failed"}`)
	}), func() providers.Model {
		model := unknownStructuredModel("model-a", 1, 1)
		model.Free = true
		return model
	}())
	defer firstServer.Close()
	second, secondServer := integrationProvider(t, "b-second", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls++
		if secondCalls == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"value\":\"ok\"}"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"fallback","choices":[{"message":{"role":"assistant","content":"{\"answer\":\"fallback\"}"}}]}`)
	}), func() providers.Model {
		model := unknownStructuredModel("model-a", 2, 2)
		model.Free = true
		return model
	}())
	defer secondServer.Close()
	proxy, db, secret := testProxy(t, first, second)
	defer db.Close()

	response := sendStructuredIntegrationRequest(t, proxy, secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"fallback"`) {
		t.Fatalf("fallback structured request failed: %d %s", response.Code, response.Body.String())
	}
	if firstCalls != 2 || secondCalls != 2 {
		t.Fatalf("unexpected lazy fallback probing: first calls=%d second calls=%d", firstCalls, secondCalls)
	}
	for _, routeID := range []string{"a-first:model-a", "b-second:model-a"} {
		supported, known, err := db.ModelRoutes.GetStructuredOutputCapability(context.Background(), routeID)
		if err != nil || !known || !supported {
			t.Fatalf("fallback route probe was not persisted for %s: supported=%v known=%v err=%v", routeID, supported, known, err)
		}
	}
}
