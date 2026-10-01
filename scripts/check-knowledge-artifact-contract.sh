#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_dir="$repo_root/api/knowledge-artifact/v1"
go_cmd="$(go env GOROOT)/bin/go"
# Pinned verification CLI, isolated from the application's module dependencies.
GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -f -c \
  "$contract_dir/schema.json" "$contract_dir"/examples/positive-*.json \
  "$contract_dir"/examples/negative-semantic-*.json
for example in "$contract_dir"/examples/negative-schema-*.json; do
  if GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -q -f -c \
    "$contract_dir/schema.json" "$example"; then
    printf 'unexpected schema acceptance: %s\n' "$example" >&2
    exit 1
  fi
done
"$go_cmd" test ./internal/knowledge -run '^TestContractExamples$' -count=1
printf 'KNOWLEDGE_ARTIFACT_CONTRACT_PASS\n'
