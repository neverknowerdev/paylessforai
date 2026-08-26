package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestClientKeyLifecycle(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, secret, err := s.CreateClientKey(context.Background(), "IDE")
	if err != nil || secret == "" {
		t.Fatalf("create: %#v %v", key, err)
	}
	if _, ok, err := s.AuthenticateClientKey(context.Background(), secret); err != nil || !ok {
		t.Fatalf("authenticate: ok=%v err=%v", ok, err)
	}
	if err := s.RevokeClientKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.AuthenticateClientKey(context.Background(), secret); err != nil || ok {
		t.Fatalf("revoked key authenticated: ok=%v err=%v", ok, err)
	}
}

func TestProviderCredentialLifecycle(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	credential := ProviderCredential{ID: "credential-1", Provider: "openrouter", Label: "main", BaseURL: "https://openrouter.example/v1", Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"), Enabled: true}
	if err := s.UpsertProviderCredential(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListProviderCredentials(context.Background())
	if err != nil || len(items) != 1 || string(items[0].Ciphertext) != "ciphertext" || items[0].BaseURL != credential.BaseURL {
		t.Fatalf("unexpected credentials: %#v %v", items, err)
	}
	if err := s.DeleteProviderCredential(context.Background(), credential.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListProviderCredentials(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("credentials were not deleted: %#v %v", items, err)
	}
}
