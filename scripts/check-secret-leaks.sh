#!/usr/bin/env bash
# Scans the working tree for credential shapes: every tracked file plus every
# untracked file that is not ignored, so a fixture cannot pass the gate by not
# being committed yet.
#
# Every exception is an explicit, reviewable path: a test that must contain a
# synthetic credential to prove the scrubber works. Each listed file must carry
# the `secret-scan: allow-file` marker in its header, so a path cannot silently
# stay on the list after the fixture is gone, and the scan prints every exception
# it applied.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Synthetic-credential fixtures. The marker check below keeps this list honest.
allow_files=(
  "internal/platform/logging/logger_redaction_test.go"
  "internal/observability/observability_test.go"
  "internal/evaluation/backend/recorder_test.go"
  "internal/evaluation/langfuse/client_test.go"
  "test/security/observation_boundary_test.go"
  "test/security/worker_environment_test.go"
  "test/security/installation_seam_test.go"
  "api/observability/v1/examples/negative-schema-secret-field.json"
  "scripts/test-recovery-drill.sh"
  "doc/Kowa后端设计/record/evidence/S07-target-red-log-credential.txt"
)

# Files that define the credential shapes themselves. They contain no
# credential; their text is the rule that finds them.
definition_files=(
  "internal/observability/rules.go"
  "scripts/check-secret-leaks.sh"
)

credential_pattern='gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|(pk|sk)-lf-[A-Za-z0-9]{8,}|(^|[^A-Za-z0-9-])sk-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----'

is_allowed() {
  local candidate="$1"
  local allowed
  for allowed in "${allow_files[@]}"; do
    # shellcheck disable=SC2076
    if [[ "$candidate" == "$allowed" ]]; then
      return 0
    fi
  done
  return 1
}

is_definition() {
  local candidate="$1"
  local defined
  for defined in "${definition_files[@]}"; do
    # shellcheck disable=SC2076
    if [[ "$candidate" == "$defined" ]]; then
      return 0
    fi
  done
  return 1
}

marker="secret-scan: allow-file"
failures=0
allowed_applied=0
scanned=0
skipped=0
definitions=0

while IFS= read -r -d '' path; do
  case "$path" in
    *.png|*.jpg|*.jpeg|*.pdf|*.ico|*.woff|*.woff2) continue ;;
    # Third-party skill material that documents the credential shapes it is
    # about; it is not Kowa code and is not shipped by Kowa.
    .agents/skills/*) skipped=$((skipped + 1)); continue ;;
  esac
  scanned=$((scanned + 1))
  if ! grep -Iq . "$path" 2>/dev/null; then
    continue
  fi
  if is_definition "$path"; then
    definitions=$((definitions + 1))
    continue
  fi
  allowed="no"
  if is_allowed "$path"; then
    if head -n 12 "$path" | grep -qF "$marker"; then
      allowed="yes"
      allowed_applied=$((allowed_applied + 1))
    else
      printf '%s: allowlisted but missing the %q marker\n' "$path" "$marker" >&2
      failures=$((failures + 1))
      continue
    fi
  fi
  if matches="$(grep -nE "$credential_pattern" "$path" || true)"; then
    if [[ -n "$matches" ]]; then
      if [[ "$allowed" == "yes" ]]; then
        printf 'secret-scan allowance applied: %s\n' "$path"
      else
        printf '%s\n' "$matches" | while IFS= read -r line; do
          printf '%s: credential shape found: %s\n' "$path" "${line%%:*}" >&2
        done
        failures=$((failures + 1))
      fi
    fi
  fi
done < <(git ls-files --cached --others --exclude-standard -z)

if [[ "$scanned" -lt 100 ]]; then
  printf 'only %d tracked files were scanned, so the gate proves little\n' "$scanned" >&2
  exit 1
fi
if [[ "$failures" -ne 0 ]]; then
  printf 'SECRET_SCAN_FAIL: %d file(s) carry a credential shape\n' "$failures" >&2
  exit 1
fi
printf 'SECRET_SCAN_PASS files=%d allowances=%d pattern-definitions=%d skipped-third-party=%d\n' "$scanned" "$allowed_applied" "$definitions" "$skipped"
