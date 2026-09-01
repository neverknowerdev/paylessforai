package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/subscription"
)

func TestTableRepositoriesCRUD(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "repo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Settings.Set(ctx, "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := s.Settings.Get(ctx, "theme"); err != nil || !ok || value != "dark" {
		t.Fatalf("settings: %q %v %v", value, ok, err)
	}

	key, secret, err := s.ClientAPIKeys.Create(ctx, "repo-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClientAPIKeys.Authenticate(ctx, secret); err != nil || !ok {
		t.Fatalf("authenticate: %v %v", ok, err)
	}
	if keys, err := s.ClientAPIKeys.List(ctx); err != nil || len(keys) != 1 {
		t.Fatalf("list keys: %d %v", len(keys), err)
	}
	if err := s.ClientAPIKeys.Revoke(ctx, key.ID); err != nil {
		t.Fatal(err)
	}

	fee := int64(20_000_000_000_000)
	credential := ProviderCredential{ID: "credential-1", Provider: "repo-provider", Label: "Subscription", Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription", SubscriptionFeePicoUSD: &fee}
	if err := s.ProviderCredentials.Upsert(ctx, credential); err != nil {
		t.Fatal(err)
	}
	if credentials, err := s.ProviderCredentials.List(ctx); err != nil || len(credentials) != 1 || credentials[0].AccessMode != "subscription" {
		t.Fatalf("provider credentials: %+v %v", credentials, err)
	}
	next := time.Now().UTC().Add(time.Hour)
	if err := s.ProviderCredentials.MarkLimited(ctx, "repo-provider", &next, "quota"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProviderCredentials.ClearExpired(ctx, time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.CatalogRefreshes.Create(ctx, CatalogRefresh{ID: "refresh-1", Provider: "repo-provider", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Models.Upsert(ctx, ModelRecord{ID: "model-1", DisplayName: "Model", MetadataJSON: "{}", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if model, err := s.Models.Get(ctx, "model-1"); err != nil || model.DisplayName != "Model" {
		t.Fatalf("model: %+v %v", model, err)
	}
	if err := s.ModelRoutes.Upsert(ctx, ModelRouteRecord{ID: "route-1", ModelID: "model-1", Provider: "repo-provider", UpstreamModel: "model", Protocol: "chat.completions", PriceJSON: "{}", CapabilitiesJSON: "{}", Health: "healthy", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: true}); err != nil {
		t.Fatal(err)
	}
	if route, err := s.ModelRoutes.Get(ctx, "route-1"); err != nil || !route.Trusted {
		t.Fatalf("route: %+v %v", route, err)
	}
	if err := s.ModelRoutes.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Models.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ProviderHealth.Upsert(ctx, ProviderHealthRecord{RouteID: "route-1", State: "healthy", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if health, err := s.ProviderHealth.Get(ctx, "route-1"); err != nil || health.State != "healthy" {
		t.Fatalf("health: %+v %v", health, err)
	}
	if err := s.ProviderHealth.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}

	if err := s.ProxyRequests.Create(ctx, "request-1", key.ID, "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProxyAttempts.Record(ctx, "request-1", 1, "repo-provider", "model", "started", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ProxyAttempts.Record(ctx, "request-1", 1, "repo-provider", "model", "failed", "quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestUsage.Upsert(ctx, RequestUsage{RequestID: "request-1", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, RawUsageJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProxyRequests.Complete(ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProviderCredentials.Delete(ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionUsageRepositoryIncludesNullableCyclesAndMissingUsage(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "subscription.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	fee := int64(20_000_000_000_000)
	start := "2026-08-01T00:00:00Z"
	end := "2026-09-01T00:00:00Z"
	if err := s.ProviderCredentials.Upsert(ctx, ProviderCredential{
		ID: "subscription-credential", Provider: "subscription-provider", Label: "Pro plan",
		Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription",
		SubscriptionFeePicoUSD: &fee, SubscriptionCycleStart: &start, SubscriptionCycleEnd: &end,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProviderCredentials.Upsert(ctx, ProviderCredential{
		ID: "subscription-credential-without-cycle", Provider: "subscription-provider-without-cycle", Label: "Legacy plan",
		Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription",
		SubscriptionFeePicoUSD: &fee,
	}); err != nil {
		t.Fatal(err)
	}

	for _, request := range []struct {
		id       string
		provider string
	}{
		{id: "request-with-usage", provider: "subscription-provider"},
		{id: "request-without-usage", provider: "subscription-provider-without-cycle"},
	} {
		requestID := request.id
		if err := s.ProxyRequests.Create(ctx, requestID, "", "chat.completions", "model"); err != nil {
			t.Fatal(err)
		}
		if err := s.ProxyRequests.RecordAttemptRoute(ctx, requestID, 1, request.provider, "model"); err != nil {
			t.Fatal(err)
		}
		if err := s.ProxyRequests.Complete(ctx, requestID, "succeeded", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RequestUsage.Upsert(ctx, RequestUsage{RequestID: "request-with-usage", InputTokens: 12, OutputTokens: 8, TotalTokens: 20, RawUsageJSON: "{}"}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Subscriptions.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two usage rows, got %#v", rows)
	}
	byProvider := map[string]subscription.UsageRow{}
	for _, row := range rows {
		byProvider[row.Provider] = row
	}
	withUsage := byProvider["subscription-provider"]
	if withUsage.InputTokens != 12 || withUsage.OutputTokens != 8 || withUsage.CycleStart.IsZero() || withUsage.CycleEnd.IsZero() {
		t.Fatalf("unexpected populated subscription usage row: %#v", withUsage)
	}
	withoutUsage := byProvider["subscription-provider-without-cycle"]
	if withoutUsage.InputTokens != 0 || withoutUsage.OutputTokens != 0 || !withoutUsage.CycleStart.IsZero() || !withoutUsage.CycleEnd.IsZero() {
		t.Fatalf("unexpected nullable/coalesced subscription usage row: %#v", withoutUsage)
	}
}
