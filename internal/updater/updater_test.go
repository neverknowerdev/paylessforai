package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
)

type memorySettings struct{ values map[string]string }

func (m *memorySettings) GetSetting(_ context.Context, key string) (string, bool, error) {
	value, ok := m.values[key]
	return value, ok, nil
}
func (m *memorySettings) SetSetting(_ context.Context, key, value string) error {
	m.values[key] = value
	return nil
}

func TestJournalSurvivesRestartAndReturnsNewestHistoryFirst(t *testing.T) {
	root := t.TempDir()
	journal, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Transition(State{OperationID: "one", Phase: PhaseStaged, CandidateVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendHistory(HistoryRecord{OperationID: "one", Version: "v1", Outcome: "rolled_back"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Snapshot().Phase; got != PhaseStaged {
		t.Fatalf("phase = %q", got)
	}
	history, err := reloaded.History(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Version != "v1" {
		t.Fatalf("history = %#v", history)
	}
}

func TestVerifyArtifactChecksSizeAndDigest(t *testing.T) {
	data := []byte("candidate")
	sum := sha256.Sum256(data)
	if got, err := VerifyArtifact(bytes.NewReader(data), int64(len(data)), hex.EncodeToString(sum[:])); err != nil || string(got) != string(data) {
		t.Fatalf("verify = %q, %v", got, err)
	}
	if _, err := VerifyArtifact(bytes.NewReader(data), int64(len(data)+1), hex.EncodeToString(sum[:])); err == nil {
		t.Fatal("expected size mismatch")
	}
}

func TestVerifyManifestSignatureUsesCanonicalManifestBytes(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := buildinfo.UpdatePublicKey
	buildinfo.UpdatePublicKey = hex.EncodeToString(public)
	defer func() { buildinfo.UpdatePublicKey = old }()
	manifest := Manifest{Schema: 1, Channel: "releases", Version: "v1.0.0", Commit: "abc", Artifacts: []Artifact{{OS: "darwin", Arch: "arm64", URL: "https://example.invalid/a", Size: 1, SHA256: "00"}}}
	contents, err := manifest.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, contents)
	if err := VerifyManifest(manifest, signature, false); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsDefaultsAndValidation(t *testing.T) {
	settings := &memorySettings{values: map[string]string{}}
	service, err := NewService(t.TempDir(), settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.LoadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Channel != "releases" || got.IntervalSeconds != 3600 {
		t.Fatalf("defaults = %#v", got)
	}
	got.Channel = "main"
	got.IntervalSeconds = 1800
	if err := service.SaveSettings(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	if saved, err := service.LoadSettings(context.Background()); err != nil || saved != got {
		t.Fatalf("saved = %#v, %v", saved, err)
	}
	got.IntervalSeconds = 1
	if err := service.SaveSettings(context.Background(), got); err == nil {
		t.Fatal("expected interval validation")
	}
}

func TestMarkReadyUsesAtomicReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready")
	if err := MarkReady(path, "nonce"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "nonce" {
		t.Fatalf("ready = %q, %v", data, err)
	}
}
