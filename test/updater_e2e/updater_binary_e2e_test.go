//go:build e2e && !windows

package updater_e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/updater"
)

type updateResponse struct {
	Build struct {
		Version string `json:"version"`
	} `json:"build"`
	State   updater.State           `json:"state"`
	History []updater.HistoryRecord `json:"history"`
}

type githubMock struct {
	server             *httptest.Server
	releases           []githubReleaseFixture
	releasesAfterFirst []githubReleaseFixture
	releaseChecks      atomic.Int32
}

type githubReleaseSpec struct {
	tagName         string
	publishedAt     string
	version         string
	commit          string
	artifact        []byte
	privateKey      ed25519.PrivateKey
	trailingNewline bool
}

type githubReleaseFixture struct {
	tagName      string
	publishedAt  string
	manifest     []byte
	signature    []byte
	artifact     []byte
	artifactName string
}

func newGitHubMock(t *testing.T, artifact []byte, version, commit string, private ed25519.PrivateKey) *githubMock {
	t.Helper()
	return newGitHubMockWithSpecs(t, []githubReleaseSpec{{
		tagName:     "v" + strings.TrimPrefix(version, "v"),
		publishedAt: "2026-09-01T00:00:00Z",
		version:     version,
		commit:      commit,
		artifact:    artifact,
		privateKey:  private,
	}})
}

func newGitHubMockWithSpecs(t *testing.T, specs []githubReleaseSpec) *githubMock {
	t.Helper()
	mock := &githubMock{}
	serverHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		var body []byte
		switch r.URL.Path {
		case "/releases":
			check := mock.releaseChecks.Add(1)
			releasesToServe := mock.releases
			if check > 1 && len(mock.releasesAfterFirst) > 0 {
				releasesToServe = mock.releasesAfterFirst
			}
			releases := make([]map[string]any, 0, len(releasesToServe))
			for index, release := range releasesToServe {
				prefix := fmt.Sprintf("%s/assets/%d", mock.server.URL, index)
				releases = append(releases, map[string]any{
					"tag_name": release.tagName, "prerelease": false, "draft": false, "published_at": release.publishedAt,
					"assets": []map[string]string{
						{"name": "update-manifest.json", "browser_download_url": prefix + "/manifest"},
						{"name": "update-manifest.json.sig", "browser_download_url": prefix + "/signature"},
						{"name": release.artifactName, "browser_download_url": prefix + "/artifact"},
					},
				})
			}
			var err error
			body, err = json.Marshal(releases)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		default:
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/assets/"), "/")
			if len(parts) != 2 {
				http.NotFound(w, r)
				return
			}
			index, err := strconv.Atoi(parts[0])
			if err != nil || index < 0 || index >= len(mock.releases) {
				http.NotFound(w, r)
				return
			}
			releasesToServe := mock.releases
			if mock.releaseChecks.Load() > 1 && len(mock.releasesAfterFirst) > 0 {
				releasesToServe = mock.releasesAfterFirst
			}
			if index >= len(releasesToServe) {
				http.NotFound(w, r)
				return
			}
			release := releasesToServe[index]
			switch parts[1] {
			case "manifest":
				body = release.manifest
			case "signature":
				body = release.signature
			case "artifact":
				body = release.artifact
			default:
				http.NotFound(w, r)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mock.server = httptest.NewUnstartedServer(serverHandler)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mock.server.Listener = listener
	mock.server.Start()
	for index, spec := range specs {
		mock.releases = append(mock.releases, makeGitHubReleaseFixture(t, mock, index, spec))
	}
	t.Cleanup(mock.server.Close)
	return mock
}

func makeGitHubReleaseFixture(t *testing.T, mock *githubMock, index int, spec githubReleaseSpec) githubReleaseFixture {
	t.Helper()
	artifactName := "paylessforai-app.tar.gz"
	manifest := updater.Manifest{Schema: 1, Channel: "releases", Version: spec.version, Commit: spec.commit, PublishedAt: spec.publishedAt, MinSupervisorProtocol: 1}
	manifest.SchemaCompatibility.Min = 1
	manifest.SchemaCompatibility.Max = 999999
	digest := sha256.Sum256(spec.artifact)
	manifest.Artifacts = []updater.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: fmt.Sprintf("%s/assets/%d/artifact", mock.server.URL, index), Size: int64(len(spec.artifact)), SHA256: hex.EncodeToString(digest[:]), Name: artifactName}}
	contents, err := manifest.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	published := contents
	if spec.trailingNewline {
		published = append(append([]byte(nil), contents...), '\n')
	}
	return githubReleaseFixture{tagName: spec.tagName, publishedAt: spec.publishedAt, manifest: published, signature: ed25519.Sign(spec.privateKey, published), artifact: spec.artifact, artifactName: artifactName}
}

