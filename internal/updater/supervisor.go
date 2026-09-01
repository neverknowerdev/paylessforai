package updater

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
	_ "modernc.org/sqlite"
)

const UpdateRequestedExitCode = 75

type updateRequest struct {
	OperationID      string `json:"operation_id"`
	CandidatePath    string `json:"candidate_path"`
	CandidateVersion string `json:"candidate_version"`
	Channel          string `json:"channel"`
	Commit           string `json:"commit"`
}

// RunSupervisor keeps a stable process alive while replacing the application
// child. It is deliberately independent of the application database so it can
// restore that database when candidate startup fails.
func RunSupervisor(ctx context.Context, args []string) error {
	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	dataDir, err := dataDirFromArgs(args)
	if err != nil {
		return err
	}
	journal, err := OpenJournal(dataDir)
	if err != nil {
		return err
	}
	lock, err := acquireLock(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close(); _ = os.Remove(filepath.Join(dataDir, "updater", "lock")) }()
	if err := recoverInterrupted(dataDir, journal); err != nil {
		return err
	}
	currentPath := os.Args[0]
	if executable, err := os.Executable(); err == nil {
		currentPath = executable
	}
	state := journal.Snapshot()
	if state.CurrentPath != "" {
		currentPath = state.CurrentPath
	}
	if _, err := os.Stat(currentPath); err != nil {
		return fmt.Errorf("active binary unavailable: %w", err)
	}
	if state.Phase == PhaseStaged {
		if request, requestErr := readRequest(dataDir); requestErr == nil {
			if err := promoteCandidate(ctx, dataDir, journal, currentPath, args, request); err != nil && errors.Is(err, errManualRecovery) {
				return err
			}
			if recovered := journal.Snapshot(); recovered.CurrentPath != "" {
				currentPath = recovered.CurrentPath
			}
		}
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		state = journal.Snapshot()
		if state.CurrentPath == "" {
			if err := journal.Transition(State{Phase: PhaseIdle, CurrentPath: currentPath, CurrentVersion: buildinfo.Version}); err != nil {
				return err
			}
		}
		cmd := exec.CommandContext(ctx, currentPath, append([]string{"--internal-serve"}, args...)...)
		cmd.Env = childEnv("PAYLESSFORAI_SUPERVISED=1")
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start application: %w", err)
		}
		waitResult := make(chan struct {
			code int
			err  error
		}, 1)
		go func() {
			code, waitErr := waitChild(cmd)
			waitResult <- struct {
				code int
				err  error
			}{code, waitErr}
		}()
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		var result struct {
			code int
			err  error
		}
		select {
		case result = <-waitResult:
		case sig := <-signals:
			_ = cmd.Process.Signal(sig)
			result = <-waitResult
		}
		signal.Stop(signals)
		exitCode, waitErr := result.code, result.err
		if ctx.Err() != nil {
			return nil
		}
		if exitCode != UpdateRequestedExitCode {
			if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
				return waitErr
			}
			return nil
		}
		request, err := readRequest(dataDir)
		if err != nil {
			return err
		}
		if err := promoteCandidate(ctx, dataDir, journal, currentPath, args, request); err != nil {
			// promoteCandidate has already attempted fallback. Keep the service
			// running if fallback succeeded; only unrecoverable recovery errors exit.
			if errors.Is(err, errManualRecovery) {
				return err
			}
		}
		state = journal.Snapshot()
		if state.CurrentPath != "" {
			currentPath = state.CurrentPath
		}
	}
}

func acquireLock(dataDir string) (*os.File, error) {
	path := filepath.Join(dataDir, "updater", "lock")
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Sync()
			return file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var pid int
		if _, scanErr := fmt.Sscanf(string(data), "%d", &pid); scanErr != nil || pid <= 0 {
			_ = os.Remove(path)
			continue
		}
		if runtime.GOOS == "windows" {
			return nil, errors.New("another PayLessForAI instance is already running")
		}
		process, findErr := os.FindProcess(pid)
		if findErr == nil && process != nil {
			if signalErr := process.Signal(syscall.Signal(0)); signalErr == nil {
				return nil, errors.New("another PayLessForAI instance is already running")
			}
		}
		_ = os.Remove(path)
	}
	return nil, errors.New("unable to acquire updater lock")
}

func waitChild(cmd *exec.Cmd) (int, error) {
	err := cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), err
	}
	return -1, err
}

