package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

// Attempt phases stored in the journal.
const (
	PhaseExecuting = "executing"
	PhaseReporting = "reporting"
)

// AttemptRecord is the durable intent for one leased attempt. It is written
// before any worker starts, so a restart knows what was in flight without
// guessing from local process state.
type AttemptRecord struct {
	TaskID         string                `json:"taskId"`
	LeaseID        string                `json:"leaseId"`
	FencingToken   int64                 `json:"fencingToken"`
	RuntimeID      string                `json:"runtimeId"`
	RuntimeEpoch   string                `json:"runtimeEpoch"`
	WorkflowRunID  string                `json:"workflowRunId"`
	NodeRunID      string                `json:"nodeRunId"`
	AttemptKind    execution.AttemptKind `json:"attemptKind"`
	PreviousTaskID string                `json:"previousTaskId,omitempty"`
	Capability     execution.Capability  `json:"capability"`
	Phase          string                `json:"phase"`
	Workspace      string                `json:"workspace"`
	Marker         string                `json:"marker"`
	ResultDigest   string                `json:"resultDigest,omitempty"`
	StartedAt      time.Time             `json:"startedAt"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

// SessionRecord stores the provider session reference that may optimize a
// later resume attempt for the same node run. Sessions never carry identity:
// a missing or invalid session must always fall back to full input.
type SessionRecord struct {
	TaskID        string    `json:"taskId"`
	WorkflowRunID string    `json:"workflowRunId"`
	NodeRunID     string    `json:"nodeRunId"`
	ProviderID    string    `json:"providerId"`
	SessionRef    string    `json:"sessionRef"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Journal persists in-flight attempts and pending result payloads under the
// Runner work root. Every write is atomic (temp file + rename) with an fsync so
// a crash cannot leave a half-written record.
type Journal struct {
	stateDir    string
	attemptsDir string
	resultsDir  string
	sessionsDir string
}

func NewJournal(workRoot string) (*Journal, error) {
	if workRoot == "" {
		return nil, errors.New("runner: work root is required")
	}
	stateDir := filepath.Join(workRoot, "state")
	journal := &Journal{
		stateDir:    stateDir,
		attemptsDir: filepath.Join(stateDir, "attempts"),
		resultsDir:  filepath.Join(stateDir, "results"),
		sessionsDir: filepath.Join(stateDir, "sessions"),
	}
	for _, directory := range []string{journal.attemptsDir, journal.resultsDir, journal.sessionsDir} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return nil, fmt.Errorf("create journal directory: %w", err)
		}
	}
	return journal, nil
}

// Epoch returns the dispatch generation persisted for this Runner state. The
// epoch is generated once and survives restarts, so an attempt leased before a
// restart stays current and its journalled result can still be delivered.
// Rotating the state directory (or a declaration conflict) creates a new
// generation that supersedes the previous one.
func (j *Journal) Epoch() (string, error) {
	path := filepath.Join(j.stateDir, "runtime.json")
	encoded, err := os.ReadFile(path)
	if err == nil {
		var state struct {
			Epoch string `json:"epoch"`
		}
		if err := json.Unmarshal(encoded, &state); err != nil {
			return "", fmt.Errorf("decode runner state: %w", err)
		}
		if state.Epoch != "" {
			return state.Epoch, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read runner state: %w", err)
	}
	return j.RotateEpoch()
}

// RotateEpoch generates a fresh opaque generation and persists it.
func (j *Journal) RotateEpoch() (string, error) {
	suffix, err := randomHex(16)
	if err != nil {
		return "", err
	}
	epoch := "epoch-" + suffix
	encoded, err := json.MarshalIndent(struct {
		Epoch string `json:"epoch"`
	}{Epoch: epoch}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(j.stateDir, "runtime.json"), encoded); err != nil {
		return "", err
	}
	return epoch, nil
}

func (j *Journal) SaveAttempt(record AttemptRecord) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode attempt record: %w", err)
	}
	return writeAtomic(filepath.Join(j.attemptsDir, recordFileName(record.TaskID)), encoded)
}

