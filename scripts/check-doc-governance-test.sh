#!/usr/bin/env bash

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
CHECK="$SCRIPT_DIR/check-doc-governance.sh"

"$CHECK"

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/kowa-doc-governance.XXXXXX")
trap 'rm -rf "$TEST_ROOT"' EXIT INT TERM

mkdir -p \
  "$TEST_ROOT/doc/wiki" \
  "$TEST_ROOT/doc/Kowa后端设计/stage" \
  "$TEST_ROOT/doc/Kowa后端设计/record" \
  "$TEST_ROOT/doc/Kowa前端设计/stage" \
  "$TEST_ROOT/doc/Kowa前端设计/record" \
  "$TEST_ROOT/doc/Kowa验收与运行"

cp "$PROJECT_ROOT/AGENTS.md" "$PROJECT_ROOT/mise.toml" "$TEST_ROOT/"
cp "$PROJECT_ROOT/doc/common.md" "$PROJECT_ROOT/doc/decision.md" \
   "$PROJECT_ROOT/doc/bootstrap-record.md" "$PROJECT_ROOT/doc/当前进展.md" "$TEST_ROOT/doc/"
cp -R "$PROJECT_ROOT/doc/wiki/." "$TEST_ROOT/doc/wiki/"
cp "$PROJECT_ROOT/doc/Kowa后端设计/总体设计与进度.md" \
   "$PROJECT_ROOT/doc/Kowa后端设计/当前阶段与下一步.md" \
   "$PROJECT_ROOT/doc/Kowa后端设计/验证规则.md" \
   "$TEST_ROOT/doc/Kowa后端设计/"
cp "$PROJECT_ROOT/doc/Kowa前端设计/总体设计与进度.md" \
   "$PROJECT_ROOT/doc/Kowa前端设计/当前阶段与下一步.md" \
   "$PROJECT_ROOT/doc/Kowa前端设计/验证规则.md" \
   "$TEST_ROOT/doc/Kowa前端设计/"
cp "$PROJECT_ROOT/doc/Kowa后端设计/stage/"S[0-9][0-9]-*.md \
   "$TEST_ROOT/doc/Kowa后端设计/stage/"
cp "$PROJECT_ROOT/doc/Kowa前端设计/stage/"S[0-9][0-9]-*.md \
   "$TEST_ROOT/doc/Kowa前端设计/stage/"

KOWA_ROOT="$TEST_ROOT" "$CHECK"

