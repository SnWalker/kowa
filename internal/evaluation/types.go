// Package evaluation owns verification evidence and the closure decision: the
// closing checklist binds every required item to an exact fact, and the
// WorkItem close plus the delivery run success commit in one authoritative
// transaction. Auxiliary evidence (knowledge suggestions, offline evaluation)
// is recorded here but never blocks a delivery closure.
package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

const (
	SchemaVersion = "kowa.delivery-governance.v1"

	VerificationKind     = "verificationEvidence"
	VerificationListKind = "verificationEvidenceList"
	ClosureKind          = "closureEvidence"
)

var (
	ErrValidation      = errors.New("evaluation: invalid input")
	ErrNotFound        = errors.New("evaluation: not found")
	ErrForbidden       = errors.New("evaluation: forbidden")
	ErrVersionConflict = errors.New("evaluation: version conflict")
	ErrPolicyBlocked   = errors.New("evaluation: policy blocked")
	ErrNotImplemented  = errors.New("evaluation: not implemented")
)

// Verification kinds. The first four are synchronous delivery gates; the last
// two are auxiliary and never block a closure.
const (
	KindRequiredTests        = "required_tests"
	KindIndependentReview    = "independent_review"
	KindSecurityReview       = "security_review"
	KindPostMergeIntegration = "post_merge_integration"
	KindKnowledgeSuggestion  = "knowledge_suggestion"
	KindOfflineEvaluation    = "offline_evaluation"
)

// Result values.
const (
	ResultPassed = "passed"
	ResultFailed = "failed"
)

// Source values. Simulated evidence is recorded honestly and never satisfies a
// real gate.
const (
	SourceReal      = "real"
	SourceSimulated = "simulated"
)

// ProducerKinds.
const (
	ProducerTask         = "task"
	ProducerExternalCI   = "external_ci"
	ProducerControlPlane = "control_plane"
)

// AuthorizedReviewer reports whether GitHub states that the reviewer is a user
// account with write access to the repository; the rule mirrors the delivery
// package so both gates agree.
func AuthorizedReviewer(accountType, permission string) bool {
	if accountType != "User" {
		return false
	}
	return permission == "admin" || permission == "write"
}

// Gating reports whether one verification kind is a synchronous delivery gate.
func Gating(kind string) bool {
	switch kind {
	case KindRequiredTests, KindIndependentReview, KindSecurityReview, KindPostMergeIntegration:
		return true
	default:
		return false
	}
}

// KnownKind reports whether the kind is part of the frozen vocabulary.
func KnownKind(kind string) bool {
	switch kind {
	case KindRequiredTests, KindIndependentReview, KindSecurityReview,
		KindPostMergeIntegration, KindKnowledgeSuggestion, KindOfflineEvaluation:
		return true
	default:
		return false
	}
}

// Verification is one recorded verification fact.
type Verification struct {
	VerificationID       string    `json:"verificationId"`
	WorkspaceID          string    `json:"workspaceId"`
	WorkItemID           string    `json:"workItemId"`
	RunID                string    `json:"workflowRunId,omitempty"`
	NodeRunID            string    `json:"nodeRunId,omitempty"`
	Kind                 string    `json:"kind"`
	SubjectKind          string    `json:"subjectKind"`
	RepositoryID         string    `json:"repositoryId,omitempty"`
	CommitOID            string    `json:"commitOid,omitempty"`
	ArtifactID           string    `json:"artifactId,omitempty"`
	ArtifactDigest       string    `json:"artifactDigest,omitempty"`
	ConfigurationVersion string    `json:"configurationVersion"`
	ProducerKind         string    `json:"producerKind"`
	ProducerID           string    `json:"producerId"`
	Source               string    `json:"source"`
	Result               string    `json:"result"`
	ReportArtifactID     string    `json:"reportArtifactId,omitempty"`
	ReportDigest         string    `json:"reportDigest,omitempty"`
	ObservedAt           time.Time `json:"observedAt"`
	CreatedAt            time.Time `json:"createdAt"`
}

// RecordVerificationCommand records one verification fact. Required tests,
// reviews and post-merge integration checks are all recorded here.
type RecordVerificationCommand struct {
	WorkspaceID          string
	WorkItemID           string
	RunID                string
	NodeRunID            string
	Kind                 string
	SubjectKind          string
	RepositoryID         string
	CommitOID            string
	ArtifactID           string
	ArtifactDigest       string
	ConfigurationVersion string
	ProducerKind         string
	ProducerID           string
	Source               string
	Result               string
	ReportArtifactID     string
	ReportDigest         string
	RequestKey           string
	ActorID              string
}

// ClosureRecord is the evidence bundle one closure actually used. Every item is
// bound by identity, so a later version of any object cannot inherit it.
type ClosureRecord struct {
	ClosureID               string    `json:"closureId"`
	WorkspaceID             string    `json:"workspaceId"`
	WorkItemID              string    `json:"workItemId"`
	RunID                   string    `json:"workflowRunId"`
	DefinitionName          string    `json:"definitionName"`
	DefinitionVersion       int       `json:"definitionVersion"`
	DefinitionDigest        string    `json:"definitionDigest"`
	PolicyVersion           string    `json:"policyVersion"`
	RequirementDigest       string    `json:"requirementDigest"`
	KnowledgeSnapshotID     string    `json:"knowledgeSnapshotId,omitempty"`
	KnowledgeBindingID      string    `json:"knowledgeBindingEventId,omitempty"`
	MergeRepositoryID       string    `json:"mergeRepositoryId"`
	MergeCommitOID          string    `json:"mergeCommitOid"`
	PullRequestNumber       string    `json:"pullRequestNumber"`
	MergeObservationID      string    `json:"mergeObservationId"`
	ReviewObservationID     string    `json:"reviewObservationId"`
	ReviewHeadOID           string    `json:"reviewHeadOid"`
	ReviewerGitHubUserID    string    `json:"reviewerGitHubUserId"`
	MergedByGitHubUserID    string    `json:"mergedByGitHubUserId"`
	MergedByType            string    `json:"mergedByType"`
	RequiredTestIDs         []string  `json:"requiredTestVerificationIds"`
	IndependentReviewID     string    `json:"independentReviewVerificationId"`
	PostMergeVerificationID string    `json:"postMergeVerificationId"`
	ChecklistDigest         string    `json:"checklistDigest"`
	CreatedByGitHubUserID   string    `json:"createdByGitHubUserId"`
	CreatedAt               time.Time `json:"createdAt"`
}

