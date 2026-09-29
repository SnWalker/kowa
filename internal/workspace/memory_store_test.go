package workspace

import (
	"context"
	"errors"
	"sync"
	"time"
)

type idempotencyResult struct {
	digest    string
	workspace Workspace
}

type memoryStore struct {
	mu          sync.Mutex
	workspaces  map[string]Workspace
	idempotency map[string]idempotencyResult
	auditErr    error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		workspaces:  make(map[string]Workspace),
		idempotency: make(map[string]idempotencyResult),
	}
}

func (s *memoryStore) Bootstrap(_ context.Context, mutation BootstrapMutation) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := mutation.Actor.GitHubUserID + ":bootstrap:" + mutation.RequestKey
	if existing, ok := s.idempotency[key]; ok {
		if existing.digest != mutation.RequestDigest {
			return Workspace{}, ErrVersionConflict
		}
		return cloneWorkspace(existing.workspace), nil
	}
	if s.auditErr != nil {
		return Workspace{}, s.auditErr
	}
	s.workspaces[mutation.Workspace.ID] = cloneWorkspace(mutation.Workspace)
	s.idempotency[key] = idempotencyResult{digest: mutation.RequestDigest, workspace: cloneWorkspace(mutation.Workspace)}
	return cloneWorkspace(mutation.Workspace), nil
}

func (s *memoryStore) Get(_ context.Context, id string) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, ok := s.workspaces[id]
	if !ok {
		return Workspace{}, errors.New("workspace not found")
	}
	return cloneWorkspace(workspace), nil
}

func (s *memoryStore) ConfigureRepositories(
	_ context.Context,
	mutation ConfigureRepositoriesMutation,
) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"configure_repositories",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(workspace *Workspace) error {
			project := mutation.Project
			knowledge := mutation.Knowledge
			workspace.Project = &project
			workspace.Knowledge = &knowledge
			return nil
		},
	)
}

func (s *memoryStore) AddRepositoryBinding(
	_ context.Context,
	mutation AddRepositoryBindingMutation,
) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"add_repository",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(workspace *Workspace) error {
			binding := mutation.Binding
			if binding.Role == RepositoryRoleProject {
				if workspace.Project != nil {
					return ErrPolicyBlocked
				}
				workspace.Project = &binding
				return nil
			}
			if workspace.Knowledge != nil {
				return ErrPolicyBlocked
			}
			workspace.Knowledge = &binding
			return nil
		},
	)
}

func (s *memoryStore) RevokeMember(_ context.Context, mutation RevokeMemberMutation) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"revoke_member",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(workspace *Workspace) error {
			for index := range workspace.Members {
				if workspace.Members[index].GitHubUserID == mutation.GitHubUserID && workspace.Members[index].RevokedAt == nil {
					now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
					workspace.Members[index].RevokedAt = &now
					return nil
				}
			}
			return ErrValidation
		},
	)
}

func (s *memoryStore) UpsertMember(_ context.Context, mutation UpsertMemberMutation) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"upsert_member",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(workspaceValue *Workspace) error {
			for index := range workspaceValue.Members {
				if workspaceValue.Members[index].GitHubUserID == mutation.GitHubUserID {
					workspaceValue.Members[index].Role = mutation.Role
					workspaceValue.Members[index].RevokedAt = nil
					return nil
				}
			}
			workspaceValue.Members = append(workspaceValue.Members, Member{
				GitHubUserID: mutation.GitHubUserID,
				Role:         mutation.Role,
			})
			return nil
		},
	)
}

func (s *memoryStore) mutate(
	actor Actor,
	workspaceID string,
	expectedVersion int64,
	commandType string,
	requestKey string,
	requestDigest string,
	apply func(*Workspace) error,
) (Workspace, error) {
	key := actor.GitHubUserID + ":" + commandType + ":" + workspaceID + ":" + requestKey
	if existing, ok := s.idempotency[key]; ok {
		if existing.digest != requestDigest {
			return Workspace{}, ErrVersionConflict
		}
		return cloneWorkspace(existing.workspace), nil
	}
	workspace, ok := s.workspaces[workspaceID]
	if !ok {
		return Workspace{}, ErrForbidden
	}
	member, authorized := activeMember(workspace, actor.GitHubUserID)
	if !authorized || member.Role != RoleAdmin {
		return Workspace{}, ErrForbidden
	}
	if workspace.Version != expectedVersion {
		return Workspace{}, ErrVersionConflict
	}
	candidate := cloneWorkspace(workspace)
	if err := apply(&candidate); err != nil {
		return Workspace{}, err
	}
	candidate.Version++
	if s.auditErr != nil {
		return Workspace{}, s.auditErr
	}
	s.workspaces[workspaceID] = cloneWorkspace(candidate)
	s.idempotency[key] = idempotencyResult{digest: requestDigest, workspace: cloneWorkspace(candidate)}
	return cloneWorkspace(candidate), nil
}

func cloneWorkspace(workspace Workspace) Workspace {
	clone := workspace
	clone.Members = append([]Member(nil), workspace.Members...)
	if workspace.Project != nil {
		project := *workspace.Project
		clone.Project = &project
	}
	if workspace.Knowledge != nil {
		knowledge := *workspace.Knowledge
		clone.Knowledge = &knowledge
	}
	return clone
}
