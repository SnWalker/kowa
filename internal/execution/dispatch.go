package execution

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// This file holds the additive orchestration application contract of
// backend:S06. NodeRun and Task state stay owned by this module: the ports here
// materialise one node round with its first attempt from already frozen facts,
// and record a control-plane node decision. A capability round is still decided
// only by ReportResult, and a run still reaches SUCCEEDED only through the
// delivery closure transaction; neither port can do either.

// DispatchRoundKind says why a node round may exist.
type DispatchRoundKind string

const (
	// RoundInitial is the first round of a node. It attaches the first attempt
	// either to the round run admission already created, or to a new round when
	// the node has none yet.
	RoundInitial DispatchRoundKind = "initial"
	// RoundRevision is a bounded automatic revision of a settled round.
	RoundRevision DispatchRoundKind = "revision"
	// RoundRerun is an explicit rerun of a settled round.
	RoundRerun DispatchRoundKind = "rerun"
)

// DispatchRound carries the caller's frozen round policy for one dispatch. The
// bound is the caller's policy (for the fixed MVP flow: one initial round plus
// three automatic revisions); this module never invents a global budget.
type DispatchRound struct {
	Kind DispatchRoundKind
	// SupersedesNodeRunID is the settled round a revision or rerun replaces. It
	// must equal the node's current round.
	SupersedesNodeRunID string
	// MaxRounds bounds the rounds one node may reach, this round included.
	MaxRounds int
}

// maxDispatchRounds keeps a caller-supplied bound inside the persisted
// iteration column's practical range.
const maxDispatchRounds = 1000

// DispatchNodeCommand materialises one node round and its first attempt. Every
// field is constructed from already frozen facts: the run's admission
// (provider, deadline horizon), the frozen compiled plan (the node and its
// capability) and the frozen input binding (the input digest and git scopes).
type DispatchNodeCommand struct {
	RunID              string
	NodeID             string
	NodeRunID          string
	TaskID             string
	ExpectedRunVersion int64
	InputDigest        string
	Capability         Capability
	Provider           ProviderSelection
	GitScopes          []GitScope
	DeadlineAt         time.Time
	Round              DispatchRound
}

// DispatchOutcome reports the round and attempt the command materialised.
type DispatchOutcome struct {
	NodeRun   NodeRun
	Task      Task
	Duplicate bool
}

// DecideNodeCommand records the decision of a control-plane node (a system,
// human, join or end node). It materialises the node's first round when the
// node has none yet, so a control-plane round always carries the digest of its
// own frozen input document. Capability rounds are decided by result acceptance
// and are refused here.
type DecideNodeCommand struct {
	RunID              string
	NodeID             string
	NodeRunID          string
	ExpectedRunVersion int64
	InputDigest        string
	State              NodeState
	Verdict            string
	Output             map[string]any
	// Round carries the same frozen round policy as DispatchNode: the initial
	// round of the node, or a sanctioned revision/rerun of its settled round.
	Round DispatchRound
}

// DecideOutcome reports the round the decision was applied to.
type DecideOutcome struct {
	NodeRun   NodeRun
	Duplicate bool
}

var scopeRefPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|refs/heads/[A-Za-z0-9][A-Za-z0-9._/-]{0,254})$`)

// scopeOIDPattern is the only shape a frozen read input may take.
var scopeOIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// DispatchNode validates the command shape and delegates to the store, which
// applies the run, round and plan invariants in one transaction.
func (s *Service) DispatchNode(ctx context.Context, command DispatchNodeCommand) (DispatchOutcome, error) {
	if command.RunID == "" || command.NodeID == "" || command.NodeRunID == "" || command.TaskID == "" ||
		command.ExpectedRunVersion < 1 || !digestPattern.MatchString(command.InputDigest) ||
		command.Capability.ID == "" || command.Capability.Version < 1 ||
		command.Provider.ID == "" || command.Provider.Version == "" ||
		command.DeadlineAt.IsZero() || len(command.GitScopes) > maxDispatchScopes {
		return DispatchOutcome{}, ErrInvalidInput
	}
	switch command.Round.Kind {
	case RoundInitial:
		if command.Round.SupersedesNodeRunID != "" {
			return DispatchOutcome{}, ErrInvalidInput
		}
	case RoundRevision, RoundRerun:
		if command.Round.SupersedesNodeRunID == "" {
			return DispatchOutcome{}, ErrInvalidInput
		}
	default:
		return DispatchOutcome{}, ErrInvalidInput
	}
	if command.Round.MaxRounds < 1 || command.Round.MaxRounds > maxDispatchRounds {
		return DispatchOutcome{}, ErrInvalidInput
	}
	for _, scope := range command.GitScopes {
		if !validGitScope(scope) || !scopeRefPattern.MatchString(scope.Ref) {
			return DispatchOutcome{}, fmt.Errorf("%w: git scope %q is not a frozen commit oid or branch reference",
				ErrInvalidInput, scope.Ref)
		}
		// A read scope carries a frozen input, so it must be an immutable commit
		// oid: a branch reference is only ever a write authorization, and the
		// latest tip of a branch never substitutes a frozen version.
		if readOperation(scope.Operation) && !scopeOIDPattern.MatchString(scope.Ref) {
			return DispatchOutcome{}, fmt.Errorf(
				"%w: read scope %s of repository %s must be a frozen commit oid, not %q",
				ErrInvalidInput, scope.Operation, scope.RepositoryID, scope.Ref)
		}
	}
	return s.store.DispatchNode(ctx, command, s.now().UTC())
}

// maxDispatchScopes bounds one attempt's authorized scope list.
const maxDispatchScopes = 8

// SameInstant compares two timestamps at the resolution the store keeps, so a
// replay of the same frozen attempt is recognised even when the caller's clock
// is finer than the persisted column.
func SameInstant(left, right time.Time) bool {
	return left.UTC().Truncate(time.Microsecond).Equal(right.UTC().Truncate(time.Microsecond))
}

// readOperation reports whether one frozen scope operation reads a repository.
func readOperation(operation string) bool {
	switch operation {
	case "clone", "fetch", "pull":
		return true
	default:
		return false
	}
}

// DecideNode validates the command shape and delegates to the store.
func (s *Service) DecideNode(ctx context.Context, command DecideNodeCommand) (DecideOutcome, error) {
	if command.RunID == "" || command.NodeID == "" || command.NodeRunID == "" ||
		command.ExpectedRunVersion < 1 || !digestPattern.MatchString(command.InputDigest) {
		return DecideOutcome{}, ErrInvalidInput
	}
	if command.State != NodeDecided && command.State != NodeWaitingHuman {
		return DecideOutcome{}, ErrInvalidInput
	}
	if command.State == NodeDecided && command.Verdict == "" || len(command.Verdict) > 200 {
		return DecideOutcome{}, ErrInvalidInput
	}
	if command.State == NodeWaitingHuman && (command.Verdict != "" || command.Output != nil) {
		return DecideOutcome{}, ErrInvalidInput
	}
	switch command.Round.Kind {
	case RoundInitial:
		if command.Round.SupersedesNodeRunID != "" {
			return DecideOutcome{}, ErrInvalidInput
		}
	case RoundRevision, RoundRerun:
		if command.Round.SupersedesNodeRunID == "" {
			return DecideOutcome{}, ErrInvalidInput
		}
	default:
		return DecideOutcome{}, ErrInvalidInput
	}
	if command.Round.MaxRounds < 1 || command.Round.MaxRounds > maxDispatchRounds {
		return DecideOutcome{}, ErrInvalidInput
	}
	return s.store.DecideNode(ctx, command, s.now().UTC())
}

// OpenRoundCommand materialises one node round without an attempt. A
// control-plane writer that has to reference a round before its attempt exists
// (an external operation bound to the round, for example) opens it first; the
// dispatch that follows attaches the attempt to the same round.
type OpenRoundCommand struct {
	RunID              string
	NodeID             string
	NodeRunID          string
	ExpectedRunVersion int64
	InputDigest        string
	Round              DispatchRound
}

// OpenNodeRound validates the command shape and delegates to the store.
func (s *Service) OpenNodeRound(ctx context.Context, command OpenRoundCommand) (NodeRun, error) {
	if command.RunID == "" || command.NodeID == "" || command.NodeRunID == "" ||
		command.ExpectedRunVersion < 1 || !digestPattern.MatchString(command.InputDigest) {
		return NodeRun{}, ErrInvalidInput
	}
	switch command.Round.Kind {
	case RoundInitial:
		if command.Round.SupersedesNodeRunID != "" {
			return NodeRun{}, ErrInvalidInput
		}
	case RoundRevision, RoundRerun:
		if command.Round.SupersedesNodeRunID == "" {
			return NodeRun{}, ErrInvalidInput
		}
	default:
		return NodeRun{}, ErrInvalidInput
	}
	if command.Round.MaxRounds < 1 || command.Round.MaxRounds > maxDispatchRounds {
		return NodeRun{}, ErrInvalidInput
	}
	return s.store.OpenNodeRound(ctx, command, s.now().UTC())
}
