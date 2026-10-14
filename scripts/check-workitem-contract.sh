#!/usr/bin/env bash
# Validates the kowa.workitem.v1 contract artifacts owned by backend:S05.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_dir="$repo_root/api/workitem/v1"

jq --exit-status '.title == "kowa.workitem.v1" and (."$defs" | type == "object")' \
  "$contract_dir/schema.json" >/dev/null
for example in "$contract_dir"/examples/*.json; do
  jq empty "$example"
done

go_cmd="$(go env GOROOT)/bin/go"
GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -f -c \
  "$contract_dir/schema.json" "$contract_dir"/examples/positive-*.json
for example in "$contract_dir"/examples/negative-schema-*.json; do
  if GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -q -f -c \
    "$contract_dir/schema.json" "$example"; then
    printf 'unexpected schema acceptance: %s\n' "$example" >&2
    exit 1
  fi
done

# The published digest must match the frozen schema, and the manifest must not
# register a core application port as http.
jq --exit-status --arg digest "$(shasum -a 256 "$contract_dir/schema.json" | cut -d ' ' -f 1)" \
  '.contract == "kowa.workitem.v1" and .owner == "backend:S05"
   and .schema == "schema.json" and .schemaSha256 == $digest
   and ([.operations[] | .layer] | all(. == "http" or . == "application"))
   and ([.operations[] | select(.layer == "application")] | length >= 1)
   and ([.operations[] | select(.layer == "http") | has("method") and has("path")] | all)' \
  "$contract_dir/delivery.json" >/dev/null

"$go_cmd" test ./internal/interfaces/httpapi -run '^TestWorkItemSurfaceMatchesTheFrozenSchema$' -count=1

printf 'WORKITEM_CONTRACT_PASS\n'
