package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/clientauth"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
)

type gatewayModelClient struct {
	name  string
	model providers.Model
}

func (c gatewayModelClient) Name() string { return c.name }
func (c gatewayModelClient) Discover(context.Context) ([]providers.Model, error) {
	return []providers.Model{c.model}, nil
}
func (c gatewayModelClient) Do(context.Context, matcher.Protocol, string, []byte, string) (*http.Response, error) {
	return nil, nil
}

func TestModelsEndpointReturnsOpenAIShapeWithoutCatalog(t *testing.T) {
	handler := NewHandler(nil, nil, clientauth.AllowAll)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"object":"list"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestModelsEndpointReturnsCanonicalModelSlugAndName(t *testing.T) {
	modelCatalog := catalog.New([]providers.Client{
		gatewayModelClient{name: "openrouter", model: providers.Model{ID: "meta/muse-spark-1.3-contributor"}},
		gatewayModelClient{name: "opencode", model: providers.Model{ID: "muse-spark-1.3-contributor-free"}},
	})
	if err := modelCatalog.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(modelCatalog, nil, clientauth.AllowAll)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "muse-spark-1.3-contributor" || body.Data[0].Name != "Muse Spark 1.3 Contributor" {
		t.Fatalf("unexpected canonical model response: %#v", body.Data)
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
