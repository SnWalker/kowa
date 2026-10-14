package server

// The composition-root adapters must forward every fact the closing checklist
// reads; a hand-built fixture cannot catch a dropped field, so this exercises
// the real adapter over a fake observation seam.
import (
	"context"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/githubobs"
)

type stubDeliveryObserver struct {
	repository  githubobs.RepositoryObservation
	pullRequest githubobs.PullRequestObservation
	reviews     []githubobs.ReviewObservation
	checks      githubobs.ChecksObservation
	permission  string
}

func (s stubDeliveryObserver) Identity(context.Context) (string, error) { return "135722937", nil }

func (s stubDeliveryObserver) Repository(context.Context, string) (githubobs.RepositoryObservation, error) {
	return s.repository, nil
}

func (s stubDeliveryObserver) BranchHead(context.Context, string, string) (githubobs.RefObservation, error) {
	return githubobs.RefObservation{}, nil
}

func (s stubDeliveryObserver) PullRequest(context.Context, string, string) (githubobs.PullRequestObservation, error) {
	return s.pullRequest, nil
}

func (s stubDeliveryObserver) PullRequestsByHead(context.Context, string, string) ([]githubobs.PullRequestObservation, error) {
	return []githubobs.PullRequestObservation{s.pullRequest}, nil
}

func (s stubDeliveryObserver) Reviews(context.Context, string, string) ([]githubobs.ReviewObservation, error) {
	return s.reviews, nil
}

func (s stubDeliveryObserver) ReviewerPermission(context.Context, string, string, string) (string, error) {
	return s.permission, nil
}

func (s stubDeliveryObserver) Checks(context.Context, string, string) (githubobs.ChecksObservation, error) {
	return s.checks, nil
}

func (s stubDeliveryObserver) Permission(context.Context, string) (githubobs.PermissionObservation, error) {
	return githubobs.PermissionObservation{RepositoryID: "1401511579", Push: true, ObservedAt: time.Now().UTC()}, nil
}

// TestDeliveryFactsNormalizeGitHubBranchNames feeds the real decoder's output
// (from a GitHub-shaped payload whose base.ref is a bare branch name) through
// the real adapter, so a publish_pr reconciliation comparison cannot fail on
// the ref grammar.
func TestDeliveryFactsNormalizeGitHubBranchNames(t *testing.T) {
	decoded, err := githubobs.DecodePullRequest("1401511579", "42", "135722937", `{
		"number": "42", "author": "135722937", "head": "de62a4324cad57908376f8c5f3e14bd3bd3dab95",
		"head_ref": "task/s05", "base_ref": "main", "state": "open", "merged": false,
		"merge_commit": "", "merged_by": "0", "merged_by_type": "", "merged_at": ""
	}`)
	if err != nil {
		t.Fatalf("DecodePullRequest() error = %v", err)
	}
	observer := stubDeliveryObserver{
		pullRequest: decoded,
		checks:      githubobs.ChecksObservation{CommitOID: decoded.HeadOID, State: "passed", ObservedAt: time.Now().UTC()},
	}
	projection, err := githubDeliveryFacts{observer}.PullRequest(context.Background(), "1401511579", "42")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if projection.PullRequest.BaseRef != "refs/heads/main" {
		t.Fatalf("the projection lost the frozen base ref grammar: %#v", projection.PullRequest.BaseRef)
	}
}

func TestDeliveryFactsForwardEveryChecklistField(t *testing.T) {
	head := "de62a4324cad57908376f8c5f3e14bd3bd3dab95"
	observer := stubDeliveryObserver{
		pullRequest: githubobs.PullRequestObservation{
			RepositoryID: "1401511579", Number: "42", AuthorID: "135722937", HeadOID: head,
			HeadRef: "task/s05", BaseRef: "main", State: "closed", Merged: true,
			MergeCommitOID: "ca6c6ec8446ec687213d58064a7fd8363050321a",
			MergedByID:     "277460407", MergedByType: "User", MergedAt: time.Now().UTC(),
			ObservedAt: time.Now().UTC(), ActorID: "135722937",
		},
		reviews: []githubobs.ReviewObservation{{
			RepositoryID: "1401511579", Number: "42", ReviewID: "5001", ReviewerID: "277460407",
			ReviewerLogin: "reviewer-a", ReviewerType: "User", AuthorAssociation: "MEMBER",
			State: "APPROVED", CommitOID: head, SubmittedAt: time.Now().UTC(),
			ObservedAt: time.Now().UTC(), ActorID: "135722937",
		}},
		checks:     githubobs.ChecksObservation{CommitOID: head, State: "passed", Total: 1, ObservedAt: time.Now().UTC()},
		permission: "write",
	}
	facts := githubDeliveryFacts{observer}
	projection, err := facts.PullRequest(context.Background(), "1401511579", "42")
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if projection.PullRequest.MergedByID != "277460407" || projection.PullRequest.MergedByType != "User" {
		t.Fatalf("the merge identity lost its account type: %#v", projection.PullRequest)
	}
	if len(projection.Reviews) != 1 {
		t.Fatalf("reviews = %#v", projection.Reviews)
	}
	review := projection.Reviews[0]
	if review.ReviewerLogin != "reviewer-a" || review.ReviewerType != "User" ||
		review.ReviewerPermission != "write" || !review.AuthorizedReviewer() {
		t.Fatalf("the review fact lost its authorization fields: %#v", review)
	}
	byHead, err := facts.PullRequestsByHead(context.Background(), "1401511579", "task/s05")
	if err != nil || len(byHead) != 1 {
		t.Fatalf("PullRequestsByHead() = %#v, %v", byHead, err)
	}
	if byHead[0].PullRequest.MergedByType != "User" {
		t.Fatalf("the by-head projection lost the merge account type: %#v", byHead[0].PullRequest)
	}
}
