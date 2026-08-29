package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestSubscriptionRepositoryIntegrationUsesUsageAndCredentialModels(t *testing.T) {
	i := newIntegrationDB(t)
	fee := int64(20_000_000_000_000)
	if err := i.repos.ProviderCredentials.Upsert(i.ctx, models.ProviderCredential{
		ID: "subscription-credential", Provider: "subscription-provider", Label: "Pro plan",
		Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription",
		SubscriptionFeePicoUSD: &fee,
	}); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Create(i.ctx, "subscription-request", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.RecordAttemptRoute(i.ctx, "subscription-request", 1, "subscription-provider", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "subscription-request", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.RequestUsage.Upsert(i.ctx, models.RequestUsage{RequestID: "subscription-request", InputTokens: 4, OutputTokens: 6, TotalTokens: 10, RawUsageJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	rows, err := i.repos.Subscriptions.Usage(i.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Provider != "subscription-provider" || rows[0].InputTokens != 4 || rows[0].OutputTokens != 6 {
		t.Fatalf("unexpected subscription usage: %#v", rows)
	}
}