// Attempts lists every non-terminal attempt record, oldest first.
func (j *Journal) Attempts() ([]AttemptRecord, error) {
	entries, err := os.ReadDir(j.attemptsDir)
	if err != nil {
		return nil, fmt.Errorf("read attempt journal: %w", err)
	}
	records := make([]AttemptRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(j.attemptsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read attempt record: %w", err)
		}
		var record AttemptRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			return nil, fmt.Errorf("decode attempt record %s: %w", entry.Name(), err)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		return records[left].StartedAt.Before(records[right].StartedAt)
	})
	return records, nil
}

func (j *Journal) ClearAttempt(taskID string) error {
	return removeIfExists(filepath.Join(j.attemptsDir, recordFileName(taskID)))
}

// SaveResult persists the exact result payload that will be reported. The
// payload bytes are reused verbatim for every retry, so the digest stays
// identical and the control plane treats re-delivery as idempotent.
func (j *Journal) SaveResult(taskID string, payload []byte) error {
	return writeAtomic(filepath.Join(j.resultsDir, recordFileName(taskID)), payload)
}

func (j *Journal) Result(taskID string) ([]byte, bool, error) {
	payload, err := os.ReadFile(filepath.Join(j.resultsDir, recordFileName(taskID)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read pending result: %w", err)
	}
	return payload, true, nil
}

func (j *Journal) ClearResult(taskID string) error {
	return removeIfExists(filepath.Join(j.resultsDir, recordFileName(taskID)))
}

// SaveSession records the newest provider session for one node run and prunes
// older sessions of the same node run.
func (j *Journal) SaveSession(record SessionRecord) error {
	entries, err := os.ReadDir(j.sessionsDir)
	if err != nil {
		return fmt.Errorf("read session journal: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(j.sessionsDir, entry.Name())
		encoded, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var existing SessionRecord
		if json.Unmarshal(encoded, &existing) != nil {
			continue
		}
		if existing.WorkflowRunID == record.WorkflowRunID && existing.NodeRunID == record.NodeRunID &&
			existing.TaskID != record.TaskID {
			_ = removeIfExists(path)
		}
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session record: %w", err)
	}
	return writeAtomic(filepath.Join(j.sessionsDir, recordFileName(record.TaskID)), encoded)
}

// Session reads the session recorded for a previous attempt of the same node.
func (j *Journal) Session(taskID string) (SessionRecord, bool, error) {
	encoded, err := os.ReadFile(filepath.Join(j.sessionsDir, recordFileName(taskID)))
	if errors.Is(err, os.ErrNotExist) {
		return SessionRecord{}, false, nil
	}
	if err != nil {
		return SessionRecord{}, false, fmt.Errorf("read session record: %w", err)
	}
	var record SessionRecord
	if err := json.Unmarshal(encoded, &record); err != nil {
		return SessionRecord{}, false, fmt.Errorf("decode session record: %w", err)
	}
	return record, true, nil
}

// ResultDigest computes the digest over a result content payload.
func ResultDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// recordFileName maps an opaque task id to a safe file name. Collisions after
// sanitizing are avoided by appending a short hash of the original value.
func recordFileName(taskID string) string {
	safe := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '-', character == '_', character == '.':
			return character
		default:
			return '_'
		}
	}, taskID)
	if safe != taskID || len(safe) > 120 {
		sum := sha256.Sum256([]byte(taskID))
		suffix := hex.EncodeToString(sum[:4])
		if len(safe) > 100 {
			safe = safe[:100]
		}
		safe = safe + "-" + suffix
	}
	return safe + ".json"
}

func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary journal file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write journal file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync journal file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close journal file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		cleanup()
		return fmt.Errorf("commit journal file: %w", err)
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
