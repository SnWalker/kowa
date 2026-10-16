// secret-scan: allow-file — this file must contain synthetic credentials to prove the seam refuses them.

package security

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/githubapp"
	"github.com/SnWalker/kowa/internal/observability"
	"github.com/SnWalker/kowa/internal/platform/logging"
)

// TestTheInstallationSeamKeepsItsCredentialsInside proves the backend:S08
// GitHub App seam cannot turn a failed observation into a leaked credential:
// the App private key, the signed assertion and the minted installation token
// never appear in the refusal or in the process log that records it.
func TestTheInstallationSeamKeepsItsCredentialsInside(t *testing.T) {
	const fakeInstallationToken = "ghs_syntheticinstallationtokenvalue"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "app-key.pem")
	keyBlock := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, keyBlock, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/access_tokens") {
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"token":"` + fakeInstallationToken + `","expires_at":"2030-01-01T00:00:00Z"}`))
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer api.Close()

	visibility, err := githubapp.NewInstallationVisibility(githubapp.Config{
		AppID:          "20260101",
		PrivateKeyPath: keyPath,
		APIBaseURL:     api.URL,
	})
	if err != nil {
		t.Fatalf("new installation visibility: %v", err)
	}
	refusal := visibility.RepositoryVisible(context.Background(), "77", "1001")
	if refusal == nil {
		t.Fatal("a failed installation read was accepted as visibility")
	}

	var logBuffer strings.Builder
	logging.NewWithWriter(&logBuffer).Error("repository binding refused", "error", refusal)

	surfaces := map[string]string{
		"the refusal":     refusal.Error(),
		"the process log": logBuffer.String(),
	}
	for name, surface := range surfaces {
		for secretName, secret := range map[string]string{
			"the installation token": fakeInstallationToken,
			"the signed assertion":   "eyJ",
			"the private key":        strings.TrimSpace(strings.SplitN(string(keyBlock), "\n", 3)[1]),
		} {
			if strings.Contains(surface, secret) {
				t.Fatalf("%s carries %s", name, secretName)
			}
		}
	}
	// The refusal itself carries no credential, so the log has nothing to mask;
	// what matters is that the message an operator reads is still the fact —
	// the installation read failed — and not a paraphrase of the secret.
	if !strings.Contains(logBuffer.String(), "installation repositories refused") {
		t.Fatalf("the log lost the refusal fact: %s", logBuffer.String())
	}
}

// TestTheAppCredentialVocabularyIsCovered pins that the frozen credential
// vocabulary masks exactly the shapes this seam handles — the PEM key, the
// installation token and the App assertion — while leaving Kowa's own opaque
// identifiers and digests readable.
func TestTheAppCredentialVocabularyIsCovered(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAx7\n-----END RSA PRIVATE KEY-----"
	assertion := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiIyMDI2MDEwMSJ9.c2lnbmF0dXJlLXBsYWNlaG9sZGVy"
	token := "ghs_0123456789abcdefghijklmnopqrstuvwx"
	for name, input := range map[string]string{
		"pem key": key, "assertion": assertion, "installation token": token,
	} {
		masked := observability.Redact(input)
		if !strings.Contains(masked, observability.Mask) {
			t.Fatalf("%s shape is not masked: %q", name, masked)
		}
	}
	for _, kept := range []string{
		"refs/heads/task/kowa-run_0123456789abcdef",
		"sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b",
	} {
		if observability.Redact(kept) != kept {
			t.Fatalf("the vocabulary destroyed a non-credential: %q", kept)
		}
	}
}