func dataDirFromArgs(args []string) (string, error) {
	for i, arg := range args {
		if arg == "-data-dir" || arg == "--data-dir" {
			if i+1 >= len(args) {
				return "", errors.New("data-dir value is required")
			}
			return args[i+1], nil
		}
		if len(arg) > 10 && (arg[:10] == "-data-dir=" || arg[:11] == "--data-dir=") {
			if arg[0:10] == "-data-dir=" {
				return arg[10:], nil
			}
			return arg[11:], nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, "paylessforai"), nil
}

var errManualRecovery = errors.New("updater needs manual recovery")

func MarkReady(path, token string) error {
	if path == "" || token == "" {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readRequest(dataDir string) (updateRequest, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "updater", "request.json"))
	if err != nil {
		return updateRequest{}, err
	}
	var request updateRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return updateRequest{}, err
	}
	if request.OperationID == "" || request.CandidatePath == "" {
		return updateRequest{}, errors.New("invalid update request")
	}
	return request, nil
}

func promoteCandidate(ctx context.Context, dataDir string, journal *Journal, previousPath string, args []string, request updateRequest) error {
	dbPath := filepath.Join(dataDir, "paylessforai.db")
	backupDir := filepath.Join(dataDir, "updater", "backups", request.OperationID)
	backupPath := filepath.Join(backupDir, "paylessforai.db")
	state := State{OperationID: request.OperationID, Phase: PhaseSnapshotting, FailedPhase: PhaseSnapshotting, CurrentPath: previousPath, CurrentVersion: buildinfo.Current().Version, PreviousPath: previousPath, CandidatePath: request.CandidatePath, CandidateVersion: request.CandidateVersion, CandidateCommit: request.Commit, CandidateChannel: request.Channel, BackupPath: backupPath}
	if err := journal.Transition(state); err != nil {
		return err
	}
	if err := copyDatabase(dbPath, backupPath); err != nil {
		return rollbackFailure(journal, state, fmt.Errorf("snapshot database: %w", err))
	}
	if err := verifyDatabase(backupPath); err != nil {
		return rollbackFailure(journal, state, fmt.Errorf("verify database snapshot: %w", err))
	}
	state.FailedPhase = PhasePreflighting
	if err := journal.Transition(State{OperationID: request.OperationID, Phase: PhasePreflighting, FailedPhase: PhasePreflighting, CurrentPath: previousPath, PreviousPath: previousPath, CandidatePath: request.CandidatePath, CandidateVersion: request.CandidateVersion, CandidateCommit: request.Commit, CandidateChannel: request.Channel, BackupPath: backupPath}); err != nil {
		return err
	}
	if err := preflightCandidate(ctx, dataDir, backupPath, request.CandidatePath, args); err != nil {
		return rollbackFailure(journal, state, fmt.Errorf("candidate preflight: %w", err))
	}
	state.FailedPhase = PhaseMigrating
	if err := journal.Transition(State{OperationID: request.OperationID, Phase: PhaseMigrating, FailedPhase: PhaseMigrating, CurrentPath: previousPath, PreviousPath: previousPath, CandidatePath: request.CandidatePath, CandidateVersion: request.CandidateVersion, CandidateCommit: request.Commit, CandidateChannel: request.Channel, BackupPath: backupPath}); err != nil {
		return err
	}
	token := randomID()
	readyPath := filepath.Join(dataDir, "updater", "ready-"+request.OperationID)
	gatePath := filepath.Join(dataDir, "updater", "gate-"+request.OperationID)
	_ = os.Remove(readyPath)
	_ = os.Remove(gatePath)
	candidateArgs := append([]string{"--internal-serve"}, args...)
	candidate := exec.CommandContext(ctx, request.CandidatePath, candidateArgs...)
	candidate.Env = childEnv("PAYLESSFORAI_SUPERVISED=1", "PAYLESSFORAI_CANDIDATE=1", "PAYLESSFORAI_READY_TOKEN="+token, "PAYLESSFORAI_READY_PATH="+readyPath, "PAYLESSFORAI_GATE_PATH="+gatePath)
	if err := candidate.Start(); err != nil {
		return rollbackCandidate(dataDir, journal, state, candidate, fmt.Errorf("start candidate: %w", err))
	}
	state.FailedPhase = PhaseStarting
	candidateWait := make(chan error, 1)
	go func() { candidateWait <- candidate.Wait() }()
	_ = journal.Transition(State{OperationID: request.OperationID, Phase: PhaseStarting, FailedPhase: PhaseStarting, CurrentPath: previousPath, PreviousPath: previousPath, CandidatePath: request.CandidatePath, CandidateVersion: request.CandidateVersion, CandidateCommit: request.Commit, CandidateChannel: request.Channel, BackupPath: backupPath})
	readyErr, candidateExited := waitReady(ctx, readyPath, token, candidateWait, 30*time.Second)
	if readyErr != nil {
		_ = candidate.Process.Kill()
		if !candidateExited {
			<-candidateWait
		}
		// waitReady may already have reaped an exited candidate. Avoid a second
		// Process.Wait in rollbackCandidate, which would otherwise deadlock.
		return rollbackCandidate(dataDir, journal, state, nil, readyErr)
	}
	state.FailedPhase = PhaseStabilizing
	_ = journal.Transition(State{OperationID: request.OperationID, Phase: PhaseStabilizing, FailedPhase: PhaseStabilizing, CurrentPath: previousPath, PreviousPath: previousPath, CandidatePath: request.CandidatePath, CandidateVersion: request.CandidateVersion, CandidateCommit: request.Commit, CandidateChannel: request.Channel, BackupPath: backupPath})
	wait := candidateWait
	select {
	case <-time.After(3 * time.Second):
	case err := <-wait:
		return rollbackCandidate(dataDir, journal, state, nil, fmt.Errorf("candidate exited during stabilization: %v", err))
	case <-ctx.Done():
		_ = candidate.Process.Kill()
		<-candidateWait
		return rollbackCandidate(dataDir, journal, state, nil, errors.New("update canceled"))
	}
	if err := journal.Transition(State{OperationID: request.OperationID, Phase: PhasePromoted, CurrentPath: request.CandidatePath, CurrentVersion: request.CandidateVersion, PreviousPath: previousPath, CandidatePath: request.CandidatePath, CandidateVersion: request.CandidateVersion, CandidateCommit: request.Commit, CandidateChannel: request.Channel, BackupPath: backupPath, LastSuccessAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	_ = journal.AppendHistory(HistoryRecord{OperationID: request.OperationID, Version: request.CandidateVersion, Commit: request.Commit, Channel: request.Channel, Outcome: "promoted", Phase: PhasePromoted, At: time.Now().UTC().Format(time.RFC3339Nano)})
	_ = MarkReady(gatePath, token)
	_ = os.Remove(filepath.Join(dataDir, "updater", "request.json"))
	// Keep supervising the already-running promoted child. Starting another
	// child here would race for the public listener.
	<-wait
	return nil
}

func childEnv(values ...string) []string {
	result := make([]string, 0, len(os.Environ())+len(values))
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "PAYLESSFORAI_GATE_PATH=") || strings.HasPrefix(item, "PAYLESSFORAI_READY_PATH=") || strings.HasPrefix(item, "PAYLESSFORAI_READY_TOKEN=") || strings.HasPrefix(item, "PAYLESSFORAI_CANDIDATE=") {
			continue
		}
		result = append(result, item)
	}
	return append(result, values...)
}

