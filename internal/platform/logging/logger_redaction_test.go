// secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.

package logging_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/platform/logging"
)

// The three shapes below are the ones the quality design names explicitly:
// an OAuth client secret, a member/user token and a Runner credential. None of
// them may reach a log record, whichever field carries them.
const (
	clientSecret = "kowa-client-secret-9f8e7d6c5b4a"
	memberToken  = "gho_16C7e42F292c6912E7710c838347Ae178B4a"
	runnerToken  = "kowa-runner-token-2b8f0c1d4e6a"
	privateKey   = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ==\n-----END OPENSSH PRIVATE KEY-----"
)

// TestProcessLoggerNeverWritesACredential is the S07 target red: it fails while
// the process logger writes whatever it is given.
func TestProcessLoggerNeverWritesACredential(t *testing.T) {
	secretCases := []struct {
		name        string
		message     string
		attribute   string
		attributeIn string
	}{
		{
			name:        "a credential inside the message",
			message:     "github exchange failed for client_secret=" + clientSecret,
			attribute:   "detail",
			attributeIn: "safe",
		},
		{
			name:        "a member token passed as an attribute",
			message:     "session refresh failed",
			attribute:   "accessToken",
			attributeIn: memberToken,
		},
		{
			name:        "a runner credential in the authorization header field",
			message:     "runtime transport rejected the caller",
			attribute:   "authorization",
			attributeIn: "Bearer " + runnerToken,
		},
		{
			name:        "a private key inside an error value",
			message:     "repo materialization failed",
			attribute:   "error",
			attributeIn: privateKey,
		},
	}
	for _, testCase := range secretCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buffer bytes.Buffer
			logger := logging.NewWithWriter(&buffer)
			logger.Error(testCase.message, testCase.attribute, testCase.attributeIn)
			written := buffer.String()
			for _, secret := range []string{clientSecret, memberToken, runnerToken, "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ=="} {
				if strings.Contains(written, secret) {
					t.Fatalf("an unscrubbed credential reached the log: %s", written)
				}
			}
			if !strings.Contains(written, `"level":"ERROR"`) {
				t.Fatalf("the record lost its structured fields: %s", written)
			}
		})
	}
}

// TestProcessLoggerKeepsEvidenceReadable is the guard on the other side: the
// scrubber must not make digests, ids or refs unreadable, because the log is how
// an operator matches a record to a frozen fact.
func TestProcessLoggerKeepsEvidenceReadable(t *testing.T) {
	var buffer bytes.Buffer
	logger := logging.NewWithWriter(&buffer)
	const digest = "sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b"
	const commit = "6d88be15052683e0807e7a363f33000565608066"
	logger.Info("run admitted",
		"definitionDigest", digest, "commitOid", commit,
		"runId", "run_0123456789abcdef", "requestKey", "req-intake-0001")
	written := buffer.String()
	for _, evidence := range []string{digest, commit, "run_0123456789abcdef", "req-intake-0001"} {
		if !strings.Contains(written, evidence) {
			t.Fatalf("the scrubber removed ordinary evidence %q: %s", evidence, written)
		}
	}
}

// TestProcessLoggerRendersAnErrorAsItsScrubbedMessage pins that an error value
// stays readable in the record instead of degrading to an empty object.
func TestProcessLoggerRendersAnErrorAsItsScrubbedMessage(t *testing.T) {
	var buffer bytes.Buffer
	logger := logging.NewWithWriter(&buffer)
	logger.Error("provider adapter failed", "error", errors.New("gh: could not read from https://tok:"+runnerToken+"@github.com/o/r.git"))
	written := buffer.String()
	if !strings.Contains(written, `"error":"gh: could not read from https://***@github.com/o/r.git"`) {
		t.Fatalf("the error field is not a scrubbed message: %s", written)
	}
	if !strings.Contains(written, `"level":"ERROR"`) || !strings.Contains(written, `"msg":"provider adapter failed"`) {
		t.Fatalf("the record lost its structured shape: %s", written)
	}
}
