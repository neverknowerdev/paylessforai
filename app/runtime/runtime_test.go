package runtime

import (
	"context"
	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	dbpkg "github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

func TestLoadProviderClientsPrefersOneStoredCredentialPerProvider(t *testing.T) {
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
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
	if err := db.ProviderCredentials.Upsert(context.Background(), models.ProviderCredential{
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
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
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
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
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
	if err := db.ProviderCredentials.Upsert(context.Background(), models.ProviderCredential{ID: ids.New(), Provider: "local-llm", Label: "local", BaseURL: "http://127.0.0.1:9999/v1", Ciphertext: ciphertext, Nonce: nonce, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	clients := loadProviderClients(providers.Builtin(nil), db, box)
	if len(clients) != 1 || clients[0].Name() != "local-llm" {
		t.Fatalf("expected custom provider client, got %#v", clients)
	}
}

func TestCredentialClientPreservesTranslationClient(t *testing.T) {
	underlying := providers.NewHTTPClient("opencode-go", "https://opencode.ai/zen/go/v1", "key")
	client := credentialClient{Client: underlying, id: "credential-1", account: "Main"}

	translated, ok := providers.Client(client).(providers.TranslationClient)
	if !ok {
		t.Fatal("credential client must preserve the translation-capable provider interface")
	}
	prepared, err := translated.Prepare(wire.FormatResponses, "muse-spark-1.3-contributor", []byte(`{"model":"old","input":"hello"}`))
	if err != nil {
		t.Fatalf("prepare through credential client: %v", err)
	}
	defer prepared.Body.Close()
	if prepared.Format != wire.FormatResponses || prepared.URL.Path != "/zen/go/v1/responses" {
		t.Fatalf("unexpected prepared request: %#v", prepared)
	}
}

func TestCatalogRefreshStartsWithoutCredentialsAndDiscoversLaterModels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var expanded atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if expanded.Load() {
			w.Write([]byte(`{"data":[{"id":"old"},{"id":"new"}]}`))
		} else {
			w.Write([]byte(`{"data":[{"id":"old"}]}`))
		}
	}))
	defer upstream.Close()
	manager := catalog.New(nil)
	startCatalogRefresh(ctx, manager, 10*time.Millisecond)
	manager.SetClients([]providers.Client{providers.NewHTTPClient("custom", upstream.URL, "key")})
	waitFor := func(count int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(manager.Snapshot().Models) == count {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("expected %d models, got %#v", count, manager.Snapshot())
	}
	waitFor(1)
	expanded.Store(true)
	waitFor(2)
	if got := manager.Snapshot().Additions; len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("new model not recorded: %#v", got)
	}
}
