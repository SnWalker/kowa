#!/usr/bin/env bash
# Runs the kowa-backup command from the repository. It exists so the operating
# scripts do not each have to know how the tool is built.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
exec go run ./cmd/backup "$@"
