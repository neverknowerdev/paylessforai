package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelsEndpointReturnsOpenAIShapeWithoutCatalog(t *testing.T) {
	handler := NewHandler(nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"object":"list"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestInferenceEndpointFailsClearlyWhenProxyIsNotConfigured(t *testing.T) {
	handler := NewHandler(nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "not_implemented") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}