func waitReady(ctx context.Context, path, token string, candidateWait <-chan error, timeout time.Duration) (error, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil && string(data) == token {
				return nil, false
			}
		case err := <-candidateWait:
			if err == nil {
				return errors.New("candidate exited before readiness"), true
			}
			return fmt.Errorf("candidate exited before readiness: %w", err), true
		case <-deadline.C:
			return errors.New("candidate readiness timeout"), false
		case <-ctx.Done():
			return ctx.Err(), false
		}
	}
}

func preflightCandidate(ctx context.Context, dataDir, backupPath, candidatePath string, args []string) error {
	tmp, err := os.MkdirTemp(filepath.Join(dataDir, "updater"), "preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyDatabase(backupPath, filepath.Join(tmp, "paylessforai.db")); err != nil {
		return err
	}
	preflightArgs := replaceDataDir(args, tmp)
	command := exec.CommandContext(ctx, candidatePath, append([]string{"--internal-preflight"}, preflightArgs...)...)
	if err := command.Run(); err != nil {
		return err
	}
	return nil
}

func replaceDataDir(args []string, dataDir string) []string {
	result := make([]string, 0, len(args)+2)
	replaced := false
	for i := 0; i < len(args); i++ {
		if args[i] == "-data-dir" || args[i] == "--data-dir" {
			result = append(result, args[i], dataDir)
			i++
			replaced = true
			continue
		}
		if len(args[i]) > 10 && args[i][:10] == "-data-dir=" {
			result = append(result, "-data-dir="+dataDir)
			replaced = true
			continue
		}
		if len(args[i]) > 11 && args[i][:11] == "--data-dir=" {
			result = append(result, "--data-dir="+dataDir)
			replaced = true
			continue
		}
		result = append(result, args[i])
	}
	if !replaced {
		result = append(result, "-data-dir", dataDir)
	}
	return result
}

func rollbackCandidate(dataDir string, journal *Journal, state State, candidate *exec.Cmd, cause error) error {
	return rollbackFailureWithRestore(dataDir, journal, state, candidate, cause)
}

