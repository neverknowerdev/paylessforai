package mockprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScenarioControlsCatalogInferenceAndRequests(t *testing.T) {
	server := New(Scenario{Models: []Model{{ID: "model-a", Name: "Model A", PromptPrice: "0.000001", CompletionPrice: "0.000002"}}, ResponseText: "hello", InputTokens: 3, OutputTokens: 2})
	models := httptest.NewRecorder()
	server.ServeHTTP(models, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if models.Code != http.StatusOK || !strings.Contains(models.Body.String(), "model-a") {
		t.Fatalf("unexpected models: %d %s", models.Code, models.Body.String())
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "hello") {
		t.Fatalf("unexpected inference: %d %s", response.Code, response.Body.String())
	}
	requests := httptest.NewRecorder()
	server.ServeHTTP(requests, httptest.NewRequest(http.MethodGet, "/__mock/requests", nil))
	if !strings.Contains(requests.Body.String(), "/v1/chat/completions") {
		t.Fatalf("request was not recorded: %s", requests.Body.String())
	}
}

func TestScenarioFailureCountIsBounded(t *testing.T) {
	server := New(Scenario{FailureCount: 1, FailureStatus: http.StatusBadGateway})
	first := httptest.NewRecorder()
	server.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`)))
	second := httptest.NewRecorder()
	server.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`)))
	if first.Code != http.StatusBadGateway || second.Code != http.StatusOK {
		t.Fatalf("unexpected failure sequence: %d then %d", first.Code, second.Code)
	}
}

func TestScenarioOnlyStreamsWhenRequested(t *testing.T) {
	server := New(Scenario{ResponseText: "hello"})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","messages":[]}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("non-stream request returned content type %q", got)
	}
}
