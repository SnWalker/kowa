#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container_name="kowa-s01-postgres-${$}-${RANDOM}"
postgres_password="kowa-integration-password"
postgres_image="${KOWA_POSTGRES_IMAGE:-postgres:17.11-alpine}"
# Each package owns its database: the store suite injects audit failures with a
# process-global `alter table audit_event rename`, which PostgreSQL exposes to
# every session on that database and which would break the server suite
# (SQLSTATE 42P01 -> HTTP 500) while both packages run concurrently below.
store_database="kowa_test_store"
server_database="kowa_test_server"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --detach --rm \
  --name "$container_name" \
  --publish 127.0.0.1::5432 \
  --env POSTGRES_DB="$store_database" \
  --env POSTGRES_USER=kowa_test \
  --env POSTGRES_PASSWORD="$postgres_password" \
  "$postgres_image" >/dev/null

ready_count=0
for _ in {1..120}; do
  if docker exec "$container_name" pg_isready --host 127.0.0.1 --username kowa_test --dbname "$store_database" >/dev/null 2>&1; then
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
docker exec "$container_name" pg_isready --host 127.0.0.1 --username kowa_test --dbname "$store_database" >/dev/null
docker exec "$container_name" \
  psql --username kowa_test --dbname "$store_database" --set ON_ERROR_STOP=1 \
  --command "create database ${server_database} owner kowa_test" >/dev/null
host_port="$(docker port "$container_name" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
store_url="postgres://kowa_test:${postgres_password}@127.0.0.1:${host_port}/${store_database}?sslmode=disable"
server_url="postgres://kowa_test:${postgres_password}@127.0.0.1:${host_port}/${server_database}?sslmode=disable"

migrate_up() {
  docker exec --interactive "$container_name" \
    psql --username kowa_test --dbname "$1" --set ON_ERROR_STOP=1 \
    < "$repo_root/db/migrations/000001_identity_workspace.up.sql" >/dev/null
}
migrate_down() {
  docker exec --interactive "$container_name" \
    psql --username kowa_test --dbname "$1" --set ON_ERROR_STOP=1 \
    < "$repo_root/db/migrations/000001_identity_workspace.down.sql" >/dev/null
}
migrate_up "$store_database"
migrate_up "$server_database"

now_ms() {
  python3 -c 'import time; print(int(time.time() * 1000))'
}

# Both suites stay concurrent on purpose; isolation comes from the separate
# databases, not from serializing the packages.
store_start="$(now_ms)"
KOWA_TEST_DATABASE_URL="$store_url" \
  go test -count=1 -tags=integration ./internal/infra/postgres -run '^TestIdentityWorkspacePostgres$' &
store_pid="$!"
server_start="$(now_ms)"
KOWA_TEST_DATABASE_URL="$server_url" \
  go test -count=1 -tags=integration ./internal/server -run '^TestIdentityWorkspaceServerPostgres$' &
server_pid="$!"

status=0
wait "$store_pid" || status=1
store_end="$(now_ms)"
wait "$server_pid" || status=1
server_end="$(now_ms)"
test "$status" -eq 0

overlap_start="$(printf '%s\n' "$store_start" "$server_start" | sort -n | tail -1)"
overlap_end="$(printf '%s\n' "$store_end" "$server_end" | sort -n | head -1)"
overlap_ms="$((overlap_end - overlap_start))"
if [[ "$overlap_ms" -le 0 ]]; then
  printf 'identity/workspace suites did not overlap: store=[%s,%s] server=[%s,%s]\n' \
    "$store_start" "$store_end" "$server_start" "$server_end" >&2
  exit 1
fi
printf 'IDENTITY_WORKSPACE_INTEGRATION_OVERLAP_MS=%s\n' "$overlap_ms"

migrate_down "$store_database"
migrate_down "$server_database"

for database in "$store_database" "$server_database"; do
  remaining_tables="$(docker exec "$container_name" psql \
    --username kowa_test --dbname "$database" --tuples-only --no-align \
    --command "select count(*) from pg_tables where schemaname = 'public';")"
  test "$remaining_tables" = "0"
done

printf 'IDENTITY_WORKSPACE_INTEGRATION_PASS\n'