func rollbackFailure(journal *Journal, state State, cause error) error {
	state.Phase = PhaseRolledBack
	state.Error = cause.Error()
	if state.FailedPhase == "" {
		state.FailedPhase = PhaseSnapshotting
	}
	state.QuarantinedVersion = state.CandidateVersion
	_ = journal.Transition(state)
	_ = journal.AppendHistory(HistoryRecord{OperationID: state.OperationID, Version: state.CandidateVersion, Commit: state.CandidateCommit, Channel: state.CandidateChannel, Outcome: "failed", Phase: state.FailedPhase, Error: state.Error, At: time.Now().UTC().Format(time.RFC3339Nano)})
	return cause
}

func rollbackFailureWithRestore(dataDir string, journal *Journal, state State, candidate *exec.Cmd, cause error) error {
	failedPhase := state.FailedPhase
	if failedPhase == "" {
		failedPhase = PhaseStarting
	}
	_ = journal.Transition(State{OperationID: state.OperationID, Phase: PhaseRollingBack, FailedPhase: failedPhase, CurrentPath: state.PreviousPath, PreviousPath: state.PreviousPath, CandidatePath: state.CandidatePath, CandidateVersion: state.CandidateVersion, CandidateCommit: state.CandidateCommit, CandidateChannel: state.CandidateChannel, BackupPath: state.BackupPath, Error: cause.Error()})
	if candidate != nil && candidate.Process != nil {
		_ = candidate.Process.Kill()
		_, _ = candidate.Process.Wait()
	}
	if err := restoreDatabase(filepath.Join(dataDir, "paylessforai.db"), state.BackupPath); err != nil {
		_ = journal.Transition(State{OperationID: state.OperationID, Phase: PhaseManualRecovery, FailedPhase: PhaseRollingBack, CurrentPath: state.PreviousPath, PreviousPath: state.PreviousPath, CandidatePath: state.CandidatePath, CandidateVersion: state.CandidateVersion, CandidateCommit: state.CandidateCommit, CandidateChannel: state.CandidateChannel, BackupPath: state.BackupPath, Error: fmt.Sprintf("%v; restore failed: %v", cause, err)})
		return fmt.Errorf("%w: %v", errManualRecovery, err)
	}
	state.Phase = PhaseRolledBack
	state.Error = cause.Error()
	state.FailedPhase = failedPhase
	state.QuarantinedVersion = state.CandidateVersion
	_ = journal.Transition(state)
	_ = journal.AppendHistory(HistoryRecord{OperationID: state.OperationID, Version: state.CandidateVersion, Commit: state.CandidateCommit, Channel: state.CandidateChannel, Outcome: "rolled_back", Phase: state.FailedPhase, Error: state.Error, At: time.Now().UTC().Format(time.RFC3339Nano)})
	_ = os.Remove(filepath.Join(dataDir, "updater", "request.json"))
	return cause
}

func copyDatabase(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	tmp := destination + ".tmp"
	output, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, destination)
}

func restoreDatabase(destination, backup string) error {
	if _, err := os.Stat(backup); err != nil {
		return err
	}
	if err := copyDatabase(backup, destination+".restore"); err != nil {
		return err
	}
	if err := os.Rename(destination+".restore", destination); err != nil {
		return err
	}
	_ = os.Remove(destination + "-wal")
	_ = os.Remove(destination + "-shm")
	return verifyDatabase(destination)
}

func verifyDatabase(path string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer database.Close()
	var result string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("sqlite integrity check failed: " + result)
	}
	return nil
}

func recoverInterrupted(dataDir string, journal *Journal) error {
	state := journal.Snapshot()
	if state.Phase != PhaseSnapshotting && state.Phase != PhasePreflighting && state.Phase != PhaseMigrating && state.Phase != PhaseStarting && state.Phase != PhaseStabilizing && state.Phase != PhaseRollingBack {
		return nil
	}
	if state.BackupPath == "" || state.PreviousPath == "" {
		return journal.Transition(State{Phase: PhaseManualRecovery, Error: "incomplete updater journal", FailedPhase: state.Phase})
	}
	if err := restoreDatabase(filepath.Join(dataDir, "paylessforai.db"), state.BackupPath); err != nil {
		return fmt.Errorf("%w: %v", errManualRecovery, err)
	}
	return journal.Transition(State{OperationID: state.OperationID, Phase: PhaseRolledBack, CurrentPath: state.PreviousPath, PreviousPath: state.PreviousPath, CandidatePath: state.CandidatePath, CandidateVersion: state.CandidateVersion, BackupPath: state.BackupPath, Error: "recovered interrupted update", FailedPhase: state.Phase})
}
