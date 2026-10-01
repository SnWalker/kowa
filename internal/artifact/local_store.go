package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

var objectID = regexp.MustCompile(`^[0-9a-f]{48}$`)

// LocalStore is a development adapter; its root is private and independent of task directories.
// os.Root rejects symlink traversal outside the configured store.
type LocalStore struct{ root *os.Root }

func NewLocalStore(directory string) (*LocalStore, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &LocalStore{root: root}, nil
}
func (s *LocalStore) Close() error { return s.root.Close() }
func (s *LocalStore) Put(ctx context.Context, id string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !objectID.MatchString(id) || len(data) > MaxSize {
		return ErrValidation
	}
	f, err := s.root.OpenFile(id, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create immutable object: %w", err)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
func (s *LocalStore) Get(ctx context.Context, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !objectID.MatchString(id) {
		return nil, ErrValidation
	}
	f, err := s.root.Open(id)
	if err != nil {
		return nil, fmt.Errorf("open object: %w", errors.Join(ErrUnavailable, err))
	}
	info, statErr := f.Stat()
	if statErr != nil {
		return nil, errors.Join(statErr, f.Close())
	}
	if !info.Mode().IsRegular() || info.Size() > MaxSize {
		return nil, errors.Join(ErrValidation, f.Close())
	}
	b, readErr := io.ReadAll(io.LimitReader(f, MaxSize+1))
	err = errors.Join(readErr, f.Close())
	if len(b) > MaxSize {
		return nil, ErrValidation
	}
	return b, err
}
func (s *LocalStore) Remove(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !objectID.MatchString(id) {
		return ErrValidation
	}
	err := s.root.Remove(id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
