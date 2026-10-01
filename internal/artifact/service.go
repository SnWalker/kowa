package artifact

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Service struct {
	catalog Catalog
	blobs   BlobStore
	now     func() time.Time
}

func NewService(c Catalog, b BlobStore, now func() time.Time) *Service {
	return &Service{catalog: c, blobs: b, now: now}
}

type Upload struct {
	RequiresApproval                   bool
	SubjectID, Kind, MediaType, Digest string
	Producer                           Producer
}

func (s *Service) Upload(ctx context.Context, scope Scope, u Upload, data []byte) (Ref, error) {
	if len(data) > MaxSize {
		return Ref{}, ErrValidation
	}
	if Digest(data) != u.Digest {
		return Ref{}, ErrIntegrity
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Ref{}, fmt.Errorf("artifact id: %w", err)
	}
	id := hex.EncodeToString(nonce[:])
	ref := Ref{RequiresApproval: u.RequiresApproval, SchemaVersion: SchemaVersion, ArtifactID: id, WorkspaceID: scope.WorkspaceID, SubjectID: u.SubjectID, Kind: u.Kind, Producer: u.Producer, Digest: u.Digest, Size: int64(len(data)), MediaType: u.MediaType, StorageRef: "artifact:" + id}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	record := Record{Ref: ref, State: Staging, CreatedAt: s.now().UTC()}
	if err := s.catalog.WithArtifact(ctx, scope, id, &record, true, func(*Record) error { return nil }); err != nil {
		return Ref{}, err
	}
	// The staging row exists before any bytes: crashes cannot leave untracked objects.
	err := s.catalog.WithArtifact(ctx, scope, id, nil, true, func(r *Record) error {
		if r.State != Staging {
			return ErrUnavailable
		}
		if err := s.blobs.Put(ctx, id, data); err != nil {
			return fmt.Errorf("upload artifact: %w", err)
		}
		if _, err := s.verify(ctx, r.Ref); err != nil {
			return err
		}
		r.State = Uploaded
		return nil
	})
	if err != nil {
		return Ref{}, err
	}
	return ref, nil
}

func (s *Service) Publish(ctx context.Context, scope Scope, ref Ref) error {
	if err := checkScope(scope, ref); err != nil {
		return err
	}
	return s.catalog.WithArtifact(ctx, scope, ref.ArtifactID, nil, true, func(r *Record) error {
		if r.Ref != ref {
			return ErrIntegrity
		}
		if r.State != Uploaded && r.State != Published {
			return ErrUnavailable
		}
		if _, err := s.verify(ctx, ref); err != nil {
			return err
		}
		r.State = Published
		return nil
	})
}
func (s *Service) Read(ctx context.Context, scope Scope, ref Ref) ([]byte, error) {
	if err := checkScope(scope, ref); err != nil {
		return nil, err
	}
	var data []byte
	err := s.catalog.WithArtifact(ctx, scope, ref.ArtifactID, nil, false, func(r *Record) error {
		if r.Ref != ref {
			return ErrIntegrity
		}
		if r.State == Deleted || r.State == Deleting {
			return ErrUnavailable
		}
		if r.State != Published {
			return ErrUnpublished
		}
		var err error
		data, err = s.verify(ctx, ref)
		return err
	})
	return data, err
}
func (s *Service) Reference(ctx context.Context, scope Scope, ref Ref, owner Reference) error {
	if owner.Kind == "approval" {
		return ErrValidation
	}
	return s.reference(ctx, scope, ref, owner)
}

// Approve records an explicit control-plane confirmation for this exact immutable reference.
// HumanTask adapters must call this only after accepting the authenticated human response.
func (s *Service) Approve(ctx context.Context, scope Scope, ref Ref, responseID string) error {
	if !ValidID(responseID) {
		return ErrValidation
	}
	return s.reference(ctx, scope, ref, Reference{Kind: "approval", ID: scope.ActorID + ":" + responseID})
}
func (s *Service) reference(ctx context.Context, scope Scope, ref Ref, owner Reference) error {
	if err := checkScope(scope, ref); err != nil {
		return err
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	return s.catalog.WithArtifact(ctx, scope, ref.ArtifactID, nil, true, func(r *Record) error {
		if r.Ref != ref {
			return ErrIntegrity
		}
		if r.State == Deleting || r.State == Deleted {
			return ErrUnavailable
		}
		if r.State != Published {
			return ErrUnpublished
		}
		if _, err := s.verify(ctx, ref); err != nil {
			return err
		}
		if (owner.Kind == "run" || owner.Kind == "input") && !r.Approved() {
			return ErrUnapproved
		}
		for _, existing := range r.References {
			if existing == owner {
				return nil
			}
		}
		r.References = append(r.References, owner)
		return nil
	})
}

// Clean only removes orphans. There is intentionally no API for removing live references.
// DELETING is committed before external removal; a failed removal is safely retryable.
func (s *Service) Clean(ctx context.Context, scope Scope, id string, now time.Time, grace time.Duration) error {
	if grace <= 0 {
		return ErrValidation
	}
	err := s.catalog.WithArtifact(ctx, scope, id, nil, true, func(r *Record) error {
		if len(r.References) != 0 {
			return ErrReferenced
		}
		if r.State == Deleted || r.State == Deleting {
			return nil
		}
		if now.Before(r.CreatedAt.Add(grace)) {
			return ErrGracePeriod
		}
		r.State = Deleting
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.blobs.Remove(ctx, id); err != nil {
		return fmt.Errorf("remove orphan: %w", err)
	}
	return s.catalog.WithArtifact(ctx, scope, id, nil, true, func(r *Record) error {
		if r.State != Deleting && r.State != Deleted {
			return ErrConflict
		}
		r.State = Deleted
		return nil
	})
}
func (s *Service) verify(ctx context.Context, ref Ref) ([]byte, error) {
	data, err := s.blobs.Get(ctx, ref.ArtifactID)
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if int64(len(data)) != ref.Size || Digest(data) != ref.Digest {
		return nil, ErrIntegrity
	}
	return data, nil
}
func checkScope(scope Scope, ref Ref) error {
	if scope.WorkspaceID != ref.WorkspaceID {
		return ErrForbidden
	}
	return ref.Validate()
}
