package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/providers"
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

	clients := loadProviderClients(providers.Builtin(map[string]string{"openrouter": "https://openrouter.example/v1"}), db, box)
	if len(clients) != 1 || clients[0].Name() != "openrouter" {
		t.Fatalf("expected one stored provider client, got %d", len(clients))
	}
}

func TestLoadProviderClientsDoesNotUseEnvironmentKeys(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	clients := loadProviderClients(providers.Builtin(nil), db, box)
	if len(clients) != 0 {
		t.Fatalf("expected no clients without persisted credentials, got %d", len(clients))
	}
}

func TestLoadProviderClientsSupportsCustomProviderEndpoint(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := box.Seal("local-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertProviderCredential(context.Background(), store.ProviderCredential{ID: ids.New(), Provider: "local-llm", Label: "local", BaseURL: "http://127.0.0.1:9999/v1", Ciphertext: ciphertext, Nonce: nonce, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	clients := loadProviderClients(providers.Builtin(nil), db, box)
	if len(clients) != 1 || clients[0].Name() != "local-llm" {
		t.Fatalf("expected custom provider client, got %#v", clients)
	}
}
