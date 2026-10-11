#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
audit_tmp="$(mktemp -d "${TMPDIR:-/tmp}/kowa-round-audit.XXXXXX")"
trap 'rm -rf "$audit_tmp"' EXIT
cp "$repo_root/doc/Kowa后端设计/record/evidence/S02-current-round-characterization.go.txt" "$audit_tmp/probe_test.go"
python3 - "$repo_root" "$audit_tmp" <<'PYOVERLAY'
import json,sys
from pathlib import Path
root,tmp=map(Path,sys.argv[1:])
(tmp/'overlay.json').write_text(json.dumps({'Replace':{str(root/'internal/execution/s04_owner_audit_test.go'):str(tmp/'probe_test.go')}}))
PYOVERLAY
cd "$repo_root"
exec_go="${GO:-$(mise which go)}"
GOTOOLCHAIN=local "$exec_go" test -count=1 -json -overlay "$audit_tmp/overlay.json" ./internal/execution -run '^TestS04OwnerAudit'
