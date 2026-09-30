#!/usr/bin/env bash
# Evidence harness only: unchanged S02 production code, migrations 000001/000002,
# disposable PostgreSQL, and a Go overlay. Does not edit repository source.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
repro_dir="$(mktemp -d "${TMPDIR:-/tmp}/kowa-lease-repro.XXXXXX")"
trap 'rm -rf "$repro_dir"' EXIT
python3 - "$repo_root" "$repro_dir" <<'PY'
from pathlib import Path
import json, sys
root, temporary = map(Path, sys.argv[1:])
base = root / 'internal/infra/postgres/execution_store_integration_test.go'
evidence = root / 'doc/Kowa后端设计/record/evidence/S03-lease-mismatch-repro.go.txt'
(temporary / 'execution_test.go').write_text(base.read_text() + '\n' + evidence.read_text())
(temporary / 'omitted_s03.go').write_text('//go:build integration\n\npackage postgres_test\n')
(temporary / 'overlay.json').write_text(json.dumps({'Replace': {
    str(base): str(temporary / 'execution_test.go'),
    str(root / 'internal/infra/postgres/knowledge_artifact_store_integration_test.go'): str(temporary / 'omitted_s03.go'),
}}))
script = (root / 'scripts/test-workflow-execution-integration.sh').read_text()
script = script.replace('repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"', f'repo_root="{root}"')
script = script.replace('go test -count=1', f'go test -overlay="{temporary}/overlay.json" -v -count=1')
script = script.replace('TestWorkflowExecutionPostgres', 'TestLeaseMismatchDoesNotExpireCurrentTask')
script = script.replace('db/migrations/*.up.sql', 'db/migrations/00000[12]_*.up.sql')
script = script.replace("-name '*.down.sql'", "-name '00000[12]_*.down.sql'")
(temporary / 'run.sh').write_text(script)
PY
bash "$repro_dir/run.sh"
