package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestProviderCredentialsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	fee := int64(20_000_000_000_000)
	credential := models.ProviderCredential{ID: "credential-1", Provider: "provider", Label: "Subscription", Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), Enabled: true, AccessMode: "subscription", SubscriptionFeePicoUSD: &fee}
	if err := i.repos.ProviderCredentials.Upsert(i.ctx, credential); err != nil {
		t.Fatal(err)
	}
	credentials, err := i.repos.ProviderCredentials.List(i.ctx)
	if err != nil || len(credentials) != 1 || credentials[0].AccessMode != "subscription" {
		t.Fatalf("list: %+v, %v", credentials, err)
	}
	next := time.Now().UTC().Add(time.Hour)
	if err := i.repos.ProviderCredentials.MarkLimited(i.ctx, "provider", &next, "quota"); err != nil {
		t.Fatal(err)
	}
	credentialNext := time.Now().UTC().Add(2 * time.Hour)
	if err := i.repos.ProviderCredentials.MarkLimitedByID(i.ctx, credential.ID, &credentialNext, "credential quota"); err != nil {
		t.Fatal(err)
	}
	limited, err := i.repos.ProviderCredentials.List(i.ctx)
	if err != nil || len(limited) != 1 || limited[0].SubscriptionStatus != "limited" || limited[0].NextAvailableAt == nil || limited[0].StatusReason == nil || *limited[0].StatusReason != "credential quota" || limited[0].LastError == nil || *limited[0].LastError != "credential quota" {
		t.Fatalf("limited: %+v, %v", limited, err)
	}
	if err := i.repos.ProviderCredentials.ClearExpired(i.ctx, credentialNext.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	available, err := i.repos.ProviderCredentials.List(i.ctx)
	if err != nil || len(available) != 1 || available[0].SubscriptionStatus != "available" {
		t.Fatalf("available: %+v, %v", available, err)
	}
	if err := i.repos.ProviderCredentials.Delete(i.ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
}
