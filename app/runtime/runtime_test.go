package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/config"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/store"
)

func TestLoadProviderClientsPrefersOneStoredCredentialPerProvider(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := box.Seal("stored-openrouter-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertProviderCredential(context.Background(), store.ProviderCredential{
		ID: ids.New(), Provider: "openrouter", Label: "stored", Ciphertext: ciphertext, Nonce: nonce, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	clients := loadProviderClients(config.Config{
		OpenRouterAPIKey:  "environment-key",
		OpenRouterBaseURL: "https://openrouter.example/v1",
	}, db, box)
	if len(clients) != 1 || clients[0].Name() != "openrouter" {
		t.Fatalf("expected one stored provider client, got %d", len(clients))
	}
}
