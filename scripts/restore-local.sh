#!/usr/bin/env bash
# Restores one local Kowa backup.
#
# The order is the rule: the manifest is verified, every component is re-hashed
# and only then is anything applied. A component that no longer matches its
# manifest stops the restore with that component named, so a partially copied or
# tampered backup is never applied as if it were whole.
#
#   usage: restore-local.sh --dir <backup-dir> [--dump-dir <dir>]
#
# environment:
#   KOWA_RESTORE_POSTGRES_CONTAINER  container to apply the dump with (preferred)
#   KOWA_RESTORE_DATABASE_URL        or a connection string for a local psql
#   KOWA_MATERIALIZE_ROOT            where the artifact store is restored to
#   KOWA_RUNNER_WORK_ROOT            where the Runner state is restored to
#
# Nothing is deleted on the destination side. A restore never removes evidence a
# run or a closure still refers to; clearing a destination is the operator's
# explicit decision, taken before this script runs.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
backup_dir=""
dump_dir=""

die() {
  printf 'restore-local: %s\n' "$1" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir) backup_dir="${2:-}"; shift 2 ;;
    --dump-dir) dump_dir="${2:-}"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done
[[ -n "$backup_dir" ]] || die "--dir is required"
[[ -d "$backup_dir" ]] || die "the backup directory does not exist: $backup_dir"

started_at="$(date -u +%s)"
"$repo_root/scripts/backup-manifest.sh" verify --dir "$backup_dir"

artifact_root="${KOWA_MATERIALIZE_ROOT:-}/artifacts"
[[ -n "${KOWA_MATERIALIZE_ROOT:-}" ]] || die "KOWA_MATERIALIZE_ROOT is required"
runner_root="${KOWA_RUNNER_WORK_ROOT:-${backup_dir}.runner-state-restored}"
[[ -n "$dump_dir" ]] || dump_dir="$(mktemp -d)"

# The file components are applied by the kowa-backup tool itself, so the restored
# tree is hashed after the copy and compared with the manifest.
# Each component is restored into its own directory: the restored tree is hashed
# after the copy, so a destination that also holds another component would be
# refused rather than silently mixed.
"$repo_root/scripts/backup-manifest.sh" restore \
  --dir "$backup_dir" \
  --to-postgres "$dump_dir/postgres" \
  --to-artifacts "$artifact_root" \
  --to-runner-state "$runner_root" \
  --to-config "$dump_dir/config"

# Only now is the database touched.
container="${KOWA_RESTORE_POSTGRES_CONTAINER:-}"
database_url="${KOWA_RESTORE_DATABASE_URL:-}"
if [[ -n "$container" ]]; then
  docker exec --interactive "$container" psql --username kowa_test --dbname kowa_test \
    --set ON_ERROR_STOP=1 --quiet < "$dump_dir/postgres/database.pg_dump" >/dev/null
elif [[ -n "$database_url" ]]; then
  psql --dbname "$database_url" --set ON_ERROR_STOP=1 --quiet \
    < "$dump_dir/postgres/database.pg_dump" >/dev/null
else
  die "set KOWA_RESTORE_POSTGRES_CONTAINER or KOWA_RESTORE_DATABASE_URL"
fi

elapsed="$(( $(date -u +%s) - started_at ))"
printf 'RESTORE_LOCAL_OK dir=%s elapsedSeconds=%s artifacts=%s runnerState=%s\n' \
  "$backup_dir" "$elapsed" "$artifact_root" "$runner_root"
