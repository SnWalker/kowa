#!/usr/bin/env bash
# Validates the kowa.fixed-workflow.v1 contract artifacts owned by backend:S06.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_dir="$repo_root/api/fixed-workflow/v1"

jq --exit-status '.title == "kowa.fixed-workflow.v1" and (."$defs" | type == "object")' \
  "$contract_dir/schema.json" >/dev/null
for example in "$contract_dir"/examples/*.json; do
  jq empty "$example"
done

go_cmd="$(go env GOROOT)/bin/go"
GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -f -c \
  "$contract_dir/schema.json" "$contract_dir/examples/positive-fixed-workflow-view.json"
for example in "$contract_dir"/examples/negative-*.json; do
  if GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -q -f -c \
    "$contract_dir/schema.json" "$example"; then
    printf 'unexpected schema acceptance: %s\n' "$example" >&2
    exit 1
  fi
done

# The authored definition document belongs to kowa.workflow-execution.v1: the
# published fixed definition must be a valid definition of that frozen contract.
GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -f -c \
  "$repo_root/api/workflow-execution/v1/schema.json" "$contract_dir/examples/positive-definition.json"

# The published digest must match the frozen schema, and the manifest must not
# register a non-HTTP layer as http.
jq --exit-status --arg digest "$(shasum -a 256 "$contract_dir/schema.json" | cut -d ' ' -f 1)" \
  '.contract == "kowa.fixed-workflow.v1" and .owner == "backend:S06"
   and .schema == "schema.json" and .schemaSha256 == $digest
   and ([.operations[] | .layer] | all(. == "http"))
   and ([.operations[] | select(.layer == "http") | has("method") and has("path")] | all)' \
  "$contract_dir/delivery.json" >/dev/null

"$go_cmd" test ./internal/fixedflow -run 'TestPublishedViewIsTheRealProducerOutput' -count=1

printf 'FIXED_WORKFLOW_CONTRACT_PASS\n'
