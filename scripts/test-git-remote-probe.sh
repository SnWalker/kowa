#!/usr/bin/env bash
# Standalone, read-only observation of the kowa.delivery-governance.v1 git remote
# projection. It verifies the active gh identity, reads the repository identity
# and GitHub's permission fact, and runs `git ls-remote` over the machine's own
# transports. It performs no GitHub write: no push, no PR, no review, no merge.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${KOWA_GITHUB_MACHINE_USER_ID:?set the expected machine GitHub user id}"
: "${KOWA_PROBE_PROJECT_REPOSITORY_ID:?set the project repository id}"
: "${KOWA_PROBE_KNOWLEDGE_REPOSITORY_ID:?set the knowledge repository id}"

cd "$repo_root"
go test -count=1 -tags gitremoteprobe -run '^TestGitRemoteRealProbe$' -v ./internal/server/
printf 'GIT_REMOTE_PROBE_PASS\n'
