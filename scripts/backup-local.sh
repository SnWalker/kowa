#!/usr/bin/env bash
# Takes one local Kowa backup: PostgreSQL, the artifact store, the Runner state
# and a names-only configuration snapshot, plus the manifest that proves what was
# copied.
#
# The backup is only consistent if the writers are stopped first. This script
# does not stop them: it requires the caller to state the consistency point, so a
# backup taken from a live control plane cannot be labelled as quiesced.
#
#   usage: backup-local.sh --dir <backup-dir> --consistency-point <text> [--tool name=version]... [--note <text>]...
#
# environment:
#   KOWA_BACKUP_POSTGRES_CONTAINER  container holding the database (preferred)
#   KOWA_BACKUP_DATABASE_URL        or a connection string for a local psql
#   KOWA_MATERIALIZE_ROOT           the control plane's rebuildable state root
#   KOWA_RUNNER_WORK_ROOT           the Runner's work root
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
backup_dir=""
consistency_point=""
tools=()
notes=()

die() {
  printf 'backup-local: %s\n' "$1" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir) backup_dir="${2:-}"; shift 2 ;;
    --consistency-point) consistency_point="${2:-}"; shift 2 ;;
    --tool) tools+=("${2:-}"); shift 2 ;;
    --note) notes+=("${2:-}"); shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done
[[ -n "$backup_dir" ]] || die "--dir is required"
[[ -n "$consistency_point" ]] || die "--consistency-point is required: state when the writers were stopped"
[[ -n "${KOWA_MATERIALIZE_ROOT:-}" ]] || die "KOWA_MATERIALIZE_ROOT is required"

container="${KOWA_BACKUP_POSTGRES_CONTAINER:-}"
database_url="${KOWA_BACKUP_DATABASE_URL:-}"
[[ -n "$container" || -n "$database_url" ]] || die "set KOWA_BACKUP_POSTGRES_CONTAINER or KOWA_BACKUP_DATABASE_URL"

mkdir -p "$backup_dir/postgres" "$backup_dir/artifacts" "$backup_dir/runner-state" "$backup_dir/config"
for component in postgres artifacts runner-state config; do
  if [[ -n "$(find "$backup_dir/$component" -mindepth 1 -print -quit)" ]]; then
    die "the backup directory already holds a backup; choose an empty directory"
  fi
done

# PostgreSQL. The dump is taken with the writers stopped, so it is one
# consistent snapshot rather than a torn one.
if [[ -n "$container" ]]; then
  docker exec "$container" pg_dump --username kowa_test --dbname kowa_test \
    --no-owner --no-privileges > "$backup_dir/postgres/database.pg_dump"
else
  pg_dump --dbname "$database_url" --no-owner --no-privileges > "$backup_dir/postgres/database.pg_dump"
fi
[[ -s "$backup_dir/postgres/database.pg_dump" ]] || die "the database dump is empty"

# The artifact store and the Runner state are copied as files.
if [[ -d "$KOWA_MATERIALIZE_ROOT/artifacts" ]]; then
  cp -R "$KOWA_MATERIALIZE_ROOT/artifacts/." "$backup_dir/artifacts/"
fi
if [[ -n "${KOWA_RUNNER_WORK_ROOT:-}" && -d "$KOWA_RUNNER_WORK_ROOT" ]]; then
  cp -R "$KOWA_RUNNER_WORK_ROOT/." "$backup_dir/runner-state/"
fi

# Configuration is recorded by name only. A value is never copied: a backup that
# carries a live token is a credential leak with a retention policy.
{
  printf 'recordedAt=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'note=names only; no configuration value is backed up\n'
  env | cut -d= -f1 | grep -E '^KOWA_[A-Z0-9_]+$' | LC_ALL=C sort
} > "$backup_dir/config/config.snapshot"

tool_arguments=()
for tool in "${tools[@]}"; do
  tool_arguments+=(--tool "$tool")
done
note_arguments=()
for note in "${notes[@]}"; do
  note_arguments+=(--note "$note")
done

postgres_version=""
if [[ -n "$container" ]]; then
  postgres_version="$(docker exec "$container" psql --username kowa_test --dbname kowa_test \
    --tuples-only --no-align --command "select current_setting('server_version');")"
else
  postgres_version="$(psql --dbname "$database_url" --tuples-only --no-align \
    --command "select current_setting('server_version');")"
fi

backup_id="backup-$(date -u +%Y%m%d-%H%M%S)"
"$repo_root/scripts/backup-manifest.sh" manifest \
  --dir "$backup_dir" --id "$backup_id" --consistency-point "$consistency_point" \
  --tool "postgres=$postgres_version" "${tool_arguments[@]}" "${note_arguments[@]}"

printf 'BACKUP_LOCAL_OK dir=%s backupId=%s\n' "$backup_dir" "$backup_id"
