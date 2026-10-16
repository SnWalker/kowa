#!/usr/bin/env bash
# backend:S08 acceptance stack: runs the real Kowa MVP on this Mac for the
# cross-track acceptance scenarios (W1—W9).
#
# What it starts, all real and unchanged:
#   1. an isolated PostgreSQL 17.11 container with the frozen migrations,
#   2. the control plane (`cmd/server`) wired for identity, runtime transport
#      and the GitHub App installation seam,
#   3. the acceptance Web origin (`tests/acceptance/weborigin`) serving the
#      production Web build at the exact trusted HTTPS origin,
#   4. optionally the real Runner (`cmd/runner`) with the frozen capability set
#      and the local Provider CLI.
#
# What it never does: install or authorize the GitHub App, log a user in,
# merge a PR, or invent any exterior fact. Those stay human operations per
# AGENTS §6; this script only removes the mechanical drudgery around them.
#
# The GitHub App credentials arrive through the environment and are never
# printed or written into the repository:
#   KOWA_GITHUB_CLIENT_ID, KOWA_GITHUB_CLIENT_SECRET   (App user authorization)
#   KOWA_GITHUB_APP_ID, KOWA_GITHUB_APP_PRIVATE_KEY_PATH (installation seam)
#
# Usage:
#   bash scripts/kowa-acceptance-stack.sh [--with-runner] [--fresh-db]
#
# Exit: SIGINT/SIGTERM stops the processes. The PostgreSQL container and its
# volume are kept on purpose (the campaign continues across restarts); pass
# --fresh-db to discard the previous database, or remove the container and
# volume by hand when the campaign ends.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

origin_address="${KOWA_ACCEPTANCE_ORIGIN:-127.0.0.1:8443}"
server_address="${KOWA_ACCEPTANCE_SERVER:-127.0.0.1:8080}"
container_name="${KOWA_ACCEPTANCE_PG_CONTAINER:-kowa-s08-acceptance-pg}"
volume_name="${KOWA_ACCEPTANCE_PG_VOLUME:-kowa-s08-acceptance-pgdata}"
run_dir="${KOWA_ACCEPTANCE_RUN_DIR:-/tmp/kowa-s08-run}"
postgres_image="${KOWA_POSTGRES_IMAGE:-postgres:17.11-alpine}"
postgres_password="kowa-acceptance-password"
database_name="kowa_acceptance"
database_user="kowa_acceptance"
# The accepted workspace binds exactly this pair; both are frozen in the test
# repository agreement (SnWalker/kowa-sandbox, SnWalker/kowa-sandbox-knowledge).
project_repository_id="${KOWA_ACCEPTANCE_PROJECT_REPO_ID:-1401511579}"
knowledge_repository_id="${KOWA_ACCEPTANCE_KNOWLEDGE_REPO_ID:-1413481670}"
project_clone_url="${KOWA_ACCEPTANCE_PROJECT_CLONE_URL:-https://github.com/SnWalker/kowa-sandbox.git}"
knowledge_clone_url="${KOWA_ACCEPTANCE_KNOWLEDGE_CLONE_URL:-https://github.com/SnWalker/kowa-sandbox-knowledge.git}"
machine_github_user_id="${KOWA_ACCEPTANCE_MACHINE_USER_ID:-135722937}"
bootstrap_admin_ids="${KOWA_ACCEPTANCE_ADMIN_IDS:-135722937}"

with_runner=0
fresh_db=0
for argument in "$@"; do
  case "$argument" in
    --with-runner) with_runner=1 ;;
    --fresh-db) fresh_db=1 ;;
    *) printf 'unknown argument: %s\n' "$argument" >&2; exit 2 ;;
  esac
done

require_env() {
  local name=$1
  if [ -z "${!name:-}" ]; then
    printf 'missing required environment variable: %s\n' "$name" >&2
    printf '(the GitHub App credentials are user-provided and must not be committed; see the S08 operation list)\n' >&2
    exit 2
  fi
}

server_pid=""
origin_pid=""
runner_pid=""

stop_processes() {
  for pid in "$runner_pid" "$server_pid" "$origin_pid"; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill -INT "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
}
trap 'stop_processes' EXIT
# A background invocation would otherwise ignore SIGINT/SIGQUIT (shell rule), so
# the stop signals are trapped explicitly: they end the script, which runs the
# EXIT trap and stops every child it started.
trap 'exit 0' INT TERM HUP

