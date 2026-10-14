package githubobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PullRequestObservation is one observed pull request. The head oid, the
// author's numeric identity and the base ref are the fields delivery gates
// compare; a read that fails stays unknown instead of guessing.
type PullRequestObservation struct {
	RepositoryID   string    `json:"repositoryId"`
	Number         string    `json:"number,omitempty"`
	AuthorID       string    `json:"authorId,omitempty"`
	HeadOID        string    `json:"headOid,omitempty"`
	HeadRef        string    `json:"headRef,omitempty"`
	BaseRef        string    `json:"baseRef,omitempty"`
	State          string    `json:"state,omitempty"`
	Merged         bool      `json:"merged"`
	MergeCommitOID string    `json:"mergeCommitOid,omitempty"`
	MergedByID     string    `json:"mergedById,omitempty"`
	MergedAt       time.Time `json:"mergedAt,omitempty"`
	ObservedAt     time.Time `json:"observedAt"`
	ActorID        string    `json:"actorId,omitempty"`
	Unknown        bool      `json:"unknown"`
	UnknownReason  string    `json:"unknownReason,omitempty"`
}

// ReviewObservation is one observed review on a pull request.
type ReviewObservation struct {
	RepositoryID  string    `json:"repositoryId"`
	Number        string    `json:"number"`
	ReviewID      string    `json:"reviewId,omitempty"`
	ReviewerID    string    `json:"reviewerId,omitempty"`
	State         string    `json:"state,omitempty"`
	CommitOID     string    `json:"commitOid,omitempty"`
	SubmittedAt   time.Time `json:"submittedAt,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
	ActorID       string    `json:"actorId,omitempty"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

// ChecksObservation is the aggregated check state of one commit: never merged
// into a single "healthy" answer, and `none` is distinct from `pending`.
type ChecksObservation struct {
	RepositoryID  string    `json:"repositoryId"`
	CommitOID     string    `json:"commitOid"`
	State         string    `json:"state,omitempty"`
	Total         int       `json:"total"`
	Failed        int       `json:"failed"`
	Pending       int       `json:"pending"`
	ObservedAt    time.Time `json:"observedAt"`
	ActorID       string    `json:"actorId,omitempty"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

// PermissionObservation is GitHub's own permission fact about the acting
// identity on one repository.
type PermissionObservation struct {
	RepositoryID  string    `json:"repositoryId"`
	Push          bool      `json:"push"`
	Admin         bool      `json:"admin"`
	ObservedAt    time.Time `json:"observedAt"`
	ActorID       string    `json:"actorId,omitempty"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

// DeliveryObserver is the S05 read surface over the same verified gh identity
// the S04 Observer uses. Pull-request, review, check, merge and permission
// facts are read-only: this seam never writes to GitHub.
type DeliveryObserver interface {
	Identity(ctx context.Context) (string, error)
	Repository(ctx context.Context, repositoryID string) (RepositoryObservation, error)
	BranchHead(ctx context.Context, repositoryID, ref string) (RefObservation, error)
	PullRequest(ctx context.Context, repositoryID, number string) (PullRequestObservation, error)
	PullRequestsByHead(ctx context.Context, repositoryID, headBranch string) ([]PullRequestObservation, error)
	Reviews(ctx context.Context, repositoryID, number string) ([]ReviewObservation, error)
	Checks(ctx context.Context, repositoryID, commitOID string) (ChecksObservation, error)
	Permission(ctx context.Context, repositoryID string) (PermissionObservation, error)
}

// Identity reports the currently authenticated numeric account id.
func (o *GHObserver) Identity(ctx context.Context) (string, error) {
	return o.activeIdentity(ctx)
}

// PullRequest reads one pull request by its number.
func (o *GHObserver) PullRequest(ctx context.Context, repositoryID, number string) (PullRequestObservation, error) {
	observation := PullRequestObservation{RepositoryID: repositoryID, Number: number, ObservedAt: o.now().UTC()}
	repository, err := o.Repository(ctx, repositoryID)
	if err != nil {
		observation.Unknown, observation.UnknownReason = true, repository.UnknownReason
		return observation, fmt.Errorf("%w: repository identity is not established", ErrUnknown)
	}
	observation.ActorID = repository.ActorID
	result := o.execute(ctx, "api", fmt.Sprintf("repos/%s/pulls/%s", repository.FullName, number), "--jq", pullRequestJQ)
	if result.err != nil || result.exitCode != 0 {
		observation.Unknown, observation.UnknownReason = true, UnknownUnavailable
		return observation, fmt.Errorf("%w: pull request query failed", ErrUnknown)
	}
	if err := decodePullRequest(repositoryID, number, repository.ActorID, strings.TrimSpace(result.stdout), &observation); err != nil {
		observation.Unknown, observation.UnknownReason = true, UnknownMalformed
		return observation, fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	return observation, nil
}

// PullRequestsByHead lists the pull requests whose head is one branch. The
// query is the reconciliation path for an unknown PR creation result: it must
// find the existing object before any retry.
func (o *GHObserver) PullRequestsByHead(ctx context.Context, repositoryID, headBranch string) ([]PullRequestObservation, error) {
	repository, err := o.Repository(ctx, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("%w: repository identity is not established", ErrUnknown)
	}
	owner, _, found := strings.Cut(repository.FullName, "/")
	if !found || owner == "" || headBranch == "" {
		return nil, fmt.Errorf("%w: head branch cannot be qualified", ErrUnknown)
	}
	result := o.execute(ctx, "api",
		fmt.Sprintf("repos/%s/pulls?state=all&head=%s:%s&per_page=30", repository.FullName, owner, headBranch),
		"--jq", "["+pullRequestJQ+"]")
	if result.err != nil || result.exitCode != 0 {
		return nil, fmt.Errorf("%w: pull request query failed", ErrUnknown)
	}
	var payload []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.stdout)), &payload); err != nil {
		return nil, fmt.Errorf("%w: pull request list is malformed", ErrUnknown)
	}
	observations := make([]PullRequestObservation, 0, len(payload))
	for _, raw := range payload {
		observation := PullRequestObservation{RepositoryID: repositoryID, ObservedAt: o.now().UTC(), ActorID: repository.ActorID}
		if err := decodePullRequest(repositoryID, "", repository.ActorID, string(raw), &observation); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnknown, err)
		}
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(left, right int) bool {
		return observations[left].Number < observations[right].Number
	})
	return observations, nil
}

