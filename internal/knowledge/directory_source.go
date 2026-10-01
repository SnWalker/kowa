package knowledge

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/SnWalker/kowa/internal/artifact"
)

// DirectorySource reads a trusted, already materialized repository/commit for local tests.
// It does not resolve mutable refs or clone a repository. The materializer owns identity.
type DirectorySource struct {
	root                 *os.Root
	repositoryID, commit string
}

func NewDirectorySource(directory, repositoryID, commit string) (*DirectorySource, error) {
	if !repositoryPattern.MatchString(repositoryID) || !commitPattern.MatchString(commit) {
		return nil, ErrValidation
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &DirectorySource{root: root, repositoryID: repositoryID, commit: commit}, nil
}
func (s *DirectorySource) Close() error { return s.root.Close() }
func (s *DirectorySource) Probe(ctx context.Context, repositoryID, commit string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if repositoryID != s.repositoryID || commit != s.commit {
		return ErrUnavailable
	}
	_, err := s.root.Stat(".")
	return err
}
func (s *DirectorySource) Read(ctx context.Context, repositoryID, commit, path string) ([]byte, error) {
	if err := s.Probe(ctx, repositoryID, commit); err != nil {
		return nil, err
	}
	if !validPath(path) {
		return nil, ErrValidation
	}
	file, err := s.root.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() || info.Size() > artifact.MaxSize {
		return nil, errors.Join(ErrValidation, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, artifact.MaxSize+1))
	err = errors.Join(readErr, file.Close())
	if len(data) > artifact.MaxSize {
		return nil, ErrValidation
	}
	return data, err
}