command -v docker >/dev/null || { printf 'docker is required\n' >&2; exit 2; }
command -v go >/dev/null || { printf 'go is required\n' >&2; exit 2; }

require_env KOWA_GITHUB_CLIENT_ID
require_env KOWA_GITHUB_CLIENT_SECRET
require_env KOWA_GITHUB_APP_ID
require_env KOWA_GITHUB_APP_PRIVATE_KEY_PATH

if [ ! -f "$KOWA_GITHUB_APP_PRIVATE_KEY_PATH" ]; then
  printf 'the GitHub App private key file does not exist: %s\n' "$KOWA_GITHUB_APP_PRIVATE_KEY_PATH" >&2
  exit 2
fi

mkdir -p "$run_dir"

if [ "$fresh_db" -eq 1 ]; then
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker volume rm "$volume_name" >/dev/null 2>&1 || true
fi

# 1. Isolated PostgreSQL with a persistent volume: the acceptance campaign
#    keeps its runs across process restarts, and every scenario still uses a
#    new WorkItem/Run/Task/Artifact/PR of its own.
if ! docker inspect "$container_name" >/dev/null 2>&1; then
  docker volume create "$volume_name" >/dev/null
  docker run --detach --name "$container_name" \
    --publish "127.0.0.1::5432" \
    --volume "$volume_name:/var/lib/postgresql/data" \
    --env POSTGRES_DB="$database_name" \
    --env POSTGRES_USER="$database_user" \
    --env POSTGRES_PASSWORD="$postgres_password" \
    "$postgres_image" >/dev/null
else
  docker start "$container_name" >/dev/null
fi

ready=0
for _ in $(seq 1 120); do
  if docker exec "$container_name" pg_isready --host 127.0.0.1 --username "$database_user" --dbname "$database_name" >/dev/null 2>&1; then
    ready=$((ready + 1))
    [ "$ready" -ge 3 ] && break
  else
    ready=0
  fi
  sleep 0.5
done
[ "$ready" -ge 3 ] || { printf 'the acceptance PostgreSQL did not become ready\n' >&2; exit 1; }

