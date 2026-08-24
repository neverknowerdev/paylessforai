package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/store"
)

func testServer(t *testing.T) (*Server, func()) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New("127.0.0.1:0", time.Second, time.Second, db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return server, func() { db.Close() }
}

func TestServerHealthModelsAndNotImplementedEndpoints(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	for _, test := range []struct {
		path string
		code int
	}{
		{path: "/healthz", code: http.StatusOK},
		{path: "/readyz", code: http.StatusOK},
		{path: "/v1/models", code: http.StatusOK},
		{path: "/v1/chat/completions", code: http.StatusNotImplemented},
		{path: "/v1/responses", code: http.StatusNotImplemented},
		{path: "/v1/messages", code: http.StatusNotImplemented},
	} {
		method := http.MethodGet
		if test.path != "/healthz" && test.path != "/readyz" && test.path != "/v1/models" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, test.path, nil)
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, req)
		if response.Code != test.code {
			t.Fatalf("%s: got %d want %d", test.path, response.Code, test.code)
		}
		if response.Header().Get("X-PayLess-Request-ID") == "" {
			t.Fatalf("%s: missing request ID", test.path)
		}
	}
}

func TestServerModelsShape(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	server.httpServer.Handler.ServeHTTP(response, req)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["object"] != "list" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestRequestStatsAPI(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateProxyRequest(context.Background(), "request-1", "", "chat_completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordUsage(context.Background(), store.RequestUsage{RequestID: "request-1", TotalTokens: 5, EstimatedCostPico: 7}); err != nil {
		t.Fatal(err)
	}
	server, err := New("127.0.0.1:0", time.Second, time.Second, db)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/requests?limit=10", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total_tokens":5`) {
		t.Fatalf("unexpected request stats response: %d %s", response.Code, response.Body.String())
	}
	summary := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(summary, httptest.NewRequest(http.MethodGet, "/api/stats/summary", nil))
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"total_requests":1`) {
		t.Fatalf("unexpected stats summary response: %d %s", summary.Code, summary.Body.String())
	}
}

func TestServerUI(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !contains(response.Body.String(), "PayLessForAI") {
		t.Fatalf("unexpected UI response: %d %s", response.Code, response.Body.String())
	}
}

func TestClientKeyManagementAPI(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	create := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/client-keys", strings.NewReader(`{"label":"editor"}`)))
	if create.Code != http.StatusCreated || !strings.Contains(create.Body.String(), `"secret":"plai_`) {
		t.Fatalf("unexpected create response: %d %s", create.Code, create.Body.String())
	}
	list := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/client-keys", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"label":"editor"`) || strings.Contains(list.Body.String(), `"secret"`) {
		t.Fatalf("unexpected list response: %d %s", list.Code, list.Body.String())
	}
}

func TestProviderCredentialManagementAPI(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewWithDeps("127.0.0.1:0", time.Second, time.Second, db, nil, nil, CredentialDeps{Box: box, ProviderBases: map[string]string{"openrouter": "https://openrouter.invalid/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/providers/credentials", strings.NewReader(`{"provider":"openrouter","label":"main","api_key":"secret-value"}`)))
	if create.Code != http.StatusCreated || strings.Contains(create.Body.String(), "secret-value") || strings.Contains(create.Body.String(), "ciphertext") {
		t.Fatalf("unexpected credential response: %d %s", create.Code, create.Body.String())
	}
	list := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/providers/credentials", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"provider":"openrouter"`) {
		t.Fatalf("unexpected credential list: %d %s", list.Code, list.Body.String())
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