// CloseCommand is the request to close one WorkItem. Definition facts come from
// the run itself; the caller supplies the evidence bindings, which the service
// re-reads and re-validates instead of trusting.
type CloseCommand struct {
	WorkspaceID             string
	WorkItemID              string
	RunID                   string
	ExpectedWorkItemVersion int64
	ExpectedRunVersion      int64
	ActorID                 string
	RequestKey              string
	PolicyVersion           string
	KnowledgeSnapshotID     string
	KnowledgeBindingEventID string
	PullRequestNumber       string
	MergeObservationID      string
	ReviewObservationID     string
	RequiredTestIDs         []string
	IndependentReviewID     string
	PostMergeVerificationID string
}

// CloseMutation is the atomic store command: the run success, the WorkItem
// close and the closure record commit together, or none of them do.
type CloseMutation struct {
	Record                  ClosureRecord
	ExpectedRunVersion      int64
	ExpectedWorkItemVersion int64
	ActorID                 string
	RequestKey              string
	RequestDigest           string
	ClosedAt                time.Time
}

// AtomicCloser applies a closure together with its WorkItem close and the
// delivery run success. The frozen invariant is that all three commit or none
// do, so the only implementation is the PostgreSQL single transaction: the
// in-memory adapters deliberately do not offer closing.
type AtomicCloser interface {
	Close(ctx context.Context, mutation CloseMutation) (ClosureRecord, error)
}

// Store persists verification evidence: the only aggregate both adapters own.
type Store interface {
	// RecordVerification records one verification fact. actorID is the acting
	// authenticated identity for idempotency and audit (a decimal GitHub user
	// id); ProducerID stays a data field and is never used as an actor.
	RecordVerification(ctx context.Context, verification Verification, actorID, requestKey, requestDigest string) (Verification, error)
	Verification(context.Context, string, string) (Verification, bool, error)
	VerificationsFor(ctx context.Context, workspaceID, workItemID string) ([]Verification, error)
}

// ClosureReader reads closure records and replays close commands. Closing is
// the atomic PostgreSQL transaction's responsibility, so only that adapter
// implements this port; consumers must treat its absence as "no closure".
type ClosureReader interface {
	Closure(ctx context.Context, workspaceID, workItemID string) (ClosureRecord, bool, error)
	// ReplayedClose returns the recorded outcome of an identical close command,
	// or false when this (actor, work item, request key) was never recorded.
	// The service consults it before the checklist so a replay of an accepted
	// closure is answered with that closure instead of a state rejection.
	ReplayedClose(ctx context.Context, actor, workItemID, requestKey, digest string) (ClosureRecord, bool, error)
}

// WorkItemSource reads the WorkItem facts; the WorkItem service owns them.
type WorkItemSource interface {
	Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error)
}

// RunSource reads the delivery run; the execution service owns it.
type RunSource interface {
	Run(ctx context.Context, runID string) (execution.Run, error)
}

// KnowledgeSource verifies that the knowledge confirmation a closure binds
// exists in the workspace, in the same snapshot, and is bound to this work
// item; backend:S03 owns those rows.
type KnowledgeSource interface {
	KnowledgeBindingExists(ctx context.Context, workspaceID, workItemID, snapshotID, bindingEventID string) (bool, error)
}

// PublishPrSource reports the pull request number of the settled publish_pr
// operation that delivered one exact run, repository and commit; the GitHub
// delivery service owns that fact. A settled operation of another run, or of
// another head, must never satisfy a closure.
type PublishPrSource interface {
	SettledPublishPullRequest(ctx context.Context, workspaceID, workItemID, runID, repositoryID, commitOID string) (string, bool, error)
}

// FactSource reads one reconciled GitHub observation. The GitHub delivery
// service owns those facts; an unknown observation never satisfies a gate.
type FactSource interface {
	Observation(ctx context.Context, workspaceID, observationID string) (ObservationFact, bool, error)
}

// ObservationFact is the local mirror of the fact fields the closure binds, so
// this package does not depend on the delivery package's storage shapes.
type ObservationFact struct {
	ObservationID string
	RepositoryID  string
	SubjectKind   string
	SubjectKey    string
	Unknown       bool

	PullRequestAuthorID string
	HeadOID             string
	BaseRef             string
	State               string
	Merged              bool
	MergeCommitOID      string
	MergedByID          string
	MergedByType        string

	ReviewID                string
	ReviewerID              string
	ReviewAuthorAssociation string
	ReviewerType            string
	ReviewerPermission      string
	ReviewState             string
	ReviewCommitOID         string
}

// IDGenerator creates opaque evidence and closure ids.
type IDGenerator interface {
	New() (string, error)
}

var (
	requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	externalIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	oidPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$`)
)

// Digest is the frozen request-digest rule for command idempotency.
func Digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validRequestKey(key string) bool { return requestKeyPattern.MatchString(key) }
func validActor(actor string) bool    { return externalIDPattern.MatchString(actor) }
