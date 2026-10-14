package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/runnertransport"
)

// Frozen-input handling outcomes. A verification failure must never execute a
// worker; an availability failure may be retried inside the attempt deadline.
var (
	errInputInvalid     = errors.New("runner: frozen input failed verification")
	errInputUnavailable = errors.New("runner: frozen input is unavailable")
)

// inputFetchBackoff bounds the retry cadence while the control plane cannot yet
// serve the binding (run admission may still be persisting it).
const (
	inputFetchBackoffMin = 500 * time.Millisecond
	inputFetchBackoffMax = 5 * time.Second
)

// fetchFrozenInput obtains, verifies and materialises the task's frozen input
// into the attempt workspace. The bundle bytes are stored verbatim, every
// referenced artifact is verified against its frozen reference, and a manifest
// records exactly what was accepted. Any verification failure returns
// errInputInvalid and the attempt must not execute.
func (l *Loop) fetchFrozenInput(
	ctx context.Context,
	task execution.TaskSpec,
	lease execution.TaskLease,
	workspace string,
) (string, error) {
	deadline := task.DeadlineAt.UTC()
	if leaseGuard := lease.ExpiresAt.UTC().Add(-l.config.ReportMargin); leaseGuard.Before(deadline) {
		deadline = leaseGuard
	}
	fetchContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	backoff := inputFetchBackoffMin
	for {
		manifest, err := l.client.FetchInput(fetchContext, task.ID)
		if err != nil {
			if !retryableInputError(err) {
				return "", err
			}
			if !sleepContext(fetchContext, jitter(backoff)) {
				return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
			}
			backoff = growBackoff(backoff, inputFetchBackoffMax)
			continue
		}
		return l.materializeInput(ctx, task, workspace, manifest)
	}
}

// retryableInputError classifies transport failures: everything that is not a
// permanent authorization/lease settlement is retried until the attempt
// deadline; settleable and supersession errors propagate to the caller.
func retryableInputError(err error) bool {
	switch {
	case errors.Is(err, runnertransport.ErrNotOwner),
		errors.Is(err, runnertransport.ErrStale),
		errors.Is(err, runnertransport.ErrConflict),
		errors.Is(err, runnertransport.ErrSuperseded),
		errors.Is(err, runnertransport.ErrForbidden),
		errors.Is(err, runnertransport.ErrUnauthorized),
		errors.Is(err, runnertransport.ErrProtocol):
		return false
	default:
		return true
	}
}

func (l *Loop) materializeInput(
	ctx context.Context,
	task execution.TaskSpec,
	workspace string,
	manifest runnertransport.InputManifestResponse,
) (string, error) {
	digest := artifact.Digest(manifest.Bundle)
	if digest != manifest.BundleDigest || digest != task.InputDigest {
		return "", fmt.Errorf("%w: bundle digest %s does not match the frozen input %s",
			errInputInvalid, digest, task.InputDigest)
	}
	inputDirectory := filepath.Join(workspace, "input")
	if err := os.MkdirAll(filepath.Join(inputDirectory, "artifacts"), 0o750); err != nil {
		return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
	}
	bundlePath := filepath.Join(inputDirectory, "bundle.json")
	if err := writeAtomic(bundlePath, manifest.Bundle); err != nil {
		return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
	}
	type verifiedArtifact struct {
		ArtifactID string `json:"artifactId"`
		Digest     string `json:"digest"`
		Size       int64  `json:"size"`
		MediaType  string `json:"mediaType"`
	}
	verified := make([]verifiedArtifact, 0, len(manifest.Artifacts))
	for _, reference := range manifest.Artifacts {
		if err := reference.Validate(); err != nil {
			return "", fmt.Errorf("%w: invalid artifact reference: %v", errInputInvalid, err)
		}
		data, err := l.client.FetchInputArtifact(ctx, task.ID, reference.ArtifactID)
		if err != nil {
			if !retryableInputError(err) {
				return "", err
			}
			return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
		}
		if int64(len(data)) != reference.Size || artifact.Digest(data) != reference.Digest {
			return "", fmt.Errorf("%w: artifact %s does not match its frozen reference",
				errInputInvalid, reference.ArtifactID)
		}
		if err := writeAtomic(filepath.Join(inputDirectory, "artifacts", reference.ArtifactID), data); err != nil {
			return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
		}
		verified = append(verified, verifiedArtifact{
			ArtifactID: reference.ArtifactID, Digest: reference.Digest,
			Size: reference.Size, MediaType: reference.MediaType,
		})
	}
	manifestRecord := struct {
		BundleDigest string             `json:"bundleDigest"`
		Artifacts    []verifiedArtifact `json:"artifacts"`
		VerifiedAt   time.Time          `json:"verifiedAt"`
	}{BundleDigest: digest, Artifacts: verified, VerifiedAt: l.now().UTC()}
	encoded, err := json.MarshalIndent(manifestRecord, "", "  ")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
	}
	if err := writeAtomic(filepath.Join(inputDirectory, "manifest.json"), append(encoded, '\n')); err != nil {
		return "", fmt.Errorf("%w: %v", errInputUnavailable, err)
	}
	l.logger.Info("frozen input delivered", "task", task.ID,
		"bundleBytes", len(manifest.Bundle), "artifacts", len(verified))
	return bundlePath, nil
}
