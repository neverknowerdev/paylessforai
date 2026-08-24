package secrets

import (
	"path/filepath"
	"testing"
)

func TestBoxRoundTripAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	box, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := box.Seal("provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Open(ciphertext, nonce)
	if err != nil || plaintext != "provider-secret" {
		t.Fatalf("round trip: %q %v", plaintext, err)
	}
	reloaded, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := reloaded.Open(ciphertext, nonce); err != nil || plaintext != "provider-secret" {
		t.Fatalf("reload round trip: %q %v", plaintext, err)
	}
}
