package knowledge

import (
	"context"

	"github.com/SnWalker/kowa/internal/artifact"
)

type Store interface {
	ConfirmKnowledge(context.Context, artifact.Scope, ConfirmCommand) (BindingEvent, error)
	AssembleKnowledgeInput(context.Context, artifact.Scope, InputRequest, string) (EffectiveInput, error)
}
type ArtifactReader interface {
	Read(context.Context, artifact.Scope, artifact.Ref) ([]byte, error)
}
type Service struct {
	store     Store
	artifacts ArtifactReader
}

func NewService(store Store, reader ArtifactReader) *Service {
	return &Service{store: store, artifacts: reader}
}
func (s *Service) Confirm(ctx context.Context, scope artifact.Scope, c ConfirmCommand) (BindingEvent, error) {
	if scope.WorkspaceID != c.Snapshot.Content.WorkspaceID {
		return BindingEvent{}, artifact.ErrForbidden
	}
	if !artifact.ValidID(c.SubjectID) || !artifact.ValidID(c.HumanResponseID) || c.ExpectedVersion < 0 {
		return BindingEvent{}, ErrValidation
	}
	if err := ValidateSnapshot(c.Snapshot); err != nil {
		return BindingEvent{}, err
	}
	return s.store.ConfirmKnowledge(ctx, scope, c)
}
func (s *Service) Assemble(ctx context.Context, scope artifact.Scope, req InputRequest, eventID string) (EffectiveInput, error) {
	if scope.WorkspaceID != req.WorkspaceID {
		return EffectiveInput{}, artifact.ErrForbidden
	}
	if eventID == "" {
		return EffectiveInput{}, ErrUnconfirmed
	}
	for _, upstream := range req.Upstream {
		if _, err := s.artifacts.Read(ctx, scope, upstream.Ref); err != nil {
			return EffectiveInput{}, err
		}
	}
	// The transaction rechecks publication and creates every retention reference atomically.
	return s.store.AssembleKnowledgeInput(ctx, scope, req, eventID)
}
