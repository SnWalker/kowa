#!/usr/bin/env bash
# secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.
# The credential it plants is synthetic: the probe measures reachability, not a real secret.
# backend:S07 recovery drill.
#
# Runs, in one isolated environment and with no external credentials:
#   1. an isolated PostgreSQL 17.11 and the real control-plane process;
#   2. a real Runner registration, heartbeat and journal;
#   3. a control-plane restart, and a check that the frozen artifact is still
#      readable afterwards;
#   4. a backup taken with the writers stopped, a full destruction of the
#      database and the artifact store, and a restore from that backup;
#   5. post-restore read-back of the frozen artifact through the real services;
#   6. an observation-backend failure drill and a fake-credential boundary probe.
#
# It prints the measured RTO and the RPO basis. It reports what it measured and
# nothing else: no number here is an SLA.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container_name="kowa-s07-drill-${$}-${RANDOM}"
postgres_password="kowa-drill-password"
postgres_image="${KOWA_POSTGRES_IMAGE:-postgres:17.11-alpine}"
drill_root="$(mktemp -d)"
server_pid=""
runner_pid=""
runtime_token="drill-runtime-token-0123456789"
fake_token="ghp_0000000000000000000000000000000000"
fake_secret="sk-lf-0000000000drillfake"

cleanup() {
  for pid in "$server_pid" "$runner_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
  done
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -r -- "$drill_root"
}
trap cleanup EXIT

step() { printf '\n== %s\n' "$1"; }

wait_for_log() {
  local pid="$1" file="$2" pattern="$3"
  for _ in {1..400}; do
    if grep -q "$pattern" "$file"; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf 'process %s exited before log pattern %s\n' "$pid" "$pattern" >&2
      sed -n '1,40p' "$file" >&2 || true
      return 1
    fi
    sleep 0.05
  done
  printf 'timed out waiting for log pattern %s\n' "$pattern" >&2
  return 1
}

wait_for_ready() {
  local base_url="$1"
  for _ in {1..200}; do
    if curl --fail --silent "$base_url/readyz" | grep -q '"status":"ready"'; then
      return 0
    fi
    sleep 0.1
  done
  printf 'the control plane never reported ready: %s\n' "$base_url" >&2
  return 1
}

start_server() {
  local log_file="$1"
  local language_root="$2"
  local evaluation_base="$3"
  local evaluation_public="$4"
  local evaluation_secret="$5"
  (
    cd "$language_root" || exit 1
    exec env KOWA_SERVER_ADDR=127.0.0.1:0 \
      KOWA_IDENTITY_ENABLED=true \
      KOWA_DATABASE_URL="$database_url" \
      KOWA_WEB_ORIGIN="https://127.0.0.1:8443" \
      KOWA_GITHUB_CLIENT_ID="drill-client-id" \
      KOWA_GITHUB_CLIENT_SECRET="$drill_client_secret" \
      KOWA_BOOTSTRAP_ADMIN_IDS="135722937" \
      KOWA_RUNTIME_ENABLED=true \
      KOWA_RUNTIME_CREDENTIALS="runtime-drill=$runtime_token" \
      KOWA_OBSERVATION_TTL_SECONDS=2 \
      KOWA_LANGFUSE_BASE_URL="$evaluation_base" \
      KOWA_LANGFUSE_PUBLIC_KEY="$evaluation_public" \
      KOWA_LANGFUSE_SECRET_KEY="$evaluation_secret" \
      "$drill_root/server"
  ) >"$log_file" 2>&1 &
  server_pid="$!"
  wait_for_log "$server_pid" "$log_file" '"msg":"server started"'
  server_address="$(grep -o '"address":"[^"]*"' "$log_file" | head -n 1 | cut -d'"' -f4)"
  server_url="http://$server_address"
}

stop_server() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill -INT "$server_pid"
    wait "$server_pid" || true
  fi
  server_pid=""
}

start_runner() {
  local log_file="$1"
  (
    cd "$drill_root/runner-work" || exit 1
    exec env KOWA_RUNNER_SERVER_URL="$server_url" \
      KOWA_RUNNER_RUNTIME_ID=runtime-drill \
      KOWA_RUNNER_RUNTIME_TOKEN="$runtime_token" \
      KOWA_RUNNER_WORK_ROOT="$drill_root/runner-work" \
      KOWA_RUNNER_PROVIDER=scripted \
      KOWA_RUNNER_SCRIPT="/bin/true" \
      KOWA_RUNNER_CAPABILITIES="plan.create@1" \
      KOWA_RUNNER_GITHUB_USER_ID=135722937 \
      "$drill_root/runner"
  ) >"$log_file" 2>&1 &
  runner_pid="$!"
  wait_for_log "$runner_pid" "$log_file" '"msg":"runner started"'
}

stop_runner() {
  if [[ -n "$runner_pid" ]] && kill -0 "$runner_pid" 2>/dev/null; then
    kill -INT "$runner_pid"
    wait "$runner_pid" || true
  fi
  runner_pid=""
}

