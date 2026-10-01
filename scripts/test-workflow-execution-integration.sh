#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container_name="kowa-s02-postgres-${$}-${RANDOM}"
postgres_password="kowa-integration-password"
postgres_image="${KOWA_POSTGRES_IMAGE:-postgres:17.11-alpine}"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --detach --rm \
  --name "$container_name" \
  --publish 127.0.0.1::5432 \
  --env POSTGRES_DB=kowa_test \
  --env POSTGRES_USER=kowa_test \
  --env POSTGRES_PASSWORD="$postgres_password" \
  "$postgres_image" >/dev/null

ready_count=0
for _ in {1..120}; do
  if docker exec "$container_name" pg_isready --host 127.0.0.1 --username kowa_test --dbname kowa_test >/dev/null 2>&1; then
    ready_count="$((ready_count + 1))"
    if [[ "$ready_count" -ge 3 ]]; then
      break
    fi
  else
    ready_count=0
  fi
  sleep 0.25
done

test "$ready_count" -ge 3
host_port="$(docker port "$container_name" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
database_url="postgres://kowa_test:${postgres_password}@127.0.0.1:${host_port}/kowa_test?sslmode=disable"

for migration in "$repo_root"/db/migrations/*.up.sql; do
  docker exec --interactive "$container_name" \
    psql --username kowa_test --dbname kowa_test --set ON_ERROR_STOP=1 < "$migration" >/dev/null
done

KOWA_TEST_DATABASE_URL="$database_url" \
  go test -count=1 -json -tags=integration ./internal/infra/postgres -run '^(TestWorkflowExecutionPostgres|TestWorkflowExecutionLeaseRejectionPostgres|TestWorkflowExecutionRuntimeRegistrationPostgres)$'

for migration in $(find "$repo_root/db/migrations" -name '*.down.sql' -print | sort -r); do
  docker exec --interactive "$container_name" \
    psql --username kowa_test --dbname kowa_test --set ON_ERROR_STOP=1 < "$migration" >/dev/null
done

remaining_tables="$(docker exec "$container_name" psql \
  --username kowa_test --dbname kowa_test --tuples-only --no-align \
  --command "select count(*) from pg_tables where schemaname = 'public';")"
test "$remaining_tables" = "0"

printf 'WORKFLOW_EXECUTION_INTEGRATION_PASS\n'