func TestUpdaterBinaryE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the supervisor E2E harness uses Unix signals")
	}
	repoRoot := findRepoRoot(t)
	root := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicHex := hex.EncodeToString(public)
	base := buildBinary(t, repoRoot, root, "v0.1.0-e2e", "base-e2e", publicHex)
	successBinary := buildBinary(t, repoRoot, root, "v0.1.1-e2e", "success-e2e", publicHex)
	successArtifact := archiveBinary(t, successBinary)
	failingArtifact := archiveBytes(t, []byte("#!/bin/sh\nset -eu\ncase \"${PAYLESSFORAI_TEST_CANDIDATE_MODE:-}:$1\" in\n  preflight-fail:--internal-preflight) echo 'simulated migration failure' >&2; exit 42 ;;\n  startup-fail:--internal-serve) echo 'simulated startup failure' >&2; exit 43 ;;\nesac\nexit 0\n"))

	tests := []struct {
		name            string
		version         string
		commit          string
		artifact        []byte
		candidateMode   string
		wantPhase       updater.Phase
		wantBuild       string
		wantFailedPhase updater.Phase
		wantError       string
	}{
		{name: "promotes signed release", version: "v0.1.1-e2e", commit: "success-e2e", artifact: successArtifact, wantPhase: updater.PhasePromoted, wantBuild: "v0.1.1-e2e"},
		{name: "rolls back migration failure", version: "v0.1.2-e2e", commit: "migration-fail-e2e", artifact: failingArtifact, candidateMode: "preflight-fail", wantPhase: updater.PhaseRolledBack, wantFailedPhase: updater.PhasePreflighting, wantError: "exit status 42"},
		{name: "rolls back startup failure", version: "v0.1.3-e2e", commit: "startup-fail-e2e", artifact: failingArtifact, candidateMode: "startup-fail", wantPhase: updater.PhaseRolledBack, wantFailedPhase: updater.PhaseStarting, wantError: "exit status 43"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mock := newGitHubMock(t, test.artifact, test.version, test.commit, private)
			dataDir := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"))
			listenAddr := freeListenAddr(t)
			cmd := exec.Command(base, "--data-dir", dataDir, "--listen", listenAddr)
			cmd.Env = append(os.Environ(), "PAYLESSFORAI_UPDATE_BASE_URL="+mock.server.URL+"/releases")
			if test.candidateMode != "" {
				cmd.Env = append(cmd.Env, "PAYLESSFORAI_TEST_CANDIDATE_MODE="+test.candidateMode)
			}
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stopProcess(t, cmd) })
			waitHTTP(t, "http://"+listenAddr+"/readyz", 20*time.Second)
			result := waitForUpdate(t, "http://"+listenAddr+"/api/updates", func(payload updateResponse) bool {
				return payload.State.Phase == test.wantPhase && (test.wantBuild == "" || payload.Build.Version == test.wantBuild)
			}, 30*time.Second)
			if mock.releaseChecks.Load() == 0 {
				t.Fatal("updater did not query the mock GitHub Releases API")
			}
			if result.State.Phase != test.wantPhase {
				t.Fatalf("phase = %q, payload = %#v", result.State.Phase, result)
			}
			if test.wantPhase == updater.PhasePromoted {
				if result.State.CurrentVersion != test.wantBuild || result.State.PreviousPath == "" || len(result.History) != 1 || result.History[0].Commit != test.commit {
					t.Fatalf("promotion state/history = %#v", result)
				}
				return
			}
			if result.Build.Version != "v0.1.0-e2e" || result.State.FailedPhase != test.wantFailedPhase || !strings.Contains(result.State.Error, test.wantError) || result.State.QuarantinedVersion != test.version || len(result.History) != 1 {
				t.Fatalf("rollback state/history = %#v", result)
			}
			if result.History[0].Commit != test.commit || result.History[0].Channel != "releases" {
				t.Fatalf("rollback metadata = %#v", result.History[0])
			}
			// A restarted old child performs another automatic check, but the
			// quarantined candidate must not create another operation/history row.
			time.Sleep(2 * time.Second)
			after := waitForUpdate(t, "http://"+listenAddr+"/api/updates", func(payload updateResponse) bool {
				return payload.State.OperationID == result.State.OperationID && len(payload.History) == 1
			}, 5*time.Second)
			if after.State.CurrentPath == "" || databaseDigest(t, filepath.Join(dataDir, "paylessforai.db")) != databaseDigest(t, after.State.BackupPath) {
				t.Fatalf("rollback was not durable: state=%#v", after)
			}
		})
	}
}

