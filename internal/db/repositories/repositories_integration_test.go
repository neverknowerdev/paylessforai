package repositories_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
)

const postgresSchema = `
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS provider_credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    label TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_checked_at TEXT,
    last_error TEXT,
    manual_models_json TEXT NOT NULL DEFAULT '[]',
    access_mode TEXT NOT NULL DEFAULT 'api',
    subscription_fee_pico_usd BIGINT,
    subscription_cycle_start TEXT,
    subscription_cycle_end TEXT,
    subscription_status TEXT NOT NULL DEFAULT 'available',
    next_available_at TEXT,
    status_reason TEXT
);
CREATE TABLE IF NOT EXISTS client_api_keys (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);
CREATE TABLE IF NOT EXISTS catalog_refreshes (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    error TEXT
);
CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    context_length BIGINT NOT NULL DEFAULT 0,
    max_output_tokens BIGINT NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    observed_at TEXT NOT NULL,
    stale_at TEXT
);
CREATE TABLE IF NOT EXISTS model_routes (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    protocol TEXT NOT NULL,
    price_json TEXT NOT NULL,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    health TEXT NOT NULL DEFAULT 'healthy',
    trusted INTEGER NOT NULL DEFAULT 0,
    observed_at TEXT NOT NULL,
    stale_at TEXT
);
CREATE TABLE IF NOT EXISTS provider_health (
    route_id TEXT PRIMARY KEY,
    failure_count BIGINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'healthy',
    backoff_until TEXT,
    last_error TEXT,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS proxy_requests (
    id TEXT PRIMARY KEY,
    client_key_id TEXT,
    protocol TEXT NOT NULL,
    logical_model TEXT NOT NULL,
    state TEXT NOT NULL,
    received_at TEXT NOT NULL,
    completed_at TEXT,
    selected_provider TEXT,
    selected_upstream_model TEXT,
    attempt_count BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT,
    error_code TEXT,
    error_message TEXT,
    stats_disposition TEXT NOT NULL DEFAULT 'included'
);
CREATE TABLE IF NOT EXISTS proxy_attempts (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL REFERENCES proxy_requests(id) ON DELETE CASCADE,
    attempt_number BIGINT NOT NULL,
    route_id TEXT,
    provider TEXT,
    upstream_model TEXT,
    state TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    http_status BIGINT,
    error_class TEXT,
    error_message TEXT,
    error_raw TEXT,
    duration_ms BIGINT,
    delivery_state TEXT NOT NULL DEFAULT 'nothing_sent',
    stats_disposition TEXT NOT NULL DEFAULT 'included'
);
CREATE TABLE IF NOT EXISTS request_usage (
    request_id TEXT PRIMARY KEY REFERENCES proxy_requests(id) ON DELETE CASCADE,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    cached_read_tokens BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    estimated_cost_pico_usd BIGINT NOT NULL DEFAULT 0,
    official_cost_pico_usd BIGINT NOT NULL DEFAULT 0,
    discount_pico_usd BIGINT,
    discount_percent_bps BIGINT,
    actual_cost_pico_usd BIGINT,
    raw_usage_json TEXT NOT NULL DEFAULT '{}'
);`

const postgresReset = `TRUNCATE request_usage, proxy_attempts, proxy_requests, provider_health, model_routes, models, catalog_refreshes, client_api_keys, provider_credentials, settings CASCADE`

func TestRepositoriesIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, postgresSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	repos := repositories.New(db.NewPostgresORM(database))

	reset := func(t *testing.T) {
		t.Helper()
		if _, err := database.ExecContext(ctx, postgresReset); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("settings Get and Set", func(t *testing.T) {
		reset(t)
		if value, ok, err := repos.Settings.Get(ctx, "theme"); err != nil || ok || value != "" {
			t.Fatalf("missing setting: %q, %v, %v", value, ok, err)
		}
		if err := repos.Settings.Set(ctx, "theme", "dark"); err != nil {
			t.Fatal(err)
		}
		if value, ok, err := repos.Settings.Get(ctx, "theme"); err != nil || !ok || value != "dark" {
			t.Fatalf("stored setting: %q, %v, %v", value, ok, err)
		}
	})

	t.Run("client API key CRUD", func(t *testing.T) {
		reset(t)
		key, secret, err := repos.ClientAPIKeys.Create(ctx, "integration")
		if err != nil {
			t.Fatal(err)
		}
		if authenticated, ok, err := repos.ClientAPIKeys.Authenticate(ctx, secret); err != nil || !ok || authenticated.ID != key.ID {
			t.Fatalf("authenticate: %+v, %v, %v", authenticated, ok, err)
		}
		keys, err := repos.ClientAPIKeys.List(ctx)
		if err != nil || len(keys) != 1 {
			t.Fatalf("list: %d, %v", len(keys), err)
		}
		if err := repos.ClientAPIKeys.Revoke(ctx, key.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := repos.ClientAPIKeys.Authenticate(ctx, secret); err != nil || ok {
			t.Fatalf("revoked key authenticated: %v, %v", ok, err)
		}
	})

	t.Run("provider credential lifecycle", func(t *testing.T) {
		reset(t)
		fee := int64(20_000_000_000_000)
		credential := models.ProviderCredential{ID: "credential-1", Provider: "provider", Label: "Subscription", Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription", SubscriptionFeePicoUSD: &fee}
		if err := repos.ProviderCredentials.Upsert(ctx, credential); err != nil {
			t.Fatal(err)
		}
		credentials, err := repos.ProviderCredentials.List(ctx)
		if err != nil || len(credentials) != 1 || credentials[0].AccessMode != "subscription" {
			t.Fatalf("list: %+v, %v", credentials, err)
		}
		next := time.Now().UTC().Add(time.Hour)
		if err := repos.ProviderCredentials.MarkLimited(ctx, "provider", &next, "quota"); err != nil {
			t.Fatal(err)
		}
		limited, err := repos.ProviderCredentials.List(ctx)
		if err != nil || len(limited) != 1 || limited[0].SubscriptionStatus != "limited" || limited[0].NextAvailableAt == nil {
			t.Fatalf("limited: %+v, %v", limited, err)
		}
		if err := repos.ProviderCredentials.ClearExpired(ctx, next.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		available, err := repos.ProviderCredentials.List(ctx)
		if err != nil || len(available) != 1 || available[0].SubscriptionStatus != "available" {
			t.Fatalf("available: %+v, %v", available, err)
		}
		if err := repos.ProviderCredentials.Delete(ctx, credential.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("catalog refresh create", func(t *testing.T) {
		reset(t)
		if err := repos.CatalogRefreshes.Create(ctx, models.CatalogRefresh{ID: "refresh-1", Provider: "provider", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_refreshes WHERE id='refresh-1'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("catalog row: %d, %v", count, err)
		}
	})

	t.Run("models CRUD", func(t *testing.T) {
		reset(t)
		model := models.ModelRecord{ID: "model-1", DisplayName: "Model", ContextLength: 1000, MaxOutputTokens: 500, MetadataJSON: "{}", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := repos.Models.Upsert(ctx, model); err != nil {
			t.Fatal(err)
		}
		if got, err := repos.Models.Get(ctx, model.ID); err != nil || got.DisplayName != model.DisplayName {
			t.Fatalf("get: %+v, %v", got, err)
		}
		if err := repos.Models.DeleteAll(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := repos.Models.Get(ctx, model.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("deleted model error: %v", err)
		}
	})

	t.Run("model routes CRUD", func(t *testing.T) {
		reset(t)
		if err := repos.Models.Upsert(ctx, models.ModelRecord{ID: "model-1", DisplayName: "Model", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
		route := models.ModelRouteRecord{ID: "route-1", ModelID: "model-1", Provider: "provider", UpstreamModel: "model", Protocol: "chat.completions", PriceJSON: "{}", CapabilitiesJSON: "{}", Health: "healthy", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: true}
		if err := repos.ModelRoutes.Upsert(ctx, route); err != nil {
			t.Fatal(err)
		}
		if got, err := repos.ModelRoutes.Get(ctx, route.ID); err != nil || !got.Trusted {
			t.Fatalf("get: %+v, %v", got, err)
		}
		if err := repos.ModelRoutes.DeleteAll(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("provider health CRUD", func(t *testing.T) {
		reset(t)
		health := models.ProviderHealthRecord{RouteID: "route-1", FailureCount: 2, State: "healthy", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := repos.ProviderHealth.Upsert(ctx, health); err != nil {
			t.Fatal(err)
		}
		if got, err := repos.ProviderHealth.Get(ctx, health.RouteID); err != nil || got.FailureCount != health.FailureCount {
			t.Fatalf("get: %+v, %v", got, err)
		}
		if err := repos.ProviderHealth.DeleteAll(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("proxy requests and attempts", func(t *testing.T) {
		reset(t)
		if err := repos.ProxyRequests.Create(ctx, "request-1", "", "chat.completions", "model"); err != nil {
			t.Fatal(err)
		}
		if err := repos.ProxyRequests.RecordAttemptRoute(ctx, "request-1", 1, "provider", "model"); err != nil {
			t.Fatal(err)
		}
		if err := repos.ProxyAttempts.Record(ctx, "request-1", 1, "provider", "model", "started", "", ""); err != nil {
			t.Fatal(err)
		}
		if err := repos.ProxyAttempts.Record(ctx, "request-1", 1, "provider", "model", "failed", "quota_exhausted", "quota"); err != nil {
			t.Fatal(err)
		}
		if err := repos.ProxyRequests.Complete(ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
			t.Fatal(err)
		}
		var provider, disposition, state string
		var attempts int
		if err := database.QueryRowContext(ctx, `SELECT selected_provider,stats_disposition,state,attempt_count FROM proxy_requests WHERE id='request-1'`).Scan(&provider, &disposition, &state, &attempts); err != nil {
			t.Fatal(err)
		}
		if provider != "provider" || disposition != "excluded_limit" || state != "failed" || attempts != 1 {
			t.Fatalf("request row: provider=%q disposition=%q state=%q attempts=%d", provider, disposition, state, attempts)
		}
	})

	t.Run("request usage upsert", func(t *testing.T) {
		reset(t)
		if err := repos.ProxyRequests.Create(ctx, "request-1", "", "chat.completions", "model"); err != nil {
			t.Fatal(err)
		}
		usage := models.RequestUsage{RequestID: "request-1", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, RawUsageJSON: "{}"}
		if err := repos.RequestUsage.Upsert(ctx, usage); err != nil {
			t.Fatal(err)
		}
		var total int64
		if err := database.QueryRowContext(ctx, `SELECT total_tokens FROM request_usage WHERE request_id='request-1'`).Scan(&total); err != nil || total != 15 {
			t.Fatalf("usage row: %d, %v", total, err)
		}
	})
}
