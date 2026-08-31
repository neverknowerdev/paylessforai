package updater

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
)

type SettingsStore interface {
	GetSetting(context.Context, string) (string, bool, error)
	SetSetting(context.Context, string, string) error
}

type Settings struct {
	Enabled         bool   `json:"enabled"`
	Channel         string `json:"channel"`
	IntervalSeconds int    `json:"interval_seconds"`
}
type Snapshot struct {
	Settings  Settings        `json:"settings"`
	Build     buildinfo.Info  `json:"build"`
	State     State           `json:"state"`
	Available *Manifest       `json:"available,omitempty"`
	History   []HistoryRecord `json:"history"`
}

type Service struct {
	dataDir           string
	store             SettingsStore
	journal           *Journal
	client            *http.Client
	baseURL           string
	onUpdateRequested func()
	allowUnsigned     bool
	mu                sync.Mutex
	available         *Manifest
	checking          bool
	installing        bool
	requested         bool
	stop              chan struct{}
}

func NewService(dataDir string, store SettingsStore, onUpdateRequested func()) (*Service, error) {
	journal, err := OpenJournal(dataDir)
	if err != nil {
		return nil, err
	}
	return &Service{dataDir: dataDir, store: store, journal: journal, client: &http.Client{Timeout: 30 * time.Second}, baseURL: "https://api.github.com/repos/neverknowerdev/paylessforai/releases", onUpdateRequested: onUpdateRequested, allowUnsigned: !buildinfo.Current().Official, stop: make(chan struct{})}, nil
}

func (s *Service) SetBaseURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(url) != "" {
		s.baseURL = strings.TrimRight(url, "/")
	}
}
func (s *Service) SetAllowUnsigned(value bool) { s.mu.Lock(); s.allowUnsigned = value; s.mu.Unlock() }

func (s *Service) LoadSettings(ctx context.Context) (Settings, error) {
	result := Settings{Enabled: true, Channel: "releases", IntervalSeconds: 3600}
	if value, ok, err := s.store.GetSetting(ctx, "updates.enabled"); err != nil {
		return result, err
	} else if ok {
		result.Enabled = strings.EqualFold(value, "true")
	}
	if value, ok, err := s.store.GetSetting(ctx, "updates.channel"); err != nil {
		return result, err
	} else if ok && (value == "main" || value == "releases") {
		result.Channel = value
	}
	if value, ok, err := s.store.GetSetting(ctx, "updates.check_interval_seconds"); err != nil {
		return result, err
	} else if ok {
		var parsed int
		_, _ = fmt.Sscanf(value, "%d", &parsed)
		if parsed >= 900 && parsed <= 604800 {
			result.IntervalSeconds = parsed
		}
	}
	return result, nil
}

func (s *Service) SaveSettings(ctx context.Context, settings Settings) error {
	if settings.Channel != "main" && settings.Channel != "releases" {
		return errors.New("channel must be main or releases")
	}
	if settings.IntervalSeconds < 900 || settings.IntervalSeconds > 604800 {
		return errors.New("check interval must be between 15 minutes and 7 days")
	}
	if err := s.store.SetSetting(ctx, "updates.enabled", fmt.Sprintf("%t", settings.Enabled)); err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, "updates.channel", settings.Channel); err != nil {
		return err
	}
	return s.store.SetSetting(ctx, "updates.check_interval_seconds", fmt.Sprintf("%d", settings.IntervalSeconds))
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	history, err := s.journal.History(50)
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	available := s.available
	s.mu.Unlock()
	return Snapshot{Settings: settings, Build: buildinfo.Current(), State: s.journal.Snapshot(), Available: available, History: history}, nil
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		if os.Getenv("PAYLESSFORAI_CANDIDATE") == "1" {
			return
		}
		settings, err := s.LoadSettings(ctx)
		if err != nil {
			return
		}
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				if settings.Enabled && buildinfo.Current().Official {
					_ = s.Check(ctx, true)
				}
				settings, _ = s.LoadSettings(ctx)
				timer.Reset(time.Duration(settings.IntervalSeconds) * time.Second)
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *Service) Close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
func (s *Service) IsUpdateRequested() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.requested }
func (s *Service) AcknowledgeWarning() error {
	state := s.journal.Snapshot()
	state.WarningAcknowledgedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.journal.Transition(state)
}

func (s *Service) Check(ctx context.Context, automatic bool) error {
	s.mu.Lock()
	if s.checking || s.installing {
		s.mu.Unlock()
		return errors.New("update operation is already running")
	}
	s.checking = true
	baseURL, allowUnsigned := s.baseURL, s.allowUnsigned
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.checking = false; s.mu.Unlock() }()
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if automatic && (!settings.Enabled || !buildinfo.Current().Official) {
		return nil
	}
	baseState := s.journal.Snapshot()
	baseState.Phase = PhaseChecking
	baseState.Error = ""
	baseState.FailedPhase = ""
	baseState.LastCheckAt = time.Now().UTC().Format(time.RFC3339Nano)
	_ = s.journal.Transition(baseState)
	manifest, signature, err := s.fetchManifest(ctx, baseURL, settings.Channel)
	if err != nil {
		baseState.Phase, baseState.Error, baseState.FailedPhase = PhaseIdle, err.Error(), PhaseChecking
		_ = s.journal.Transition(baseState)
		return err
	}
	if err := VerifyManifest(manifest, signature, allowUnsigned); err != nil {
		baseState.Phase, baseState.Error, baseState.FailedPhase = PhaseIdle, err.Error(), PhaseChecking
		_ = s.journal.Transition(baseState)
		return err
	}
	if !eligible(manifest, buildinfo.Current(), settings.Channel) {
		baseState.Phase, baseState.Error, baseState.FailedPhase = PhaseIdle, "", ""
		_ = s.journal.Transition(baseState)
		s.mu.Lock()
		s.available = nil
		s.mu.Unlock()
		return nil
	}
	s.mu.Lock()
	s.available = &manifest
	s.mu.Unlock()
	baseState.Phase, baseState.CandidateVersion, baseState.Error = PhaseAvailable, manifest.Version, ""
	_ = s.journal.Transition(baseState)
	if automatic {
		return s.Install(ctx, manifest.Version)
	}
	return nil
}

