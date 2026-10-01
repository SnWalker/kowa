#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_dir="$repo_root/api/identity-workspace/v1"

jq --exit-status '.title == "kowa.identity-workspace.v1" and (."$defs" | type == "object")' \
  "$contract_dir/schema.json" >/dev/null

for example in "$contract_dir"/examples/*.json; do
  jq empty "$example"
done

jq --exit-status '
  .project.role == "project"
  and .project.access == "read_write"
  and .knowledge.role == "knowledge"
  and .knowledge.access == "read"
  and .project.githubRepositoryId != .knowledge.githubRepositoryId
' "$contract_dir/examples/positive-workspace-config.json" >/dev/null

jq --exit-status '.expected.body.error == "FORBIDDEN"' \
  "$contract_dir/examples/negative-cross-workspace.json" >/dev/null
jq --exit-status '.expected.body.error == "POLICY_BLOCKED"' \
  "$contract_dir/examples/negative-second-writable-project.json" >/dev/null

go_cmd="$(go env GOROOT)/bin/go"
for family in identity-workspace web-session; do
  directory="$repo_root/api/$family/v1"
  GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -f -c \
    -m "https://kowa.dev/contracts/=$repo_root/api" \
    "$directory/schema.json" "$directory"/examples/positive-*.json
  for example in "$directory"/examples/negative-schema-*.json; do
    if GOTOOLCHAIN=local "$go_cmd" run github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0 -q -f -c \
      -m "https://kowa.dev/contracts/=$repo_root/api" "$directory/schema.json" "$example"; then
      printf 'unexpected schema acceptance: %s\n' "$example" >&2
      exit 1
    fi
  done
  jq --exit-status --arg digest "$(shasum -a 256 "$directory/schema.json" | cut -d ' ' -f 1)" \
    '.owner == "backend:S01" and .schemaSha256 == $digest and ([.operations[]] | all(. == "http"))' "$directory/delivery.json" >/dev/null
 done
printf 'IDENTITY_WORKSPACE_CONTRACT_PASS\n'
