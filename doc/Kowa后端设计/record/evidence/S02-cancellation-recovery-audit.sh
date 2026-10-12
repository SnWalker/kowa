#!/usr/bin/env bash
# Audit only: overlays tests; creates and removes an isolated PG17.11 container.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$repo_root"
evidence="$repo_root/doc/Kowa后端设计/record/evidence"
task_tmp="$(mktemp -d)"
container_name="kowa-cancellation-audit-${$}-${RANDOM}"
cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -rf "$task_tmp"
}
trap cleanup EXIT
python3 - "$repo_root" "$evidence" "$task_tmp/overlay.json" <<'PY'
import json,sys
from pathlib import Path
root,evidence,out=map(Path,sys.argv[1:])
mapping={
 root/'internal/execution/executiontest/cancellation_audit.go': evidence/'S02-cancellation-recovery-audit.go.txt',
 root/'internal/execution/current_round_contract_test.go': evidence/'S02-cancellation-memory_test.go.txt',
 root/'internal/infra/postgres/current_round_integration_test.go': evidence/'S02-cancellation-postgres_test.go.txt',
}
out.write_text(json.dumps({'Replace':{str(k):str(v) for k,v in mapping.items()}}))
PY
mode="${1:-target}"
if [[ "$mode" == characterize ]]; then
  export KOWA_CANCELLATION_CHARACTERIZE=recovery
elif [[ "$mode" == target ]]; then
  unset KOWA_CANCELLATION_CHARACTERIZE
else
  printf 'usage: %s [characterize|target]\n' "$0" >&2
  exit 2
fi
set +e
go test -count=1 -json -overlay="$task_tmp/overlay.json" ./internal/execution -run '^TestMemoryStoreCancellationAudit$'
memory_exit=$?
set -e
image=postgres:17.11-alpine
docker run --detach --rm --name "$container_name" --publish 127.0.0.1::5432 \
 --env POSTGRES_DB=kowa_test --env POSTGRES_USER=kowa_test \
 --env POSTGRES_PASSWORD=kowa-integration-password "$image" >/dev/null
ready_count=0
for _ in {1..120}; do
 if docker exec "$container_name" pg_isready --host 127.0.0.1 --username kowa_test --dbname kowa_test >/dev/null 2>&1; then
  ready_count=$((ready_count + 1))
  if [[ "$ready_count" -ge 3 ]]; then break; fi
 else ready_count=0; fi
 sleep 0.25
done
test "$ready_count" -ge 3
printf 'CANCELLATION_AUDIT_ENV: container=%s image=%s\n' "$container_name" "$image"
docker exec "$container_name" psql --username kowa_test --dbname kowa_test --tuples-only --no-align \
 --command "select 'server_version=' || current_setting('server_version') || ',database=' || current_database();"
port="$(docker port "$container_name" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
for migration in "$repo_root"/db/migrations/*.up.sql; do
 docker exec --interactive "$container_name" psql --username kowa_test --dbname kowa_test --set ON_ERROR_STOP=1 < "$migration" >/dev/null
done
set +e
KOWA_TEST_DATABASE_URL="postgres://kowa_test:kowa-integration-password@127.0.0.1:${port}/kowa_test?sslmode=disable" \
 go test -count=1 -json -overlay="$task_tmp/overlay.json" -tags=integration ./internal/infra/postgres \
 -run '^TestWorkflowExecutionCancellationAuditPostgres$'
postgres_exit=$?
set -e
printf 'CANCELLATION_AUDIT_RESULT: mode=%s memory_exit=%d postgres_exit=%d\n' "$mode" "$memory_exit" "$postgres_exit"
if [[ "$memory_exit" -ne 0 || "$postgres_exit" -ne 0 ]]; then exit 1; fi