func eligible(manifest Manifest, current buildinfo.Info, channel string) bool {
	if manifest.Channel != channel || manifest.Version == current.Version {
		return false
	}
	if current.Version == "dev" || current.Version == "" {
		return true
	}
	if channel == "main" {
		return manifest.Commit != current.Commit
	}
	return compareVersions(strings.TrimPrefix(manifest.Version, "v"), strings.TrimPrefix(current.Version, "v")) > 0
}

func compareVersions(a, b string) int {
	var ai, bi [3]int
	for i, value := range strings.Split(a, ".") {
		if i >= 3 {
			break
		}
		var n int
		_, _ = fmt.Sscanf(value, "%d", &n)
		ai[i] = n
	}
	for i, value := range strings.Split(b, ".") {
		if i >= 3 {
			break
		}
		var n int
		_, _ = fmt.Sscanf(value, "%d", &n)
		bi[i] = n
	}
	for i := range ai {
		if ai[i] > bi[i] {
			return 1
		}
		if ai[i] < bi[i] {
			return -1
		}
	}
	return 0
}

func (s *Service) Install(ctx context.Context, version string) error {
	s.mu.Lock()
	if s.installing {
		s.mu.Unlock()
		return errors.New("update operation is already running")
	}
	manifest := s.available
	s.installing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.installing = false; s.mu.Unlock() }()
	if manifest == nil || (version != "" && manifest.Version != version) {
		return errors.New("update target is stale; check for updates again")
	}
	artifact, ok := manifest.ArtifactForCurrentPlatform()
	if !ok {
		return errors.New("no artifact for this platform")
	}
	response, err := s.client.Get(artifact.URL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: HTTP %d", response.StatusCode)
	}
	data, err := VerifyArtifact(response.Body, artifact.Size, artifact.SHA256)
	if err != nil {
		return err
	}
	id := randomID()
	root := filepath.Join(s.dataDir, "updater", "releases", id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	state := s.journal.Snapshot()
	state.OperationID, state.Phase, state.CandidateVersion, state.CandidatePath = id, PhaseDownloading, manifest.Version, root
	state.Error, state.FailedPhase = "", ""
	_ = s.journal.Transition(state)
	executable, err := ExtractArtifact(data, root)
	if err != nil {
		return err
	}
	if err := os.Chmod(executable, 0o700); err != nil {
		return err
	}
	state.Phase, state.CandidatePath = PhaseStaged, executable
	if err := s.journal.Transition(state); err != nil {
		return err
	}
	request := map[string]string{"operation_id": id, "candidate_path": executable, "candidate_version": manifest.Version, "channel": manifest.Channel, "commit": manifest.Commit}
	encoded, _ := json.Marshal(request)
	tmp := filepath.Join(s.dataDir, "updater", "request.json.tmp")
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dataDir, "updater", "request.json")); err != nil {
		return err
	}
	s.mu.Lock()
	s.requested = true
	s.mu.Unlock()
	if s.onUpdateRequested != nil {
		go s.onUpdateRequested()
	}
	return nil
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Prerelease  bool          `json:"prerelease"`
	Draft       bool          `json:"draft"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}
type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func (s *Service) fetchManifest(ctx context.Context, baseURL, channel string) (Manifest, []byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(req)
	if err != nil {
		return Manifest{}, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, nil, fmt.Errorf("check updates: HTTP %d", response.StatusCode)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&releases); err != nil {
		return Manifest{}, nil, err
	}
	for _, release := range releases {
		if release.Draft || (channel == "releases" && release.Prerelease) || (channel == "main" && (!release.Prerelease || !strings.HasPrefix(release.TagName, "main-"))) {
			continue
		}
		var manifestAsset, signatureAsset githubAsset
		for _, asset := range release.Assets {
			if asset.Name == "update-manifest.json" {
				manifestAsset = asset
			}
			if asset.Name == "update-manifest.json.sig" {
				signatureAsset = asset
			}
		}
		if manifestAsset.URL == "" {
			continue
		}
		manifestResponse, err := s.client.Get(manifestAsset.URL)
		if err != nil {
			return Manifest{}, nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(manifestResponse.Body, 1<<20))
		manifestResponse.Body.Close()
		if readErr != nil {
			return Manifest{}, nil, readErr
		}
		var manifest Manifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			return Manifest{}, nil, err
		}
		var signature []byte
		if signatureAsset.URL != "" {
			signatureResponse, err := s.client.Get(signatureAsset.URL)
			if err != nil {
				return Manifest{}, nil, err
			}
			sigBody, readErr := io.ReadAll(io.LimitReader(signatureResponse.Body, 1<<20))
			signatureResponse.Body.Close()
			if readErr != nil {
				return Manifest{}, nil, readErr
			}
			signature = decodeSignature(bytesTrimSpace(sigBody))
		}
		return manifest, signature, nil
	}
	return Manifest{}, nil, errors.New("no eligible update release found")
}

func bytesTrimSpace(value []byte) []byte { return []byte(strings.TrimSpace(string(value))) }
func decodeSignature(value []byte) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(string(value)); err == nil {
		return decoded
	}
	if decoded, err := hex.DecodeString(string(value)); err == nil {
		return decoded
	}
	return value
}
func randomID() string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data[:])
}