// Reviews lists the reviews of one pull request.
func (o *GHObserver) Reviews(ctx context.Context, repositoryID, number string) ([]ReviewObservation, error) {
	repository, err := o.Repository(ctx, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("%w: repository identity is not established", ErrUnknown)
	}
	result := o.execute(ctx, "api",
		fmt.Sprintf("repos/%s/pulls/%s/reviews?per_page=100", repository.FullName, number),
		"--jq", `[.[] | {id: (.id|tostring), reviewer: ((.user.id // 0)|tostring), state: .state, commit: (.commit_id // ""), submitted: (.submitted_at // "")}]`)
	if result.err != nil || result.exitCode != 0 {
		return nil, fmt.Errorf("%w: review query failed", ErrUnknown)
	}
	var payload []struct {
		ID        string `json:"id"`
		Reviewer  string `json:"reviewer"`
		State     string `json:"state"`
		Commit    string `json:"commit"`
		Submitted string `json:"submitted"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.stdout)), &payload); err != nil {
		return nil, fmt.Errorf("%w: review list is malformed", ErrUnknown)
	}
	observations := make([]ReviewObservation, 0, len(payload))
	for _, review := range payload {
		observation := ReviewObservation{
			RepositoryID: repositoryID, Number: number, ReviewID: review.ID,
			ReviewerID: review.Reviewer, State: review.State, CommitOID: review.Commit,
			ObservedAt: o.now().UTC(), ActorID: repository.ActorID,
		}
		if review.Submitted != "" {
			submitted, err := time.Parse(time.RFC3339, review.Submitted)
			if err != nil {
				return nil, fmt.Errorf("%w: review timestamp is malformed", ErrUnknown)
			}
			observation.SubmittedAt = submitted.UTC()
		}
		if observation.ReviewID == "" || !repoPattern.MatchString(observation.ReviewerID) {
			return nil, fmt.Errorf("%w: review identity is malformed", ErrUnknown)
		}
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(left, right int) bool {
		return observations[left].ReviewID < observations[right].ReviewID
	})
	return observations, nil
}

// Checks aggregates the check runs of one commit. `none` is reported as its
// own state so an absent check suite never reads as passed.
func (o *GHObserver) Checks(ctx context.Context, repositoryID, commitOID string) (ChecksObservation, error) {
	observation := ChecksObservation{RepositoryID: repositoryID, CommitOID: commitOID, ObservedAt: o.now().UTC()}
	repository, err := o.Repository(ctx, repositoryID)
	if err != nil {
		observation.Unknown, observation.UnknownReason = true, repository.UnknownReason
		return observation, fmt.Errorf("%w: repository identity is not established", ErrUnknown)
	}
	observation.ActorID = repository.ActorID
	result := o.execute(ctx, "api",
		fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repository.FullName, commitOID),
		"--jq", `{total: .total_count, runs: [.check_runs[] | {status: .status, conclusion: (.conclusion // "")}]}`)
	if result.err != nil || result.exitCode != 0 {
		observation.Unknown, observation.UnknownReason = true, UnknownUnavailable
		return observation, fmt.Errorf("%w: check query failed", ErrUnknown)
	}
	var payload struct {
		Total int `json:"total"`
		Runs  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.stdout)), &payload); err != nil {
		observation.Unknown, observation.UnknownReason = true, UnknownMalformed
		return observation, fmt.Errorf("%w: check response is malformed", ErrUnknown)
	}
	observation.Total = payload.Total
	failed, pending := 0, 0
	for _, run := range payload.Runs {
		switch run.Status {
		case "completed":
			switch run.Conclusion {
			case "success", "neutral", "skipped":
			default:
				failed++
			}
		default:
			pending++
		}
	}
	observation.Failed, observation.Pending = failed, pending
	switch {
	case payload.Total == 0:
		observation.State = "none"
	case pending > 0:
		observation.State = "pending"
	case failed > 0:
		observation.State = "failed"
	default:
		observation.State = "passed"
	}
	return observation, nil
}

// Permission reads GitHub's own permission fact for the acting identity.
func (o *GHObserver) Permission(ctx context.Context, repositoryID string) (PermissionObservation, error) {
	observation := PermissionObservation{RepositoryID: repositoryID, ObservedAt: o.now().UTC()}
	repository, err := o.Repository(ctx, repositoryID)
	if err != nil {
		observation.Unknown, observation.UnknownReason = true, repository.UnknownReason
		return observation, fmt.Errorf("%w: repository identity is not established", ErrUnknown)
	}
	observation.ActorID = repository.ActorID
	result := o.execute(ctx, "api", fmt.Sprintf("repos/%s", repository.FullName),
		"--jq", `{push: (.permissions.push // false), admin: (.permissions.admin // false)}`)
	if result.err != nil || result.exitCode != 0 {
		observation.Unknown, observation.UnknownReason = true, UnknownUnavailable
		return observation, fmt.Errorf("%w: permission query failed", ErrUnknown)
	}
	var payload struct {
		Push  bool `json:"push"`
		Admin bool `json:"admin"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.stdout)), &payload); err != nil {
		observation.Unknown, observation.UnknownReason = true, UnknownMalformed
		return observation, fmt.Errorf("%w: permission response is malformed", ErrUnknown)
	}
	observation.Push, observation.Admin = payload.Push, payload.Admin
	return observation, nil
}

const pullRequestJQ = `{number: (.number|tostring), author: ((.user.id // 0)|tostring), head: (.head.sha // ""), head_ref: (.head.ref // ""), base_ref: (.base.ref // ""), state: (.state // ""), merged: (.merged_at != null), merge_commit: (.merge_commit_sha // ""), merged_by: (((.merged_by // {}).id // 0)|tostring), merged_at: (.merged_at // "")}`

func decodePullRequest(repositoryID, number, actorID, raw string, observation *PullRequestObservation) error {
	var payload struct {
		Number      string `json:"number"`
		Author      string `json:"author"`
		Head        string `json:"head"`
		HeadRef     string `json:"head_ref"`
		BaseRef     string `json:"base_ref"`
		State       string `json:"state"`
		Merged      bool   `json:"merged"`
		MergeCommit string `json:"merge_commit"`
		MergedBy    string `json:"merged_by"`
		MergedAt    string `json:"merged_at"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Errorf("pull request payload is malformed")
	}
	if payload.Number == "" || !oidPattern.MatchString(payload.Head) || !repoPattern.MatchString(payload.Author) {
		return fmt.Errorf("pull request payload is incomplete")
	}
	if number != "" && payload.Number != number {
		return fmt.Errorf("pull request number mismatch")
	}
	if payload.Merged && !oidPattern.MatchString(payload.MergeCommit) {
		return fmt.Errorf("merged pull request has no merge commit")
	}
	if payload.Merged && !repoPattern.MatchString(payload.MergedBy) {
		return fmt.Errorf("merged pull request has no merged by identity")
	}
	observation.Number = payload.Number
	observation.AuthorID = payload.Author
	observation.HeadOID = payload.Head
	observation.HeadRef = payload.HeadRef
	observation.BaseRef = payload.BaseRef
	observation.State = payload.State
	observation.Merged = payload.Merged
	observation.MergeCommitOID = payload.MergeCommit
	observation.MergedByID = payload.MergedBy
	if payload.MergedAt != "" {
		mergedAt, err := time.Parse(time.RFC3339, payload.MergedAt)
		if err != nil {
			return fmt.Errorf("merge timestamp is malformed")
		}
		observation.MergedAt = mergedAt.UTC()
	}
	if observation.RepositoryID == "" {
		observation.RepositoryID = repositoryID
	}
	if observation.ActorID == "" {
		observation.ActorID = actorID
	}
	return nil
}
