#!/usr/bin/env bash
# Validates the kowa.web-application.v1 contract artifacts owned by backend:S06.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_dir="$repo_root/api/web-application/v1"

jq --exit-status '.title == "kowa.web-application.v1" and (."$defs" | type == "object")' \
  "$contract_dir/schema.json" >/dev/null
for example in "$contract_dir"/examples/*.json; do
  jq empty "$example"
done

go_cmd="$(go env GOROOT)/bin/go"
GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -f -c \
  "$contract_dir/schema.json" "$contract_dir"/examples/positive-*.json
for example in "$contract_dir"/examples/negative-*.json; do
  if GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -q -f -c \
    "$contract_dir/schema.json" "$example"; then
    printf 'unexpected schema acceptance: %s\n' "$example" >&2
    exit 1
  fi
done

# The published digest must match the frozen schema. Every operation the Web
# application consumes is registered as a real HTTP operation.
jq --exit-status --arg digest "$(shasum -a 256 "$contract_dir/schema.json" | cut -d ' ' -f 1)" \
  '.contract == "kowa.web-application.v1" and .owner == "backend:S06"
   and .schema == "schema.json" and .schemaSha256 == $digest
   and ([.operations[] | .layer] | all(. == "http"))
   and ([.operations | keys[]] | sort ==
        ["delivery.artifact-fixrun", "intake.start-knowledge", "journey", "run.detail-history",
         "run.recovery", "workspace.list"])' \
  "$contract_dir/delivery.json" >/dev/null

# The registered operations must be the operations the handler actually mounts.
"$go_cmd" test ./internal/interfaces/httpapi -run 'TestWebApplicationSurfaceMatchesTheFrozenContract' -count=1

printf 'WEB_APPLICATION_CONTRACT_PASS\n'
