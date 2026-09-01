package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/remoteaccess"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
)

type credentialTestClient struct{ provider string }

func (c credentialTestClient) Name() string { return c.provider }
func (c credentialTestClient) Discover(context.Context) ([]providers.Model, error) {
	price := matcher.Price{InputPicoUSDPerToken: 1_000_000, OutputPicoUSDPerToken: 2_000_000}
	return []providers.Model{{ID: "model-a", Name: "Model A", Pricing: price, OfficialPricing: price, PriceAvailable: true, OfficialPriceAvailable: true}}, nil
}
func (c credentialTestClient) Do(context.Context, matcher.Protocol, string, []byte) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`)), Header: make(http.Header)}, nil
}

func recordAttempt(repos *repositories.Repositories, ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if err := repos.ProxyRequests.RecordAttemptRoute(ctx, requestID, attempt, provider, upstream); err != nil {
		return err
	}
	return repos.ProxyAttempts.Record(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, rawError...)
}

func testServer(t *testing.T) (*Server, func()) {
	t.Helper()
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
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
	_, secret, err := server.db.ClientAPIKeys.Create(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
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
		if strings.HasPrefix(test.path, "/v1/") {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
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
	_, secret, err := server.db.ClientAPIKeys.Create(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
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
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ProxyRequests.Create(context.Background(), "request-1", "", "chat_completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	group, err := db.Groups.Save(context.Background(), groups.Definition{ID: "stats-group", Name: "Stats group", Slug: "stats-group", Enabled: true, Stages: []groups.Stage{{Name: "primary", Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ProxyRequests.RecordResolution(context.Background(), "request-1", group.ID, group.Revision, "{}", "model-a"); err != nil {
		t.Fatal(err)
	}
	if err := recordAttempt(db, context.Background(), "request-1", 1, "surplus", "model-a", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.RequestUsage.Upsert(context.Background(), models.RequestUsage{RequestID: "request-1", TotalTokens: 5, EstimatedCostPico: 7}); err != nil {
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
	modelSummary := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(modelSummary, httptest.NewRequest(http.MethodGet, "/api/stats/models", nil))
	if modelSummary.Code != http.StatusOK || !strings.Contains(modelSummary.Body.String(), `"model":"model-a"`) {
		t.Fatalf("unexpected model stats response: %d %s", modelSummary.Code, modelSummary.Body.String())
	}
	providerSummary := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(providerSummary, httptest.NewRequest(http.MethodGet, "/api/stats/providers", nil))
	if providerSummary.Code != http.StatusOK || !strings.Contains(providerSummary.Body.String(), `"provider":"surplus"`) {
		t.Fatalf("unexpected provider stats response: %d %s", providerSummary.Code, providerSummary.Body.String())
	}
	groupSummary := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(groupSummary, httptest.NewRequest(http.MethodGet, "/api/stats/groups", nil))
	if groupSummary.Code != http.StatusOK || !strings.Contains(groupSummary.Body.String(), `"group":"Stats group"`) || !strings.Contains(groupSummary.Body.String(), `"slug":"stats-group"`) {
		t.Fatalf("unexpected group stats response: %d %s", groupSummary.Code, groupSummary.Body.String())
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
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry(
		providers.Definition{Name: "openrouter", DisplayName: "OpenRouter", DefaultBaseURL: "http://provider.invalid/v1", NewClient: func(string, string) providers.Client { return credentialTestClient{provider: "openrouter"} }},
		providers.Definition{Name: "local-llm", DisplayName: "Local LLM", DefaultBaseURL: "http://provider.invalid/v1", NewClient: func(string, string) providers.Client { return credentialTestClient{provider: "local-llm"} }},
	)
	server, err := NewWithDeps("127.0.0.1:0", time.Second, time.Second, db, nil, nil, CredentialDeps{Box: box, Registry: registry, Reload: func() error { return errors.New("partial provider catalog refresh: subscription-mock unavailable") }})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/providers/credentials", strings.NewReader(`{"provider":"openrouter","label":"main","api_key":"secret-value"}`)))
	if create.Code != http.StatusCreated || !strings.Contains(create.Body.String(), "catalog_refresh_warning") || strings.Contains(create.Body.String(), "secret-value") || strings.Contains(create.Body.String(), "ciphertext") {
		t.Fatalf("unexpected credential response: %d %s", create.Code, create.Body.String())
	}
	list := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/providers/credentials", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"provider":"openrouter"`) {
		t.Fatalf("unexpected credential list: %d %s", list.Code, list.Body.String())
	}
	custom := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(custom, httptest.NewRequest(http.MethodPost, "/api/providers/credentials", strings.NewReader(`{"provider":"local-llm","label":"local","base_url":"http://custom.invalid/v1","api_key":"different-secret"}`)))
	if custom.Code != http.StatusCreated || !strings.Contains(custom.Body.String(), `"provider":"local-llm"`) || !strings.Contains(custom.Body.String(), `"base_url":"http://custom.invalid/v1"`) {
		t.Fatalf("unexpected custom credential response: %d %s", custom.Code, custom.Body.String())
	}
	duplicate := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/api/providers/credentials", strings.NewReader(`{"provider":"openrouter","label":"duplicate","api_key":"secret-value"}`)))
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), `"code":"duplicate_provider_credential"`) {
		t.Fatalf("expected duplicate credential rejection: %d %s", duplicate.Code, duplicate.Body.String())
	}
}

type remoteControllerFake struct {
	status remoteaccess.Status
	config remoteaccess.Config
}

func (f *remoteControllerFake) Status(context.Context) remoteaccess.Status { return f.status }
func (f *remoteControllerFake) Configure(_ context.Context, config remoteaccess.Config) error {
	f.config = config
	f.status.DesiredMode = config.Mode
	f.status.Hostname = config.Hostname
	return nil
}
func (f *remoteControllerFake) Retry(context.Context) error          { return nil }
func (f *remoteControllerFake) Stop(context.Context) error           { return nil }
func (f *remoteControllerFake) ForgetIdentity(context.Context) error { return nil }

func TestRemoteAccessControlAPIIsRegisteredOnLoopback(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	fake := &remoteControllerFake{status: remoteaccess.Status{Phase: remoteaccess.PhaseDisabled, DesiredMode: remoteaccess.ModeDisabled, Hostname: "paylessforai"}}
	server.SetRemoteAccess(fake)
	get := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/remote-access", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"phase":"disabled"`) {
		t.Fatalf("status: %d %s", get.Code, get.Body.String())
	}
	put := httptest.NewRequest(http.MethodPut, "/api/remote-access", strings.NewReader(`{"mode":"private","hostname":"node"}`))
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, put)
	if response.Code != http.StatusAccepted || fake.config.Mode != remoteaccess.ModePrivate {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
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
