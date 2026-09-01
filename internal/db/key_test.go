package db

import (
	"context"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"path/filepath"
	"testing"
)

func TestClientKeyLifecycle(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, secret, err := s.ClientAPIKeys.Create(context.Background(), "IDE")
	if err != nil || secret == "" {
		t.Fatalf("create: %#v %v", key, err)
	}
	if _, ok, err := s.ClientAPIKeys.Authenticate(context.Background(), secret); err != nil || !ok {
		t.Fatalf("authenticate: ok=%v err=%v", ok, err)
	}
	if err := s.ClientAPIKeys.Revoke(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClientAPIKeys.Authenticate(context.Background(), secret); err != nil || ok {
		t.Fatalf("revoked key authenticated: ok=%v err=%v", ok, err)
	}
}

func TestProviderCredentialLifecycle(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	credential := models.ProviderCredential{ID: "credential-1", Provider: "openrouter", Label: "main", BaseURL: "https://openrouter.example/v1", Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"), Enabled: true}
	if err := s.ProviderCredentials.Upsert(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	items, err := s.ProviderCredentials.List(context.Background())
	if err != nil || len(items) != 1 || string(items[0].Ciphertext) != "ciphertext" || items[0].BaseURL != credential.BaseURL {
		t.Fatalf("unexpected credentials: %#v %v", items, err)
	}
	if err := s.ProviderCredentials.Delete(context.Background(), credential.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.ProviderCredentials.List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("credentials were not deleted: %#v %v", items, err)
	}
}
