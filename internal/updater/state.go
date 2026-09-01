package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Phase string

const (
	PhaseIdle           Phase = "idle"
	PhaseChecking       Phase = "checking"
	PhaseAvailable      Phase = "available"
	PhaseDownloading    Phase = "downloading"
	PhaseVerified       Phase = "verified"
	PhasePreflighting   Phase = "preflighting"
	PhaseStaged         Phase = "staged"
	PhaseDraining       Phase = "draining"
	PhaseSnapshotting   Phase = "snapshotting"
	PhaseMigrating      Phase = "migrating"
	PhaseStarting       Phase = "starting"
	PhaseStabilizing    Phase = "stabilizing"
	PhasePromoted       Phase = "promoted"
	PhaseRollingBack    Phase = "rolling_back"
	PhaseRolledBack     Phase = "rolled_back"
	PhaseManualRecovery Phase = "needs_manual_recovery"
)

type State struct {
	OperationID           string `json:"operation_id,omitempty"`
	Phase                 Phase  `json:"phase"`
	CurrentPath           string `json:"current_path,omitempty"`
	CurrentVersion        string `json:"current_version,omitempty"`
	PreviousPath          string `json:"previous_path,omitempty"`
	PreviousVersion       string `json:"previous_version,omitempty"`
	CandidatePath         string `json:"candidate_path,omitempty"`
	CandidateVersion      string `json:"candidate_version,omitempty"`
	CandidateCommit       string `json:"candidate_commit,omitempty"`
	CandidateChannel      string `json:"candidate_channel,omitempty"`
	BackupPath            string `json:"backup_path,omitempty"`
	Error                 string `json:"error,omitempty"`
	FailedPhase           Phase  `json:"failed_phase,omitempty"`
	LastCheckAt           string `json:"last_check_at,omitempty"`
	LastSuccessAt         string `json:"last_success_at,omitempty"`
	WarningAcknowledgedAt string `json:"warning_acknowledged_at,omitempty"`
	QuarantinedVersion    string `json:"quarantined_version,omitempty"`
	UpdatedAt             string `json:"updated_at"`
}

type HistoryRecord struct {
	OperationID string `json:"operation_id"`
	Version     string `json:"version"`
	Commit      string `json:"commit,omitempty"`
	Channel     string `json:"channel"`
	Outcome     string `json:"outcome"`
	Phase       Phase  `json:"phase"`
	Error       string `json:"error,omitempty"`
	At          string `json:"at"`
}

type Journal struct {
	mu    sync.Mutex
	root  string
	state State
}

func OpenJournal(root string) (*Journal, error) {
	if root == "" {
		return nil, errors.New("updater root is required")
	}
	if err := os.MkdirAll(filepath.Join(root, "updater"), 0o700); err != nil {
		return nil, err
	}
	j := &Journal{root: filepath.Join(root, "updater"), state: State{Phase: PhaseIdle}}
	data, err := os.ReadFile(filepath.Join(j.root, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &j.state); err != nil {
		return nil, fmt.Errorf("read updater journal: %w", err)
	}
	if j.state.Phase == "" {
		j.state.Phase = PhaseIdle
	}
	return j, nil
}

func (j *Journal) Snapshot() State {
	j.mu.Lock()
	defer j.mu.Unlock()
	// The supervisor and application child share this journal. Refresh from the
	// atomically replaced state file so a long-lived child observes promotions
	// and rollbacks written by the supervisor after the child started.
	data, err := os.ReadFile(filepath.Join(j.root, "state.json"))
	if err == nil {
		var persisted State
		if json.Unmarshal(data, &persisted) == nil && persisted.Phase != "" {
			j.state = persisted
		}
	}
	return j.state
}

func (j *Journal) Transition(next State) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(j.root, "state.json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	file, err := os.OpenFile(tmp, os.O_WRONLY, 0)
	if err == nil {
		_ = file.Sync()
		_ = file.Close()
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(j.root, "state.json")); err != nil {
		return err
	}
	j.state = next
	return nil
}

func (j *Journal) AppendHistory(record HistoryRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(j.root, "history.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (j *Journal) History(limit int) ([]HistoryRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	data, err := os.ReadFile(filepath.Join(j.root, "history.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return []HistoryRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := splitLines(data)
	records := make([]HistoryRecord, 0, len(lines))
	for _, line := range lines {
		var record HistoryRecord
		if json.Unmarshal(line, &record) == nil {
			records = append(records, record)
		}
	}
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, value := range data {
		if value == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
