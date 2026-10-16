package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/SnWalker/kowa/internal/githubapp"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/workspace"
)

// TestTheVisibilityPortFailsClosedWithoutTheInstallationSeam pins the frozen
// default: a deployment that did not configure the GitHub App installation
// seam keeps the adapter that refuses every binding instead of granting one.
func TestTheVisibilityPortFailsClosedWithoutTheInstallationSeam(t *testing.T) {
	visibility, err := newRepositoryVisibility(config.Identity{})
	if err != nil {
		t.Fatalf("unconfigured visibility failed to assemble: %v", err)
	}
	if _, ok := visibility.(unavailableRepositoryVisibility); !ok {
		t.Fatalf("unconfigured visibility is %T, want the fail-closed adapter", visibility)
	}
	if reason := visibility.RepositoryVisible(context.Background(), "77", "1001"); reason == nil {
		t.Fatal("the fail-closed adapter granted visibility")
	}
}

// TestTheVisibilityPortUsesTheInstallationSeamWhenConfigured pins that the
// S01 port is served by the real GitHub App observation once the deployment
// supplies the App id and the key path — the port, its semantics and the
// workspace authority are unchanged.
func TestTheVisibilityPortUsesTheInstallationSeamWhenConfigured(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "app-key.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, block, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	visibility, err := newRepositoryVisibility(config.Identity{
		GitHubAppID:             "20260101",
		GitHubAppPrivateKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("configured visibility failed to assemble: %v", err)
	}
	if _, ok := visibility.(*githubapp.InstallationVisibility); !ok {
		t.Fatalf("configured visibility is %T, want the installation seam", visibility)
	}
	// The seam answers unknown facts with an error; the workspace authority
	// keeps mapping every error to RESOURCE_UNAVAILABLE.
	var port workspace.RepositoryVisibility = visibility
	if reason := port.RepositoryVisible(context.Background(), "not-decimal", "1001"); reason == nil {
		t.Fatal("a malformed identifier was accepted")
	}
}

// TestTheVisibilityPortRefusesAHalfConfiguredSeam pins that an operator
// mistake (id without a key path) is a startup error, not a silent fallback.
func TestTheVisibilityPortRefusesAHalfConfiguredSeam(t *testing.T) {
	if _, err := newRepositoryVisibility(config.Identity{GitHubAppID: "20260101"}); err == nil {
		t.Fatal("a half-configured installation seam was accepted")
	}
	if _, err := newRepositoryVisibility(config.Identity{
		GitHubAppID:             "20260101",
		GitHubAppPrivateKeyPath: filepath.Join(t.TempDir(), "absent.pem"),
	}); err == nil {
		t.Fatal("an unreadable key was accepted")
	}
}