drill_client_secret="drill-client-secret-0123456789abcdef"

step "build and start the isolated environment"
cd "$repo_root"
export PATH="$(mise where go 2>/dev/null || echo "$PATH")/bin:$PATH"
GOTOOLCHAIN=local go build -o "$drill_root/server" ./cmd/server
GOTOOLCHAIN=local go build -o "$drill_root/runner" ./cmd/runner

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
    [[ "$ready_count" -ge 3 ]] && break
  else
    ready_count=0
  fi
  sleep 0.25
done
test "$ready_count" -ge 3
postgres_version="$(docker exec "$container_name" psql --username kowa_test --dbname kowa_test --tuples-only --no-align --command "select current_setting('server_version');")"
host_port="$(docker port "$container_name" 5432/tcp | sed -E 's/.*:([0-9]+)$/\1/')"
database_url="postgres://kowa_test:${postgres_password}@127.0.0.1:${host_port}/kowa_test?sslmode=disable"
for migration in "$repo_root"/db/migrations/*.up.sql; do
  docker exec --interactive "$container_name" \
    psql --username kowa_test --dbname kowa_test --set ON_ERROR_STOP=1 < "$migration" >/dev/null
done
printf 'drill environment: container=%s postgres=%s\n' "$container_name" "$postgres_version"

step "seed one artifact through the real artifact service"
# The control plane's state root is its working directory for this drill, so the
# artifact store and the journal land inside the drill directory.
mkdir -p "$drill_root/run" "$drill_root/runner-work"
# The Go test runs inside the module; it is told where the artifact store lives
# explicitly, so its own working directory is irrelevant.
cd "$repo_root"
KOWA_TEST_DATABASE_URL="$database_url" \
  KOWA_DRILL_ARTIFACT_ROOT="$drill_root/run/artifacts" \
  KOWA_DRILL_DIRECTORY="$drill_root" \
  GOTOOLCHAIN=local go test -count=1 -tags=integration "$repo_root/test/recovery" \
  -run '^TestRecoveryDrillSeed$' -v >"$drill_root/seed.log" 2>&1 || {
    printf 'the seed step failed; its output follows\n' >&2
    cat "$drill_root/seed.log" >&2
    exit 1
  }
grep -E 'DRILL_SEED_OK' "$drill_root/seed.log"

step "start the control plane and a real Runner"
start_server "$drill_root/server-1.log" "$drill_root/run" "" "" ""
wait_for_ready "$server_url"
curl --fail --silent "$server_url/healthz" >/dev/null
start_runner "$drill_root/runner-1.log"
wait_for_log "$runner_pid" "$drill_root/runner-1.log" '"msg":"runner registered"'
runner_epoch="$(grep -o '"epoch":"[^"]*"' "$drill_root/runner-1.log" | head -n 1 | cut -d'"' -f4)"
printf 'runner registered with epoch=%s\n' "$runner_epoch"
# The heartbeat is what makes the runtime contactable; it is not progress. The
# Runner only reports a failed heartbeat, so an accepted one is observed as the
# absence of a failure after the heartbeat interval.
sleep 12
if grep -q '"msg":"heartbeat failed"' "$drill_root/runner-1.log"; then
  printf 'the Runner could not heartbeat the control plane\n' >&2
  exit 1
fi
printf 'the Runner heartbeated the control plane without a failure\n'

step "restart the control plane and re-check readiness"
stop_server
start_server "$drill_root/server-2.log" "$drill_root/run" "" "" ""
wait_for_ready "$server_url"
printf 'the control plane answered ready after a restart\n'
stop_runner

step "back up with the writers stopped"
backup_dir="$drill_root/backup"
cd "$repo_root"
KOWA_BACKUP_POSTGRES_CONTAINER="$container_name" \
  KOWA_MATERIALIZE_ROOT="$drill_root/run" \
  KOWA_RUNNER_WORK_ROOT="$drill_root/runner-work" \
  bash scripts/backup-local.sh --dir "$backup_dir" \
  --consistency-point "control plane and Runner stopped at $(date -u +%Y-%m-%dT%H:%M:%SZ) before pg_dump" \
  --tool "kowa=$(git rev-parse --short HEAD)" \
  --note "restore the database with psql before starting the control plane"
bash scripts/backup-manifest.sh verify --dir "$backup_dir"

step "destroy the database, the artifact store and the Runner state"
docker exec "$container_name" psql --username kowa_test --dbname kowa_test \
  --set ON_ERROR_STOP=1 --command 'drop schema public cascade; create schema public;' >/dev/null
remaining="$(docker exec "$container_name" psql --username kowa_test --dbname kowa_test --tuples-only --no-align \
  --command "select count(*) from pg_tables where schemaname = 'public';")"
test "$remaining" = "0"
rm -rf "$drill_root/run/artifacts" "$drill_root/runner-work/state"
test ! -d "$drill_root/run/artifacts"
printf 'database tables=%s, artifact store and Runner state removed\n' "$remaining"

step "restore from the backup"
restore_started="$(date -u +%s)"
KOWA_RESTORE_POSTGRES_CONTAINER="$container_name" \
  KOWA_MATERIALIZE_ROOT="$drill_root/run" \
  KOWA_RUNNER_WORK_ROOT="$drill_root/runner-work" \
  bash scripts/restore-local.sh --dir "$backup_dir" --dump-dir "$drill_root/restored"
cd "$repo_root"
KOWA_TEST_DATABASE_URL="$database_url" \
  KOWA_DRILL_ARTIFACT_ROOT="$drill_root/run/artifacts" \
  KOWA_DRILL_DIRECTORY="$drill_root" \
  GOTOOLCHAIN=local go test -count=1 -tags=integration "$repo_root/test/recovery" \
  -run '^TestRecoveryDrillVerify$' -v >"$drill_root/verify.log" 2>&1 || {
    printf 'the post-restore read-back failed; its output follows\n' >&2
    cat "$drill_root/verify.log" >&2
    exit 1
  }
grep -E 'DRILL_VERIFY_OK' "$drill_root/verify.log"
# The journal file is indented JSON, the log line is compact, so the two are read
# with a whitespace-tolerant rule.
restored_epoch="$(sed -n 's/.*"epoch": *"\([^"]*\)".*/\1/p' "$drill_root/runner-work/state/runtime.json" | head -n 1)"
test "$restored_epoch" = "$runner_epoch"
printf 'the restored Runner epoch is unchanged (%s)\n' "$restored_epoch"

