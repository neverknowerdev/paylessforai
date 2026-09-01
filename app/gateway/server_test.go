package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/clientauth"
)

func TestModelsEndpointReturnsOpenAIShapeWithoutCatalog(t *testing.T) {
	handler := NewHandler(nil, nil, clientauth.AllowAll)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"object":"list"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestInferenceEndpointFailsClearlyWhenProxyIsNotConfigured(t *testing.T) {
	handler := NewHandler(nil, nil, clientauth.AllowAll)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "not_implemented") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestGatewayRejectsUnknownPathsBeforeAuthentication(t *testing.T) {
	handler := NewHandler(nil, nil, clientauth.Middleware(nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected unknown-path response: %d %s", response.Code, response.Body.String())
	}
}

func TestModelsEndpointRequiresClientKey(t *testing.T) {
	handler := NewHandler(nil, nil, clientauth.Middleware(nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "invalid_api_key") {
		t.Fatalf("unexpected unauthenticated response: %d %s", response.Code, response.Body.String())
	}
}
