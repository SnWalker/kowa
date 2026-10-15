// Package orchestration owns run admission, the frozen task input binding, the
// knowledge recommendation and confirmation integration, node advancement and
// the recovery commands of the single built-in workflow (kowa.fixed-workflow).
//
// It consumes the frozen application ports of the other modules and never
// reinterprets their business decisions: execution owns runs, node rounds and
// attempts; knowledge/artifact owns frozen content and confirmations; workitem
// owns the goal and its single advancement run; humantask owns answers;
// delivery/evaluation own external facts and the closure transaction.
package orchestration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SchemaVersion is the contract version of the Web application surface.
const SchemaVersion = "kowa.web-application.v1"

var (
	ErrValidation          = errors.New("orchestration: invalid input")
	ErrNotFound            = errors.New("orchestration: not found")
	ErrForbidden           = errors.New("orchestration: forbidden")
	ErrVersionConflict     = errors.New("orchestration: version conflict")
	ErrPolicyBlocked       = errors.New("orchestration: policy blocked")
	ErrResourceUnavailable = errors.New("orchestration: resource unavailable")
	ErrExternalUnknown     = errors.New("orchestration: external state is unknown")

	oidPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	externalID     = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	requestKeyRule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// Classification of one node round's verdict.
const (
	// VerdictApproved is the frozen verdict of a control-plane node whose gate
	// is satisfied.
	VerdictApproved = "approved"
	// VerdictPassed is the gate-satisfying verdict of an executed verification.
	VerdictPassed = "passed"
	// VerdictRejected marks a human gate the person refused.
	VerdictRejected = "rejected"
	// VerdictNeedsRevision marks a review that asks for a bounded revision of
	// its region instead of accepting the reviewed result.
	VerdictNeedsRevision = "needs_revision"
	// VerdictFailed marks a control-plane node whose operation failed.
	VerdictFailed = "failed"
)

// gateAccepting reports whether one decided verdict satisfies downstream gates:
// a review approves, a test passes. Every other value — including a missing
// verdict — leaves the gate closed.
func gateAccepting(verdict string) bool {
	return verdict == VerdictApproved || verdict == VerdictPassed
}

// Frozen orchestration policy of the fixed MVP flow. These are this stage's
// policy values, not universal execution rules.
const (
	// MaxNodeRounds bounds the rounds one node may reach: the initial round plus
	// the three automatic revisions the MVP policy allows.
	MaxNodeRounds = 4
	// MaxKnowledgeConfirmRounds bounds how often a refused knowledge
	// confirmation may be re-asked before it needs a human-granted round.
	MaxKnowledgeConfirmRounds = 4
	// ProjectBranchMain and ProjectBranchMaster are the only delivery base
	// branches the MVP admits; the first one the repository actually has wins.
	ProjectBranchMain   = "refs/heads/main"
	ProjectBranchMaster = "refs/heads/master"
	// KnowledgeIndexPath is the frozen entry index of the knowledge repository:
	// the MVP recommends exactly the catalog it declares at the frozen commit.
	KnowledgeIndexPath = "kowa-index.json"
	// AttemptDeadlineHorizon bounds one dispatched attempt.
	AttemptDeadlineHorizon = 6 * time.Hour
)

// Human task kinds and object bindings of the fixed flow.
const (
	HumanTaskKnowledgeConfirmation = "knowledge_confirmation"
	HumanTaskPlanApproval          = "plan_approval"
	HumanTaskRevisionEscalation    = "revision_escalation"

	ObjectKindKnowledge = "knowledge_recommendation"
	ObjectKindPlan      = "plan"
)

// Candidate is one entry of the frozen knowledge index.
type Candidate struct {
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
}

// Admission is the frozen admission record of one delivery run.
type Admission struct {
	RunID                 string    `json:"workflowRunId"`
	WorkspaceID           string    `json:"workspaceId"`
	WorkItemID            string    `json:"workItemId"`
	DefinitionName        string    `json:"definitionName"`
	DefinitionVersion     int       `json:"definitionVersion"`
	DefinitionDigest      string    `json:"definitionDigest"`
	ProjectRepositoryID   string    `json:"projectRepositoryId"`
	KnowledgeRepositoryID string    `json:"knowledgeRepositoryId"`
	ProjectCommitOID      string    `json:"projectCommitOid"`
	KnowledgeCommitOID    string    `json:"knowledgeCommitOid"`
	ProjectBranchRef      string    `json:"projectBranchRef"`
	WorkingBranchRef      string    `json:"workingBranchRef"`
	ProviderID            string    `json:"providerId"`
	ProviderVersion       string    `json:"providerVersion"`
	PolicyVersion         string    `json:"policyVersion"`
	AdmittedByUserID      string    `json:"admittedByGitHubUserId"`
	AdmittedAt            time.Time `json:"admittedAt"`
}

// Recommendation is the frozen knowledge recommendation of one run.
type Recommendation struct {
	RunID                 string      `json:"workflowRunId"`
	WorkspaceID           string      `json:"workspaceId"`
	WorkItemID            string      `json:"workItemId"`
	KnowledgeRepositoryID string      `json:"knowledgeRepositoryId"`
	KnowledgeCommitOID    string      `json:"knowledgeCommitOid"`
	IndexPath             string      `json:"indexPath"`
	IndexDigest           string      `json:"indexDigest"`
	State                 string      `json:"state"`
	HumanTaskID           string      `json:"humanTaskId"`
	Candidates            []Candidate `json:"candidates"`
	SnapshotID            string      `json:"snapshotId,omitempty"`
	BindingEventID        string      `json:"bindingEventId,omitempty"`
	EffectiveInputID      string      `json:"effectiveInputId,omitempty"`
	RecommendedAt         time.Time   `json:"recommendedAt"`
	ConfirmedAt           *time.Time  `json:"confirmedAt,omitempty"`
}

// Confirmed reports whether the human confirmation already froze the binding.
func (r Recommendation) Confirmed() bool { return r.State == StateConfirmed }

// Recommendation states.
const (
	StateRecommended = "RECOMMENDED"
	StateConfirmed   = "CONFIRMED"
)

// CausalRef is one frozen causal reference of a binding: the round of a
// dependency gate or declared input producer the dispatch was built from, plus
// the deliverable identity when the referenced node publishes one. Staleness
// compares these with the current rounds; per-node counters are never compared.
type CausalRef struct {
	NodeID     string `json:"nodeId"`
	NodeRunID  string `json:"nodeRunId"`
	ArtifactID string `json:"artifactId,omitempty"`
}

// Binding is the immutable frozen input binding of one dispatched attempt. It
// records the complete dispatch intent, so a restarted control plane reuses the
// same identity, digest and deadline instead of creating a second attempt.
type Binding struct {
	TaskID                string      `json:"taskId"`
	WorkspaceID           string      `json:"workspaceId"`
	RunID                 string      `json:"workflowRunId"`
	NodeID                string      `json:"nodeId"`
	NodeRunID             string      `json:"nodeRunId"`
	InputID               string      `json:"inputId"`
	InputDigest           string      `json:"inputDigest"`
	ProjectRepositoryID   string      `json:"projectRepositoryId"`
	ProjectCommitOID      string      `json:"projectCommitOid"`
	KnowledgeRepositoryID string      `json:"knowledgeRepositoryId"`
	KnowledgeCommitOID    string      `json:"knowledgeCommitOid"`
	CapabilityID          string      `json:"capabilityId"`
	CapabilityVersion     int         `json:"capabilityVersion"`
	ProviderID            string      `json:"providerId"`
	ProviderVersion       string      `json:"providerVersion"`
	Scopes                []Scope     `json:"gitScopes"`
	ArtifactIDs           []string    `json:"artifactIds"`
	CausalRefs            []CausalRef `json:"causalRefs"`
	DeadlineAt            time.Time   `json:"deadlineAt"`
	BoundAt               time.Time   `json:"boundAt"`
}

// Scope is the frozen git scope shape of one binding, mirroring the execution
// wire so a binding can be materialised into an attempt without translation.
type Scope struct {
	RepositoryID string `json:"repositoryId"`
	Role         string `json:"role"`
	Operation    string `json:"operation"`
	Ref          string `json:"ref"`
}

// RecoveryKind is one explicit recovery command of the fixed flow.
type RecoveryKind string

const (
	RecoveryRetry  RecoveryKind = "retry"
	RecoveryRerun  RecoveryKind = "rerun"
	RecoveryCancel RecoveryKind = "cancel"
	RecoveryRepair RecoveryKind = "repair"
	// RecoveryRetrySuggestion re-attempts the auxiliary knowledge-suggestion
	// branch of a run that has already closed its work item. It is not a
	// business rerun: it never touches the run, the item or the closure.
	RecoveryRetrySuggestion RecoveryKind = "retry-knowledge-suggestion"
)

// SuggestionState is the state of one auxiliary suggestion attempt.
type SuggestionState string

const (
	SuggestionPending    SuggestionState = "PENDING"
	SuggestionProcessing SuggestionState = "PROCESSING"
	SuggestionPublished  SuggestionState = "PUBLISHED"
	SuggestionFailed     SuggestionState = "FAILED"
)

// SuggestionAttempt is one persisted attempt of the auxiliary
// knowledge-suggestion branch. Attempts are append-only: a retry opens the next
// attempt and the previous failure stays readable.
type SuggestionAttempt struct {
	RunID          string          `json:"workflowRunId"`
	WorkspaceID    string          `json:"workspaceId"`
	WorkItemID     string          `json:"workItemId"`
	Attempt        int             `json:"attempt"`
	AttemptID      string          `json:"attemptId"`
	State          SuggestionState `json:"state"`
	InputDigest    string          `json:"inputDigest"`
	ClaimedUntil   *time.Time      `json:"claimedUntil,omitempty"`
	ArtifactID     string          `json:"artifactId,omitempty"`
	ArtifactDigest string          `json:"artifactDigest,omitempty"`
	FailureReason  string          `json:"failureReason,omitempty"`
	RequestedAt    time.Time       `json:"requestedAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	Version        int64           `json:"version"`
}

// SuggestionSeed materialises the first auxiliary attempt of a run. The input
// digest is the frozen identity of everything the suggestion derives from; the
// seed is idempotent and a drifted input is refused.
type SuggestionSeed struct {
	RunID       string
	WorkspaceID string
	WorkItemID  string
	InputDigest string
	At          time.Time
}

// SuggestionClaim fences one attempt under a lease so a competing or restarted
// process cannot process it twice.
type SuggestionClaim struct {
	AttemptID  string
	LeaseUntil time.Time
	At         time.Time
}

// SuggestionOutcome finalises one claimed attempt. The version proves the
// claimant still owns the lease: a stale claimant cannot overwrite it.
type SuggestionOutcome struct {
	RunID           string
	Attempt         int
	ExpectedVersion int64
	ArtifactID      string
	ArtifactDigest  string
	FailureReason   string
	At              time.Time
}

// SuggestionRequest opens the next attempt after a failed one. The store
// refuses it unless the newest attempt failed, so an open or published attempt
// is never duplicated.
type SuggestionRequest struct {
	RunID string
	At    time.Time
}

// RecoveryCommand is one authorized recovery request. Identities, versions and
// budgets are re-read by the service; the caller never supplies a verdict.
type RecoveryCommand struct {
	WorkspaceID     string
	WorkItemID      string
	RunID           string
	Kind            RecoveryKind
	NodeID          string
	Reason          string
	MergedBaseline  string
	ExpectedVersion int64
	RequestKey      string
	ActorID         string
}

// RunRecovery is the outcome of one recovery command.
type RunRecovery struct {
	RunID      string       `json:"workflowRunId"`
	Kind       RecoveryKind `json:"kind"`
	Applied    bool         `json:"applied"`
	RunState   string       `json:"state"`
	RunVersion int64        `json:"version"`
	Detail     string       `json:"detail,omitempty"`
	RecordedAt time.Time    `json:"recordedAt"`
}

// newID returns one opaque identity whose shape is safe in URLs, branch names
// and artifact references.
func newID(prefix string) (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate identity: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buffer[:]), nil
}

// validActor reports whether the identifier is a decimal GitHub user id.
func validActor(value string) bool { return externalID.MatchString(value) }

// errorsIs keeps the error classification of the consumed modules explicit.
func errorsIs(err, target error) bool { return errors.Is(err, target) }

// SameInstant compares two timestamps at the resolution the store keeps.
// PostgreSQL timestamps carry microseconds, while a caller's time.Now() may
// carry nanoseconds, so a replay of the very same intent must not be refused
// for sub-microsecond drift.
func SameInstant(left, right time.Time) bool {
	return left.UTC().Truncate(time.Microsecond).Equal(right.UTC().Truncate(time.Microsecond))
}

// commandDigest is the idempotency digest rule of this module's commands. The
// command-idempotency table stores the bare hex form, so the wire digests keep
// their sha256 prefix while these comparisons do not.
func commandDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
