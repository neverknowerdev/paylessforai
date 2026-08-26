package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTableRepositoriesCRUD(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "repo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Repositories.Settings.Set(ctx, "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := s.Repositories.Settings.Get(ctx, "theme"); err != nil || !ok || value != "dark" {
		t.Fatalf("settings: %q %v %v", value, ok, err)
	}

	key, secret, err := s.Repositories.ClientAPIKeys.Create(ctx, "repo-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Repositories.ClientAPIKeys.Authenticate(ctx, secret); err != nil || !ok {
		t.Fatalf("authenticate: %v %v", ok, err)
	}
	if keys, err := s.Repositories.ClientAPIKeys.List(ctx); err != nil || len(keys) != 1 {
		t.Fatalf("list keys: %d %v", len(keys), err)
	}
	if err := s.Repositories.ClientAPIKeys.Revoke(ctx, key.ID); err != nil {
		t.Fatal(err)
	}

	fee := int64(20_000_000_000_000)
	credential := ProviderCredential{ID: "credential-1", Provider: "repo-provider", Label: "Subscription", Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription", SubscriptionFeePicoUSD: &fee}
	if err := s.Repositories.ProviderCredentials.Upsert(ctx, credential); err != nil {
		t.Fatal(err)
	}
	if credentials, err := s.Repositories.ProviderCredentials.List(ctx); err != nil || len(credentials) != 1 || credentials[0].AccessMode != "subscription" {
		t.Fatalf("provider credentials: %+v %v", credentials, err)
	}
	next := time.Now().UTC().Add(time.Hour)
	if err := s.Repositories.ProviderCredentials.MarkLimited(ctx, "repo-provider", &next, "quota"); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.ProviderCredentials.ClearExpired(ctx, time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.Repositories.CatalogRefreshes.Create(ctx, CatalogRefresh{ID: "refresh-1", Provider: "repo-provider", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.Models.Upsert(ctx, ModelRecord{ID: "model-1", DisplayName: "Model", MetadataJSON: "{}", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if model, err := s.Repositories.Models.Get(ctx, "model-1"); err != nil || model.DisplayName != "Model" {
		t.Fatalf("model: %+v %v", model, err)
	}
	if err := s.Repositories.ModelRoutes.Upsert(ctx, ModelRouteRecord{ID: "route-1", ModelID: "model-1", Provider: "repo-provider", UpstreamModel: "model", Protocol: "chat.completions", PriceJSON: "{}", CapabilitiesJSON: "{}", Health: "healthy", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: true}); err != nil {
		t.Fatal(err)
	}
	if route, err := s.Repositories.ModelRoutes.Get(ctx, "route-1"); err != nil || !route.Trusted {
		t.Fatalf("route: %+v %v", route, err)
	}
	if err := s.Repositories.ModelRoutes.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.Models.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.ProviderHealth.Upsert(ctx, ProviderHealthRecord{RouteID: "route-1", State: "healthy", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if health, err := s.Repositories.ProviderHealth.Get(ctx, "route-1"); err != nil || health.State != "healthy" {
		t.Fatalf("health: %+v %v", health, err)
	}
	if err := s.Repositories.ProviderHealth.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}

	if err := s.Repositories.ProxyRequests.Create(ctx, "request-1", key.ID, "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.ProxyAttempts.Record(ctx, "request-1", 1, "repo-provider", "model", "started", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.ProxyAttempts.Record(ctx, "request-1", 1, "repo-provider", "model", "failed", "quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.RequestUsage.Upsert(ctx, RequestUsage{RequestID: "request-1", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, RawUsageJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.ProxyRequests.Complete(ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	if err := s.Repositories.ProviderCredentials.Delete(ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
}