func TestUpdaterBinaryE2EContinuesAutomaticUpdatesAfterPromotion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the supervisor E2E harness uses Unix signals")
	}
	repoRoot := findRepoRoot(t)
	root := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicHex := hex.EncodeToString(public)
	base := buildBinary(t, repoRoot, root, "v0.2.0-chain-e2e", "base-chain-e2e", publicHex)
	second := buildBinary(t, repoRoot, root, "v0.2.1-chain-e2e", "second-chain-e2e", publicHex)
	third := buildBinary(t, repoRoot, root, "v0.2.2-chain-e2e", "third-chain-e2e", publicHex)
	secondArtifact := archiveBinary(t, second)
	thirdArtifact := archiveBinary(t, third)
	mock := newGitHubMockWithSpecs(t, []githubReleaseSpec{{
		tagName:     "v0.2.1-chain-e2e",
		publishedAt: "2026-09-01T00:00:00Z",
		version:     "v0.2.1-chain-e2e",
		commit:      "second-chain-e2e",
		artifact:    secondArtifact,
		privateKey:  private,
	}})
	mock.releasesAfterFirst = []githubReleaseFixture{makeGitHubReleaseFixture(t, mock, 0, githubReleaseSpec{
		tagName:     "v0.2.2-chain-e2e",
		publishedAt: "2026-09-02T00:00:00Z",
		version:     "v0.2.2-chain-e2e",
		commit:      "third-chain-e2e",
		artifact:    thirdArtifact,
		privateKey:  private,
	})}

	dataDir := filepath.Join(root, "continues-automatic-updates")
	listenAddr := freeListenAddr(t)
	cmd := exec.Command(base, "--data-dir", dataDir, "--listen", listenAddr)
	cmd.Env = append(os.Environ(), "PAYLESSFORAI_UPDATE_BASE_URL="+mock.server.URL+"/releases")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(t, cmd) })
	waitHTTP(t, "http://"+listenAddr+"/readyz", 20*time.Second)
	result := waitForUpdate(t, "http://"+listenAddr+"/api/updates", func(payload updateResponse) bool {
		return payload.Build.Version == "v0.2.2-chain-e2e" && payload.State.CurrentVersion == "v0.2.2-chain-e2e" && len(payload.History) == 2
	}, 45*time.Second)
	if mock.releaseChecks.Load() < 2 {
		t.Fatalf("automatic scheduler did not perform a second release check: checks=%d", mock.releaseChecks.Load())
	}
	if result.State.Phase != updater.PhasePromoted || result.History[0].Version != "v0.2.2-chain-e2e" || result.History[1].Version != "v0.2.1-chain-e2e" {
		t.Fatalf("chained automatic promotion = state=%#v history=%#v", result.State, result.History)
	}
}

