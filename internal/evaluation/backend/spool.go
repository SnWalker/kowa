package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// MemorySpool keeps records for the lifetime of the process. It exists for the
// in-memory adapters and for tests; it is never a substitute for the file spool
// when a restart must not lose a pending record.
type MemorySpool struct {
	mutex   sync.Mutex
	records map[string]Record
}

// NewMemorySpool constructs an empty in-memory spool.
func NewMemorySpool() *MemorySpool {
	return &MemorySpool{records: map[string]Record{}}
}

func (s *MemorySpool) Append(_ context.Context, record Record) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists := s.records[record.RecordID]; exists {
		return fmt.Errorf("%w: record %s already exists", ErrSpool, record.RecordID)
	}
	s.records[record.RecordID] = record
	return nil
}

func (s *MemorySpool) Update(_ context.Context, record Record) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists := s.records[record.RecordID]; !exists {
		return fmt.Errorf("%w: record %s does not exist", ErrSpool, record.RecordID)
	}
	s.records[record.RecordID] = record
	return nil
}

func (s *MemorySpool) List(_ context.Context) ([]Record, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	records := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sortRecords(records)
	return records, nil
}

// FileSpool persists one file per record under a directory. Every write is
// atomic (temporary file, fsync, rename), the same rule the Runner journal uses,
// so a crash cannot leave a half-written record.
type FileSpool struct{ directory string }

// NewFileSpool creates the spool directory when it is missing.
func NewFileSpool(directory string) (*FileSpool, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, fmt.Errorf("%w: a spool directory is required", ErrSpool)
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("%w: create spool directory: %v", ErrSpool, err)
	}
	return &FileSpool{directory: directory}, nil
}

// Directory reports where the spool writes, for the operations book.
func (s *FileSpool) Directory() string { return s.directory }

func (s *FileSpool) Append(_ context.Context, record Record) error {
	if _, err := os.Stat(s.path(record.RecordID)); err == nil {
		return fmt.Errorf("%w: record %s already exists", ErrSpool, record.RecordID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrSpool, err)
	}
	return s.write(record)
}

func (s *FileSpool) Update(_ context.Context, record Record) error {
	if _, err := os.Stat(s.path(record.RecordID)); err != nil {
		return fmt.Errorf("%w: record %s does not exist", ErrSpool, record.RecordID)
	}
	return s.write(record)
}

func (s *FileSpool) List(_ context.Context) ([]Record, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, fmt.Errorf("%w: read spool directory: %v", ErrSpool, err)
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(s.directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("%w: read spool record: %v", ErrSpool, err)
		}
		var record Record
		if err := json.Unmarshal(encoded, &record); err != nil {
			// A record that cannot be decoded is reported, never guessed at: the
			// spool is an observation store and must not invent its contents.
			return nil, fmt.Errorf("%w: decode spool record %s: %v", ErrSpool, entry.Name(), err)
		}
		records = append(records, record)
	}
	sortRecords(records)
	return records, nil
}

func (s *FileSpool) path(recordID string) string {
	return filepath.Join(s.directory, spoolFileName(recordID))
}

func (s *FileSpool) write(record Record) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode spool record: %v", ErrSpool, err)
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(s.directory, ".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: create temporary spool file: %v", ErrSpool, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(encoded); err != nil {
		cleanup()
		return fmt.Errorf("%w: write spool record: %v", ErrSpool, err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%w: sync spool record: %v", ErrSpool, err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return fmt.Errorf("%w: close spool record: %v", ErrSpool, err)
	}
	if err := os.Rename(temporaryPath, s.path(record.RecordID)); err != nil {
		cleanup()
		return fmt.Errorf("%w: commit spool record: %v", ErrSpool, err)
	}
	return nil
}

// spoolFileName maps an opaque record id to a safe file name, keeping the name
// readable when the id already is. A sanitized name that no longer identifies
// the record gets a short hash suffix, so two ids can never share one file.
func spoolFileName(recordID string) string {
	safe := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '-', character == '_', character == '.':
			return character
		default:
			return '_'
		}
	}, recordID)
	if safe != recordID || len(safe) > 120 {
		sum := sha256.Sum256([]byte(recordID))
		if len(safe) > 100 {
			safe = safe[:100]
		}
		safe = safe + "-" + hex.EncodeToString(sum[:4])
	}
	return safe + ".json"
}

func sortRecords(records []Record) {
	sort.Slice(records, func(left, right int) bool {
		if !records[left].CreatedAt.Equal(records[right].CreatedAt) {
			return records[left].CreatedAt.Before(records[right].CreatedAt)
		}
		return records[left].RecordID < records[right].RecordID
	})
}
