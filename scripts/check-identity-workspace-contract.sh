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

printf 'IDENTITY_WORKSPACE_CONTRACT_PASS\n'