# Each mutation keeps valid JSON, so rejection must come from the schema rather
# than a parse error. Missing fields are not equivalent to explicit null.
JSON_NEGATIVE_FAILURES=0
for mutation in \
  'doc/Kowa后端设计/总体设计与进度.md|currentStage|missing' \
  'doc/Kowa后端设计/当前阶段与下一步.md|currentStage|missing' \
  'doc/Kowa后端设计/总体设计与进度.md|activeReopen|missing' \
  'doc/Kowa后端设计/总体设计与进度.md|suspendedReopen|missing' \
  'doc/当前进展.md|backendCurrentStage|missing' \
  'doc/当前进展.md|frontendCurrentStage|missing' \
  'doc/Kowa后端设计/总体设计与进度.md|currentStage|empty' \
  'doc/Kowa后端设计/当前阶段与下一步.md|currentStage|empty'; do
  mutation_file=${mutation%%|*}
  mutation_fields=${mutation#*|}
  mutation_key=${mutation_fields%%|*}
  mutation_kind=${mutation_fields#*|}
  if [ "$mutation_kind" = missing ]; then
    sed "s/\"${mutation_key}\":/\"omitted_${mutation_key}\":/" \
      "$PROJECT_ROOT/$mutation_file" > "$TEST_ROOT/$mutation_file"
  else
    sed -E "s/\"${mutation_key}\": (null|\"S[0-9]{2}\")/\"${mutation_key}\": \"\"/" \
      "$PROJECT_ROOT/$mutation_file" > "$TEST_ROOT/$mutation_file"
  fi
  if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
    printf 'expected %s %s in %s to fail\n' "$mutation_kind" "$mutation_key" "$mutation_file" >&2
    JSON_NEGATIVE_FAILURES=$((JSON_NEGATIVE_FAILURES + 1))
  fi
  cp "$PROJECT_ROOT/$mutation_file" "$TEST_ROOT/$mutation_file"
done
if [ "$JSON_NEGATIVE_FAILURES" -ne 0 ]; then
  printf 'JSON_SCHEMA_TEST_FAILED: %s unexpected pass(es)\n' "$JSON_NEGATIVE_FAILURES" >&2
  exit 1
fi
printf 'JSON_SCHEMA_TEST_PASS: 8 negative cases\n'

rm "$TEST_ROOT/doc/wiki/contracts/index.md"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected missing-index fixture to fail\n' >&2
  exit 1
fi
cp "$PROJECT_ROOT/doc/wiki/contracts/index.md" "$TEST_ROOT/doc/wiki/contracts/index.md"

printf '| S00 | UNKNOWN | 无 | invalid fixture |\n' >> "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected invalid-status fixture to fail\n' >&2
  exit 1
fi
cp "$PROJECT_ROOT/doc/Kowa后端设计/总体设计与进度.md" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"

sed -i.bak -E 's/^go[[:space:]]*=[[:space:]]*"[^"]+"/go = "0.0.0"/' "$TEST_ROOT/mise.toml"
rm "$TEST_ROOT/mise.toml.bak"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected toolchain-version-mismatch fixture to fail\n' >&2
  exit 1
fi
cp "$PROJECT_ROOT/mise.toml" "$TEST_ROOT/mise.toml"

# The remaining fixtures exercise isolated state transitions. Reset the copied
# current plan to a valid pre-S00 skeleton so fixture stage IDs do not collide.
for track in Kowa后端设计 Kowa前端设计; do
  awk '!/^\| S[0-9][0-9] /' \
    "$PROJECT_ROOT/doc/$track/总体设计与进度.md" > \
    "$TEST_ROOT/doc/$track/总体设计与进度.md"
  sed -i.bak -E 's/"currentStage": "S[0-9]{2}"/"currentStage": null/' \
    "$TEST_ROOT/doc/$track/总体设计与进度.md" \
    "$TEST_ROOT/doc/$track/当前阶段与下一步.md"
  rm "$TEST_ROOT/doc/$track/总体设计与进度.md.bak" \
     "$TEST_ROOT/doc/$track/当前阶段与下一步.md.bak"
done
sed -i.bak -E \
  -e 's/"backendCurrentStage": "S[0-9]{2}"/"backendCurrentStage": null/' \
  -e 's/"frontendCurrentStage": "S[0-9]{2}"/"frontendCurrentStage": null/' \
  "$TEST_ROOT/doc/当前进展.md"
rm "$TEST_ROOT/doc/当前进展.md.bak"
rm -f "$TEST_ROOT/doc/Kowa后端设计/stage/"S[0-9][0-9]-*.md \
      "$TEST_ROOT/doc/Kowa后端设计/record/"S[0-9][0-9].md \
      "$TEST_ROOT/doc/Kowa前端设计/stage/"S[0-9][0-9]-*.md \
      "$TEST_ROOT/doc/Kowa前端设计/record/"S[0-9][0-9].md

# A cross-track consumer may be planned while its producer is unfinished, but
# it cannot start until the producer is DONE and its contract binding is frozen.
cp "$PROJECT_ROOT/doc/wiki/operations/stage-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/stage/S00-contract-owner.md"
cp "$PROJECT_ROOT/doc/wiki/operations/stage-template.md" \
   "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md"
sed -i.bak \
  -e 's/^- 共享契约 owner：.*/- 共享契约 owner：backend:S00/' \
  -e 's/^- 冻结契约及版本：.*/- 冻结契约及版本：kowa.cross-end.v1/' \
  "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md"
rm "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md.bak"
printf '| S00 | NOT_STARTED | 无 | contract owner fixture |\n' >> \
  "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
printf '| S00 | IN_PROGRESS | backend:S00 | contract consumer fixture |\n' >> \
  "$TEST_ROOT/doc/Kowa前端设计/总体设计与进度.md"
for track in Kowa后端设计 Kowa前端设计; do
  sed -i.bak 's/"currentStage": null/"currentStage": "S00"/' \
    "$TEST_ROOT/doc/$track/总体设计与进度.md" \
    "$TEST_ROOT/doc/$track/当前阶段与下一步.md"
  rm "$TEST_ROOT/doc/$track/总体设计与进度.md.bak" \
     "$TEST_ROOT/doc/$track/当前阶段与下一步.md.bak"
done
sed -i.bak \
  -e 's/"backendCurrentStage": null/"backendCurrentStage": "S00"/' \
  -e 's/"frontendCurrentStage": null/"frontendCurrentStage": "S00"/' \
  "$TEST_ROOT/doc/当前进展.md"
rm "$TEST_ROOT/doc/当前进展.md.bak"
cp "$PROJECT_ROOT/doc/wiki/operations/record-template.md" \
   "$TEST_ROOT/doc/Kowa前端设计/record/S00.md"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected unfinished cross-track prerequisite fixture to fail\n' >&2
  exit 1
fi

sed -i.bak 's/| S00 | NOT_STARTED |/| S00 | DONE |/' \
  "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
rm "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak"
cp "$PROJECT_ROOT/doc/wiki/operations/record-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/record/S00.md"
sed -i.bak \
  -e 's/^- 开始 HEAD：$/- 开始 HEAD：fixture-head/' \
  -e 's/^- 预存工作区：$/- 预存工作区：fixture-worktree/' \
  -e 's/^- 实际修改边界：$/- 实际修改边界：fixture-scope/' \
  -e 's/^- 自动验证命令、退出码与测试数量：$/- 自动验证命令、退出码与测试数量：fixture-pass/' \
  -e 's/^- 残余风险和未完成项：$/- 残余风险和未完成项：fixture-none/' \
  -e 's/^- 阶段退出结论：$/- 阶段退出结论：fixture-done/' \
  -e 's/^- 下一阶段准入结论：$/- 下一阶段准入结论：fixture-ready/' \
  "$TEST_ROOT/doc/Kowa后端设计/record/S00.md"
rm "$TEST_ROOT/doc/Kowa后端设计/record/S00.md.bak"
KOWA_ROOT="$TEST_ROOT" "$CHECK"

cp "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md" \
   "$TEST_ROOT/valid-cross-contract-stage.md"
sed -i.bak 's/^- 共享契约 owner：.*/- 共享契约 owner：/' \
  "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md"
rm "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md.bak"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected missing cross-contract owner fixture to fail\n' >&2
  exit 1
fi
cp "$TEST_ROOT/valid-cross-contract-stage.md" \
   "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md"
sed -i.bak 's/^- 冻结契约及版本：.*/- 冻结契约及版本：candidate/' \
  "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md"
rm "$TEST_ROOT/doc/Kowa前端设计/stage/S00-contract-consumer.md.bak"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected unfrozen cross-contract version fixture to fail\n' >&2
  exit 1
fi
printf 'CROSS_TRACK_GATE_TEST_PASS: 3 negative cases\n'

for track in Kowa后端设计 Kowa前端设计; do
  awk '!/^\| S[0-9][0-9] /' \
    "$PROJECT_ROOT/doc/$track/总体设计与进度.md" > \
    "$TEST_ROOT/doc/$track/总体设计与进度.md"
  cp "$PROJECT_ROOT/doc/$track/当前阶段与下一步.md" \
     "$TEST_ROOT/doc/$track/当前阶段与下一步.md"
  sed -i.bak -E 's/"currentStage": "S[0-9]{2}"/"currentStage": null/' \
    "$TEST_ROOT/doc/$track/总体设计与进度.md" \
    "$TEST_ROOT/doc/$track/当前阶段与下一步.md"
  rm "$TEST_ROOT/doc/$track/总体设计与进度.md.bak" \
     "$TEST_ROOT/doc/$track/当前阶段与下一步.md.bak"
done
cp "$PROJECT_ROOT/doc/当前进展.md" "$TEST_ROOT/doc/当前进展.md"
sed -i.bak -E \
  -e 's/"backendCurrentStage": "S[0-9]{2}"/"backendCurrentStage": null/' \
  -e 's/"frontendCurrentStage": "S[0-9]{2}"/"frontendCurrentStage": null/' \
  "$TEST_ROOT/doc/当前进展.md"
rm "$TEST_ROOT/doc/当前进展.md.bak"
rm -f "$TEST_ROOT/doc/Kowa后端设计/stage/"S[0-9][0-9]-*.md \
      "$TEST_ROOT/doc/Kowa后端设计/record/"S[0-9][0-9].md \
      "$TEST_ROOT/doc/Kowa前端设计/stage/"S[0-9][0-9]-*.md \
      "$TEST_ROOT/doc/Kowa前端设计/record/"S[0-9][0-9].md \
      "$TEST_ROOT/valid-cross-contract-stage.md"

cp "$PROJECT_ROOT/doc/wiki/operations/stage-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/stage/S00-fixture.md"
printf '| S00 | IN_PROGRESS | 无 | fixture goal |\n' >> "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
sed -i.bak 's/"currentStage": null/"currentStage": "S00"/' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
rm "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak"
sed -i.bak 's/"currentStage": null/"currentStage": "S00"/' "$TEST_ROOT/doc/Kowa后端设计/当前阶段与下一步.md"
rm "$TEST_ROOT/doc/Kowa后端设计/当前阶段与下一步.md.bak"
sed -i.bak 's/"backendCurrentStage": null/"backendCurrentStage": "S00"/' "$TEST_ROOT/doc/当前进展.md"
rm "$TEST_ROOT/doc/当前进展.md.bak"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected missing-record fixture to fail\n' >&2
  exit 1
fi
cp "$PROJECT_ROOT/doc/wiki/operations/record-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/record/S00.md"
KOWA_ROOT="$TEST_ROOT" "$CHECK"

sed -i.bak 's/| S00 | IN_PROGRESS | 无 |/| S00 | IN_PROGRESS | S99 |/' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected missing-prerequisite fixture to fail\n' >&2
  exit 1
fi
mv "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"

sed -i.bak 's/| S00 | IN_PROGRESS |/| S00 | REOPENED |/' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
rm "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected reopened-without-descriptor fixture to fail\n' >&2
  exit 1
fi
sed -i.bak 's/| S00 | REOPENED |/| S00 | IN_PROGRESS |/' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
rm "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak"

awk '
  /"activeReopen": null,/ {
    print "  \"activeReopen\": {"
    print "    \"ownerStage\": \"S00\","
    print "    \"ownerPreviousStatus\": \"DONE\","
    print "    \"previousCurrentStage\": null,"
    print "    \"reopenedAt\": \"2026-09-24T00:00:00+08:00\","
    print "    \"successorBaseline\": {}"
    print "  },"
    next
  }
  { print }
' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md" > "$TEST_ROOT/reopen-state.tmp"
mv "$TEST_ROOT/reopen-state.tmp" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
KOWA_ROOT="$TEST_ROOT" "$CHECK"

sed -i.bak 's/| S00 | IN_PROGRESS |/| S00 | REOPENED |/' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
rm "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak"
sed -i.bak 's/^- `CODE_CONFIRMED`：$/- `CODE_CONFIRMED`：fixture evidence/' "$TEST_ROOT/doc/Kowa后端设计/record/S00.md"
rm "$TEST_ROOT/doc/Kowa后端设计/record/S00.md.bak"
KOWA_ROOT="$TEST_ROOT" "$CHECK"

cp "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md" "$TEST_ROOT/valid-reopen-state.md"
awk '
  /"ownerPreviousStatus": "DONE",/ && !removed { removed = 1; next }
  /"suspendedReopen": null/ {
    print "  \"suspendedReopen\": {"
    print "    \"ownerStage\": \"S00\","
    print "    \"ownerPreviousStatus\": \"DONE\","
    print "    \"previousCurrentStage\": null,"
    print "    \"reopenedAt\": \"2026-09-24T00:00:00+08:00\","
    print "    \"successorBaseline\": {}"
    print "  }"
    next
  }
  { print }
' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md" > "$TEST_ROOT/reopen-scope.tmp"
mv "$TEST_ROOT/reopen-scope.tmp" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected active-reopen-scope fixture to fail\n' >&2
  exit 1
fi
mv "$TEST_ROOT/valid-reopen-state.md" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"

cp "$PROJECT_ROOT/doc/wiki/operations/stage-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/stage/S01-nested-fixture.md"
cp "$PROJECT_ROOT/doc/wiki/operations/record-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/record/S01.md"
sed -i.bak \
  -e 's/^- BLOCKED 复现方式：$/- BLOCKED 复现方式：fixture reproduction/' \
  -e 's/^- BLOCKED 影响与本阶段不可消除依据：$/- BLOCKED 影响与本阶段不可消除依据：earlier owner must finish/' \
  -e 's/^- BLOCKED 解除条件和责任人：$/- BLOCKED 解除条件和责任人：S00 DONE, S01 owner/' \
  "$TEST_ROOT/doc/Kowa后端设计/record/S01.md"
rm "$TEST_ROOT/doc/Kowa后端设计/record/S01.md.bak"
printf '| S01 | BLOCKED | S00 | suspended nested fixture |\n' >> "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
awk '
  /"previousCurrentStage": null,/ && !active_previous {
    print "    \"previousCurrentStage\": \"S01\","
    active_previous = 1
    next
  }
  /"successorBaseline": \{\}/ && !active_baseline {
    print "    \"successorBaseline\": {"
    print "      \"S01\": \"BLOCKED\""
    print "    }"
    active_baseline = 1
    next
  }
  /"suspendedReopen": null/ {
    print "  \"suspendedReopen\": {"
    print "    \"ownerStage\": \"S01\","
    print "    \"ownerPreviousStatus\": \"DONE\","
    print "    \"previousCurrentStage\": null,"
    print "    \"reopenedAt\": \"2026-09-24T00:00:00+08:00\","
    print "    \"successorBaseline\": {}"
    print "  }"
    next
  }
  { print }
' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md" > "$TEST_ROOT/nested-reopen.tmp"
mv "$TEST_ROOT/nested-reopen.tmp" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
KOWA_ROOT="$TEST_ROOT" "$CHECK"

sed -i.bak 's/"S01": "BLOCKED"/"S01": "DONE"/' "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
if KOWA_ROOT="$TEST_ROOT" "$CHECK" >/dev/null 2>&1; then
  printf 'expected incomplete-successor-baseline fixture to fail\n' >&2
  exit 1
fi
mv "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md.bak" "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"

printf 'DOC_GOVERNANCE_TEST_PASS\n'