# The frozen migrations are applied in order; re-running them on an existing
# schema is not needed and not attempted (the migration files are not
# idempotent by design).
if ! docker exec "$container_name" psql --username "$database_user" --dbname "$database_name" \
    --tuples-only --command "select to_regclass('public.workflow_run') is not null" | grep -q t; then
  for migration in "$repo_root"/db/migrations/*.up.sql; do
    docker exec --interactive "$container_name" \
      psql --username "$database_user" --dbname "$database_name" --set ON_ERROR_STOP=1 \
      < "$migration" >/dev/null
  done
fi

host_port="$(docker port "$container_name" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
export KOWA_DATABASE_URL="postgres://${database_user}:${postgres_password}@127.0.0.1:${host_port}/${database_name}?sslmode=disable"

# 2. The runtime credential is generated once and kept in the run directory so
#    a restarted Runner keeps the same registration identity. It is never
#    printed and never written into the repository.
runtime_id="${KOWA_ACCEPTANCE_RUNTIME_ID:-runtime-s08}"
token_file="$run_dir/runtime-token"
if [ ! -f "$token_file" ]; then
  (umask 077; openssl rand -hex 24 > "$token_file")
fi
runtime_token="$(cat "$token_file")"

# 3. Production Web build and the acceptance binaries.
bash scripts/web-env.sh pnpm build > "$run_dir/web-build.log" 2>&1
go build -o "$run_dir/bin/server" ./cmd/server
go build -o "$run_dir/bin/runner" ./cmd/runner
go build -o "$run_dir/bin/weborigin" ./tests/acceptance/weborigin

# 4. The acceptance Web origin: the exact https origin the identity surface
#    trusts, serving the production build and proxying the API.
KOWA_WEB_ORIGIN_ADDR="$origin_address" \
KOWA_CONTROL_PLANE_URL="http://${server_address}" \
KOWA_WEB_DIST="$repo_root/dist" \
KOWA_TLS_DIR="$run_dir/tls" \
  "$run_dir/bin/weborigin" > "$run_dir/weborigin.log" 2>&1 &
origin_pid=$!

# 5. The real control plane.
KOWA_SERVER_ADDR="$server_address" \
KOWA_IDENTITY_ENABLED=true \
KOWA_WEB_ORIGIN="https://${origin_address}" \
KOWA_GITHUB_CLIENT_ID="$KOWA_GITHUB_CLIENT_ID" \
KOWA_GITHUB_CLIENT_SECRET="$KOWA_GITHUB_CLIENT_SECRET" \
KOWA_GITHUB_APP_ID="$KOWA_GITHUB_APP_ID" \
KOWA_GITHUB_APP_PRIVATE_KEY_PATH="$KOWA_GITHUB_APP_PRIVATE_KEY_PATH" \
KOWA_BOOTSTRAP_ADMIN_IDS="$bootstrap_admin_ids" \
KOWA_RUNTIME_ENABLED=true \
KOWA_RUNTIME_CREDENTIALS="${runtime_id}=${runtime_token}" \
KOWA_MATERIALIZE_ROOT="$run_dir/materialize" \
KOWA_RECONCILE_INTERVAL="${KOWA_ACCEPTANCE_RECONCILE_INTERVAL:-1s}" \
KOWA_GITHUB_MACHINE_USER_ID="$machine_github_user_id" \
KOWA_REPOSITORY_REGISTRY="${project_repository_id}=${project_clone_url},${knowledge_repository_id}=${knowledge_clone_url}" \
  "$run_dir/bin/server" > "$run_dir/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 120); do
  if curl --fail --silent --insecure "https://${origin_address}/healthz" >/dev/null 2>&1; then
    break
  fi
  kill -0 "$server_pid" 2>/dev/null || { printf 'the control plane exited early:\n' >&2; tail -20 "$run_dir/server.log" >&2; exit 1; }
  sleep 0.5
done
curl --fail --silent --insecure "https://${origin_address}/healthz" >/dev/null

if [ "$with_runner" -eq 1 ]; then
  # 6. The real Runner: the local Provider CLI and the machine's personal gh
  #    identity perform the remote operations through the controlled channel.
  KOWA_RUNNER_SERVER_URL="http://${server_address}" \
  KOWA_RUNNER_RUNTIME_ID="$runtime_id" \
  KOWA_RUNNER_RUNTIME_TOKEN="$runtime_token" \
  KOWA_RUNNER_WORK_ROOT="$run_dir/runner" \
  KOWA_RUNNER_PROVIDER="${KOWA_ACCEPTANCE_PROVIDER:-claude}" \
  KOWA_RUNNER_PROVIDER_VERSION="${KOWA_ACCEPTANCE_PROVIDER_VERSION:-}" \
  KOWA_RUNNER_GITHUB_USER_ID="$machine_github_user_id" \
  KOWA_RUNNER_CAPABILITIES="plan.create@1,plan.review@1,testcases.design@1,code.implement@1,test.execute@1,code.review@1,security.review@1,delivery.publish_pr@1,delivery.integration_verify@1" \
  KOWA_RUNNER_REPOSITORIES="${project_repository_id}=${project_clone_url},${knowledge_repository_id}=${knowledge_clone_url}" \
    "$run_dir/bin/runner" > "$run_dir/runner.log" 2>&1 &
  runner_pid=$!
  sleep 1
  kill -0 "$runner_pid" 2>/dev/null || { printf 'the Runner exited early:\n' >&2; tail -20 "$run_dir/runner.log" >&2; exit 1; }
fi

printf 'ACCEPTANCE_STACK_READY\n'
printf '  web origin      : https://%s  (trust %s/tls/origin-cert.pem once)\n' "$origin_address" "$run_dir"
printf '  control plane   : http://%s (logs: %s/server.log)\n' "$server_address" "$run_dir"
printf '  runtime id      : %s\n' "$runtime_id"
printf '  repositories    : project=%s knowledge=%s\n' "$project_repository_id" "$knowledge_repository_id"
printf '  database        : container %s (volume %s), db %s\n' "$container_name" "$volume_name" "$database_name"
if [ "$with_runner" -eq 1 ]; then
  printf '  runner          : started (logs: %s/runner.log)\n' "$run_dir"
else
  printf '  runner          : not started; re-run with --with-runner to start it\n'
fi
printf 'press Ctrl-C to stop the processes\n'

# Wait until the control plane exits (a crash or the operator's stop); the
# EXIT trap stops whatever is still running and the database container is left
# in place on purpose so the campaign continues across stack restarts.
while kill -0 "$server_pid" 2>/dev/null; do
  sleep 1
done
printf 'ACCEPTANCE_STACK_STOPPED\n'
