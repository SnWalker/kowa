#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
contract_dir="$repo_root/api/workflow-execution/v1"

jq --exit-status '
  .title == "kowa.workflow-execution.v1"
  and (."$defs".TaskSpec.required | index("providerSelection") != null)
  and (."$defs".TaskSpec.required | index("gitExecutionUserId") != null)
  and (."$defs".TaskSpec.required | index("gitScopes") != null)
  and (."$defs".RuntimeRegistration.additionalProperties == false)
' "$contract_dir/schema.json" >/dev/null

for example in "$contract_dir"/examples/*.json; do
  jq empty "$example"
done

jq --exit-status '
  .task.gitExecutionUserId == "9001"
  and .task.providerSelection.id == "codex"
  and (.task.gitScopes | length == 1)
' "$contract_dir/examples/positive-task-lease.json" >/dev/null
jq --exit-status '.nodes[0].state == "DECIDED" and .nodes[0].verdict == "needs_revision"' \
  "$contract_dir/examples/positive-run-view.json" >/dev/null
jq --exit-status '.expected.error == "VERSION_CONFLICT"' \
  "$contract_dir/examples/negative-definition-conflict.json" >/dev/null
jq --exit-status '.expected.error == "STALE_RESULT" and (.expected.advancesNode | not)' \
  "$contract_dir/examples/negative-stale-result.json" >/dev/null
jq --exit-status '.expected.error == "RESOURCE_UNAVAILABLE" and (.expected.leased | not)' \
  "$contract_dir/examples/negative-runtime-mismatch.json" >/dev/null
jq --exit-status '.kind == "apiError" and .error == "EXTERNAL_UNKNOWN"' \
  "$contract_dir/examples/negative-external-unknown.json" >/dev/null

printf 'WORKFLOW_EXECUTION_CONTRACT_PASS\n'
