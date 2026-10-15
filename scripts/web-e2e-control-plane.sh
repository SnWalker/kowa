#!/usr/bin/env bash
# Local acceptance for frontend:S01 at the promised level: the Playwright suite
# is driven against the real control-plane composition root (internal/server)
# with isolated external adapters, an isolated PostgreSQL and the production Web
# build served from the same HTTPS origin.
#
# It is deliberately not part of the frontend CI job (that job has no Docker or
# Go); the Web CI runs the same journeys in fixture mode. Real GitHub OAuth and
# App installation stay in backend:S08 and are not exercised here.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_address="${KOWA_HARNESS_ADDR:-127.0.0.1:8443}"
stub_address="${KOWA_HARNESS_STUB_ADDR:-127.0.0.1:8099}"
web_origin="https://$web_address"
stub_origin="http://$stub_address"
container_name="kowa-webharness-pg-${$}-${RANDOM}"
postgres_password="kowa-harness-password"
postgres_image="${KOWA_POSTGRES_IMAGE:-postgres:17.11-alpine}"
temp_dir="$(mktemp -d)"
harness_pid=""

cleanup() {
  if [[ -n "$harness_pid" ]] && kill -0 "$harness_pid" 2>/dev/null; then
    kill -INT "$harness_pid" 2>/dev/null || true
    wait "$harness_pid" 2>/dev/null || true
  fi
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -r -- "$temp_dir"
}
trap cleanup EXIT

docker run --detach --rm \
  --name "$container_name" \
  --publish 127.0.0.1::5432 \
  --env POSTGRES_DB=kowa_webharness \
  --env POSTGRES_USER=kowa_harness \
  --env POSTGRES_PASSWORD="$postgres_password" \
  "$postgres_image" >/dev/null

ready_count=0
for _ in {1..120}; do
  if docker exec "$container_name" pg_isready --host 127.0.0.1 --username kowa_harness --dbname kowa_webharness >/dev/null 2>&1; then
    ready_count="$((ready_count + 1))"
    [[ "$ready_count" -ge 3 ]] && break
  else
    ready_count=0
  fi
  sleep 0.25
done
test "$ready_count" -ge 3

# The harness runs the real composition root; give it the frozen schema in
# migration order so runtime registration, execution and delivery surfaces are
# the same real producers the Web pages consume.
for migration in "$repo_root"/db/migrations/*.up.sql; do
  docker exec --interactive "$container_name" \
    psql --username kowa_harness --dbname kowa_webharness --set ON_ERROR_STOP=1 \
    < "$migration" >/dev/null
done

host_port="$(docker port "$container_name" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
export KOWA_DATABASE_URL="postgres://kowa_harness:${postgres_password}@127.0.0.1:${host_port}/kowa_webharness?sslmode=disable"

cd "$repo_root"
bash scripts/web-env.sh pnpm build >"$temp_dir/web-build.log" 2>&1

export PATH="$(mise where go)/bin:$PATH"
go build -o "$temp_dir/webharness" ./tests/webharness

KOWA_HARNESS_ADDR="$web_address" \
  KOWA_HARNESS_STUB_ADDR="$stub_address" \
  KOWA_HARNESS_DIST="$repo_root/dist" \
  "$temp_dir/webharness" >"$temp_dir/webharness.log" 2>&1 &
harness_pid="$!"

for _ in {1..200}; do
  if curl --fail --silent "$stub_origin/stub/health" >/dev/null 2>&1 \
    && curl --fail --silent --insecure "$web_origin/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$harness_pid" 2>/dev/null; then
    printf 'harness exited before becoming ready:\n' >&2
    cat "$temp_dir/webharness.log" >&2
    exit 1
  fi
  sleep 0.25
done
curl --fail --silent --insecure "$web_origin/healthz" >/dev/null

KOWA_E2E_CONTROL_PLANE_URL="$web_origin" \
  KOWA_E2E_CONTROL_PLANE_STUB="$stub_origin" \
  KOWA_E2E_RUNTIME_ID="${KOWA_HARNESS_RUNTIME_ID:-runtime-harness}" \
  KOWA_E2E_RUNTIME_TOKEN="${KOWA_HARNESS_RUNTIME_TOKEN:-harness-runtime-token-0123456789}" \
  bash scripts/web-env.sh pnpm exec playwright test ${KOWA_E2E_ARGS:-}

printf 'CONTROL_PLANE_WEB_E2E_PASS\n'