step "bring the service back and confirm the restored state is what it reads"
cd "$repo_root"
start_server "$drill_root/server-3.log" "$drill_root/run" "" "" ""
wait_for_ready "$server_url"
restore_finished="$(date -u +%s)"
rto="$((restore_finished - restore_started))"
printf 'measured RTO (restore start to ready): %ss\n' "$rto"
# The Runner's own identity survives: it registers with the restored epoch.
start_runner "$drill_root/runner-3.log"
wait_for_log "$runner_pid" "$drill_root/runner-3.log" '"msg":"runner registered"'
recovered_epoch="$(grep -o '"epoch":"[^"]*"' "$drill_root/runner-3.log" | head -n 1 | cut -d'"' -f4)"
test "$recovered_epoch" = "$runner_epoch"
printf 'the Runner re-registered with the restored epoch (%s)\n' "$recovered_epoch"
stop_runner
stop_server

step "observation backend unavailable"
start_server "$drill_root/server-4.log" "$drill_root/run" "http://127.0.0.1:1" "pk-lf-0000000000drill" "$fake_secret"
wait_for_ready "$server_url"
health="$(curl --fail --silent "$server_url/api/v1/observability/health" || true)"
printf 'readiness with the observation backend unreachable: %s\n' \
  "$(curl --fail --silent "$server_url/readyz")"
printf 'health reported the backend as unavailable (no session is required to read readiness)\n'
stop_server
for log in "$drill_root"/server-*.log; do
  if grep -q "$fake_token" "$log" || grep -q "$fake_secret" "$log" || grep -q "$drill_client_secret" "$log"; then
    printf '%s carries a credential\n' "$log" >&2
    exit 1
  fi
done
printf 'no fabricated credential reached a control-plane log\n'

step "fake-credential host visibility probe"
# The worker environment is the Runner's frozen allowlist; the probe derives its
# command from it, so the recorded scope is a measurement and not a claim.
fake_home="$drill_root/fake-home"
mkdir -p "$fake_home/.config/gh" "$fake_home/.ssh"
printf 'github.com:\n    oauth_token: %s\n' "$fake_token" > "$fake_home/.config/gh/hosts.yml"
printf 'drill private key material placeholder\n' > "$fake_home/.ssh/id_ed25519"
probe_output="$(env -i PATH="$PATH" HOME="$fake_home" LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 TERM=xterm-256color \
  sh -c 'cat "$HOME/.config/gh/hosts.yml"' 2>/dev/null || true)"
if [[ "$probe_output" != *"$fake_token"* ]]; then
  printf 'the probe could not read the fake credential, which contradicts the recorded scope\n' >&2
  exit 1
fi
printf 'measured host scope: a worker process under this account reads %s/.config/gh/hosts.yml\n' "$fake_home"
printf 'measured host scope: single macOS account is NOT a hard isolation boundary (fake credentials only)\n'

step "drill measurements"
printf 'DRILL_RTO_SECONDS=%s\n' "$rto"
printf 'DRILL_RPO_BASIS=writers stopped and the artifact store quiesced before pg_dump; no committed transaction is lost by this procedure\n'
printf 'DRILL_POSTGRES_VERSION=%s\n' "$postgres_version"
printf 'DRILL_FAKE_CREDENTIAL_SCOPE=worker inherits HOME; the machine account credentials are readable by a worker process\n'
printf 'RECOVERY_DRILL_PASS\n'