func TestUpdaterBinaryE2ESelectsNewestRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the supervisor E2E harness uses Unix signals")
	}
	repoRoot := findRepoRoot(t)
	root := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicHex := hex.EncodeToString(public)
	base := buildBinary(t, repoRoot, root, "v0.1.0-newest-e2e", "base-newest-e2e", publicHex)
	candidate := buildBinary(t, repoRoot, root, "v0.1.1-newest-e2e", "newest-e2e", publicHex)
	candidateArtifact := archiveBinary(t, candidate)
	mock := newGitHubMockWithSpecs(t, []githubReleaseSpec{
		{
			tagName:         "v9.9.9-old-e2e",
			publishedAt:     "2026-09-01T00:00:00Z",
			version:         "v9.9.9-old-e2e",
			commit:          "old-e2e",
			artifact:        candidateArtifact,
			privateKey:      private,
			trailingNewline: true,
		},
		{
			tagName:     "v0.1.1-newest-e2e",
			publishedAt: "2026-09-02T00:00:00Z",
			version:     "v0.1.1-newest-e2e",
			commit:      "newest-e2e",
			artifact:    candidateArtifact,
			privateKey:  private,
		},
	})
	dataDir := filepath.Join(root, "select-newest-release")
	listenAddr := freeListenAddr(t)
	cmd := exec.Command(base, "--data-dir", dataDir, "--listen", listenAddr)
	cmd.Env = append(os.Environ(), "PAYLESSFORAI_UPDATE_BASE_URL="+mock.server.URL+"/releases")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(t, cmd) })
	waitHTTP(t, "http://"+listenAddr+"/readyz", 20*time.Second)
	result := waitForUpdate(t, "http://"+listenAddr+"/api/updates", func(payload updateResponse) bool {
		return payload.State.Phase == updater.PhasePromoted
	}, 30*time.Second)
	if mock.releaseChecks.Load() == 0 {
		t.Fatal("updater did not query the mock GitHub Releases API")
	}
	if result.State.CurrentVersion != "v0.1.1-newest-e2e" || len(result.History) != 1 || result.History[0].Commit != "newest-e2e" {
		t.Fatalf("newest release was not promoted: state=%#v history=%#v", result.State, result.History)
	}
}

func buildBinary(t *testing.T, repoRoot, root, version, commit, publicKey string) string {
	t.Helper()
	output := filepath.Join(root, version)
	ldflags := fmt.Sprintf("-s -w -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Version=%s -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Channel=releases -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Commit=%s -X github.com/neverknowerdev/paylessforai/internal/buildinfo.BuiltAt=2026-09-01T00:00:00Z -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Official=true -X github.com/neverknowerdev/paylessforai/internal/buildinfo.UpdatePublicKey=%s", version, commit, publicKey)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", output, "./cmd/paylessforai-app")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOCACHE="+filepath.Join(root, "go-cache"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, output)
	}
	return output
}

func archiveBinary(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return archiveBytes(t, data)
}

func archiveBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "paylessforai-app", Mode: 0o700, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func waitForUpdate(t *testing.T, url string, predicate func(updateResponse) bool, timeout time.Duration) updateResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	var last updateResponse
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				var payload updateResponse
				if json.Unmarshal(body, &payload) == nil {
					last = payload
					if predicate(payload) {
						return payload
					}
				}
			}
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for updater state: last=%#v err=%v", last, lastErr)
	return last
}

func waitHTTP(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", url)
}

func databaseDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func stopProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func freeListenAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not find repository root")
		}
		directory = parent
	}
}
