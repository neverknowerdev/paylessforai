package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
)

type memorySettings struct{ values map[string]string }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type gatedReader struct {
	data    []byte
	offset  int
	started chan<- struct{}
	release <-chan struct{}
}

func (r *gatedReader) Read(buffer []byte) (int, error) {
	if r.offset == 0 {
		n := copy(buffer, r.data[:2])
		r.offset += n
		close(r.started)
		return n, nil
	}
	<-r.release
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(buffer, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (m *memorySettings) Get(_ context.Context, key string) (string, bool, error) {
	value, ok := m.values[key]
	return value, ok, nil
}
func (m *memorySettings) Set(_ context.Context, key, value string) error {
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

func TestJournalSnapshotRefreshesStateWrittenByAnotherProcess(t *testing.T) {
	root := t.TempDir()
	writer, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Transition(State{OperationID: "shared", Phase: PhasePromoted, CurrentVersion: "v2"}); err != nil {
		t.Fatal(err)
	}
	state := reader.Snapshot()
	if state.Phase != PhasePromoted || state.CurrentVersion != "v2" {
		t.Fatalf("state = %#v", state)
	}
}

func TestJournalLogsAreDurableOperationScopedAndChronological(t *testing.T) {
	root := t.TempDir()
	journal, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendLog("op-1", PhaseDownloading, "download started"); err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendLog("op-2", PhaseFailed, "other operation"); err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendLog("op-1", PhaseVerified, "artifact verified"); err != nil {
		t.Fatal(err)
	}
	logs, err := journal.Logs("op-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].Message != "download started" || logs[1].Message != "artifact verified" {
		t.Fatalf("logs = %#v", logs)
	}
	reloaded, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	logs, err = reloaded.Logs("op-2", 10)
	if err != nil || len(logs) != 1 || logs[0].OperationID != "op-2" {
		t.Fatalf("reloaded logs = %#v, err=%v", logs, err)
	}
}

func TestDownloadProgressReaderReportsBytes(t *testing.T) {
	var total int64
	reader := &downloadProgressReader{reader: bytes.NewReader([]byte("candidate artifact")), onRead: func(count int64) { total += count }}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "candidate artifact" || total != int64(len(data)) {
		t.Fatalf("data=%q total=%d", data, total)
	}
}

func TestInstallFailureIsPersistedWithLifecycleLog(t *testing.T) {
	settings := &memorySettings{values: map[string]string{}}
	service, err := NewService(t.TempDir(), settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.journal.Transition(State{Phase: PhaseAvailable, CurrentPath: "/active/paylessforai-app", CurrentVersion: "v1.0.0", LastCheckAt: "2026-09-07T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	err = service.failInstall(State{OperationID: "op-failure", CandidateVersion: "v9.9.9", CandidateCommit: "test", CandidateChannel: "releases", DownloadTotalBytes: 10}, PhaseDownloading, errors.New("download update: HTTP 502"))
	if err == nil {
		t.Fatal("expected failure")
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State.Phase != PhaseFailed || snapshot.State.FailedPhase != PhaseDownloading {
		t.Fatalf("state = %#v", snapshot.State)
	}
	if snapshot.State.DownloadTotalBytes != 10 || snapshot.State.DownloadBytes != 0 {
		t.Fatalf("download progress = %#v", snapshot.State)
	}
	if snapshot.State.CurrentPath != "/active/paylessforai-app" || snapshot.State.CurrentVersion != "v1.0.0" || snapshot.State.LastCheckAt != "2026-09-07T10:00:00Z" {
		t.Fatalf("active state was not preserved: %#v", snapshot.State)
	}
	if len(snapshot.History) != 1 || snapshot.History[0].Outcome != "failed" {
		t.Fatalf("history = %#v", snapshot.History)
	}
	if len(snapshot.Logs) < 1 || snapshot.Logs[0].Phase != PhaseFailed {
		t.Fatalf("logs = %#v", snapshot.Logs)
	}
}

func TestInstallPersistsLiveDownloadProgressBeforeReadingBody(t *testing.T) {
	data := []byte("mock update artifact")
	started := make(chan struct{})
	release := make(chan struct{})
	settings := &memorySettings{values: map[string]string{}}
	service, err := NewService(t.TempDir(), settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(&gatedReader{data: data, started: started, release: release}), Header: make(http.Header), Request: request}, nil
	})}
	service.available = &Manifest{Schema: 1, Channel: "releases", Version: "v9.9.10", Commit: "progress", Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "http://mock.invalid/artifact", Size: int64(len(data)), SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}}}
	result := make(chan error, 1)
	go func() { result <- service.Install(context.Background(), "v9.9.10") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download body was not read")
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State.Phase != PhaseDownloading || snapshot.State.DownloadBytes != 2 || snapshot.State.DownloadTotalBytes != int64(len(data)) || snapshot.State.OverallProgress <= 10 {
		t.Fatalf("live progress = %#v", snapshot.State)
	}
	close(release)
	if err := <-result; err == nil {
		t.Fatal("expected checksum failure")
	}
	snapshot, err = service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State.Phase != PhaseFailed || snapshot.State.DownloadBytes != int64(len(data)) || len(snapshot.Logs) < 2 {
		t.Fatalf("final state/logs = %#v / %#v", snapshot.State, snapshot.Logs)
	}
}

func TestDecodeSignaturePreservesRawWhitespaceBytes(t *testing.T) {
	raw := make([]byte, ed25519.SignatureSize)
	raw[0] = ' '
	raw[len(raw)-1] = '\t'
	decoded := decodeSignature(raw)
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("raw signature changed: got %x want %x", decoded, raw)
	}
}

func TestWaitReadyReportsExitedCandidateWithoutReapingTwice(t *testing.T) {
	done := make(chan error, 1)
	done <- errors.New("exit status 43")
	err, exited := waitReady(context.Background(), filepath.Join(t.TempDir(), "ready"), "token", done, time.Second)
	if !exited || err == nil || !strings.Contains(err.Error(), "exit status 43") {
		t.Fatalf("waitReady = %v, exited=%v", err, exited)
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
	manifest := Manifest{Schema: 1, Channel: "releases", Version: "v1.0.0", Commit: "abc", Artifacts: []Artifact{
		{OS: "darwin", Arch: "arm64", URL: "https://example.invalid/a", Size: 1, SHA256: "00"},
		{OS: "linux", Arch: "amd64", URL: "https://example.invalid/b", Size: 1, SHA256: "00"},
	}}
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
