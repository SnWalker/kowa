#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_dir="$(mktemp -d)"
server_pid=""
runner_pid=""

cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill -KILL "$server_pid" 2>/dev/null || true
  fi
  if [[ -n "$runner_pid" ]] && kill -0 "$runner_pid" 2>/dev/null; then
    kill -KILL "$runner_pid" 2>/dev/null || true
  fi
  rm -r -- "$temp_dir"
}
trap cleanup EXIT

wait_for_log() {
  local pid="$1"
  local file="$2"
  local pattern="$3"

  for _ in {1..200}; do
    if grep -q "$pattern" "$file"; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf 'process %s exited before log pattern %s\n' "$pid" "$pattern" >&2
      return 1
    fi
    sleep 0.05
  done

  printf 'timed out waiting for log pattern %s\n' "$pattern" >&2
  return 1
}

cd "$repo_root"
go build -o "$temp_dir/" ./cmd/server ./cmd/runner

KOWA_SERVER_ADDR=127.0.0.1:0 "$temp_dir/server" >"$temp_dir/server.log" 2>&1 &
server_pid="$!"
wait_for_log "$server_pid" "$temp_dir/server.log" '"msg":"server started"'
server_address="$(jq -r 'select(.msg == "server started") | .address' "$temp_dir/server.log" | head -n 1)"
curl --fail --silent --show-error "http://$server_address/healthz" \
  | grep -q '^{"status":"ok"}$'

"$temp_dir/runner" >"$temp_dir/runner.log" 2>&1 &
runner_pid="$!"
wait_for_log "$runner_pid" "$temp_dir/runner.log" '"msg":"runner started"'

kill -INT "$server_pid" "$runner_pid"
wait "$server_pid"
server_pid=""
wait "$runner_pid"
runner_pid=""

grep -q '"msg":"server stopping"' "$temp_dir/server.log"
grep -q '"msg":"runner stopping"' "$temp_dir/runner.log"

printf 'BACKEND_PROCESS_TEST_PASS\n'
