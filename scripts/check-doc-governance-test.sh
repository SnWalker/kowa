#!/usr/bin/env bash

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
CHECK="$SCRIPT_DIR/check-doc-governance.sh"

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
for track in Kowa后端设计 Kowa前端设计; do
  for record_file in "$PROJECT_ROOT/doc/$track/record/"S[0-9][0-9].md; do
    if [ -f "$record_file" ]; then
      cp "$record_file" "$TEST_ROOT/doc/$track/record/"
    fi
  done
done


# Replace one machine-readable block without depending on the checkout's stage.
replace_json() {
  file=$1 schema=$2 value=$3
  printf '%s\n' "$value" > "$TEST_ROOT/value.json"
  awk -v schema="$schema" -v json_file="$TEST_ROOT/value.json" '
    /^```json$/ { block=$0 ORS; in_json=1; next }
    in_json { block=block $0 ORS; if ($0 == "```") {
      if (index(block, "\"" schema "\"")) { print "```json"; while ((getline line < json_file) > 0) print line; close(json_file); print "```" }
      else printf "%s", block
      in_json=0
    }; next }
    { print }
  ' "$file" > "$TEST_ROOT/json.tmp"
  mv "$TEST_ROOT/json.tmp" "$file"
}
reset_reopen() {
  replace_json "$1" kowa-stage-reopen.v1 ' {
  "schemaVersion": "kowa-stage-reopen.v1",
  "activeReopen": null,
  "suspendedReopen": null
}'
}

# Independent fixtures: the existing gate must reject illegal state, while a
# frozen in-progress successor never acquires currentStage execution authority.
cp -R "$TEST_ROOT/doc" "$TEST_ROOT/original-doc"
STATE="$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
fixture() {
  kind=$1 owner_status=$2
  for track in Kowa后端设计 Kowa前端设计; do
    awk '!/^\| S[0-9][0-9] /' "$PROJECT_ROOT/doc/$track/总体设计与进度.md" > "$TEST_ROOT/doc/$track/总体设计与进度.md"
    reset_reopen "$TEST_ROOT/doc/$track/总体设计与进度.md"
    replace_json "$TEST_ROOT/doc/$track/总体设计与进度.md" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":null}'
    replace_json "$TEST_ROOT/doc/$track/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":null}'
    rm -f "$TEST_ROOT/doc/$track/stage/"S[0-9][0-9]-*.md "$TEST_ROOT/doc/$track/record/"S[0-9][0-9].md
  done
  replace_json "$STATE" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S00"}'
  replace_json "$TEST_ROOT/doc/Kowa后端设计/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":"S00"}'
  replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":"S00","frontendCurrentStage":null}'
  for stage in S00 S01 S02 S03; do
    cp "$PROJECT_ROOT/doc/wiki/operations/stage-template.md" "$TEST_ROOT/doc/Kowa后端设计/stage/$stage-fixture.md"
    sed -E 's/^(\- .*：)$/\1fixture evidence/' "$PROJECT_ROOT/doc/wiki/operations/record-template.md" > "$TEST_ROOT/doc/Kowa后端设计/record/$stage.md"
  done
  middle=DONE
  [ "$kind" != nested ] || middle=BLOCKED
  printf '| S00 | %s | 无 | owner |\n| S01 | %s | S00 | earlier owner |\n| S02 | IN_PROGRESS | S01 | frozen frontier |\n| S03 | NOT_STARTED | S02 | future |\n' "$owner_status" "$middle" >> "$STATE"
  REOPEN=$(jq -n --arg middle "$middle" --arg kind "$kind" '{schemaVersion:"kowa-stage-reopen.v1",activeReopen:{ownerStage:"S00",ownerPreviousStatus:"DONE",previousCurrentStage:(if $kind=="nested" then "S01" else "S02" end),reopenedAt:"2026-09-30T03:58:03Z",successorBaseline:{S01:$middle,S02:"IN_PROGRESS",S03:"NOT_STARTED"}},suspendedReopen:(if $kind=="nested" then {ownerStage:"S01",ownerPreviousStatus:"DONE",previousCurrentStage:"S02",reopenedAt:"2026-09-30T03:50:00Z",successorBaseline:{S02:"IN_PROGRESS",S03:"NOT_STARTED"}} else null end)}')
  replace_json "$STATE" kowa-stage-reopen.v1 "$REOPEN"
}
REOPEN_FAILURES=0
reopen_expect() {
  expected=$1 name=$2 diagnostic=${3:-}
  actual=0
  KOWA_ROOT="$TEST_ROOT" "$CHECK" > "$TEST_ROOT/result.log" 2>&1 || actual=$?
  if [ "$actual" -ne "$expected" ] || { [ -n "$diagnostic" ] && ! grep -Fq "$diagnostic" "$TEST_ROOT/result.log"; }; then
    printf 'REOPEN_CASE_FAIL: %s expected=%s actual=%s\n' "$name" "$expected" "$actual" >&2
    cat "$TEST_ROOT/result.log" >&2
    REOPEN_FAILURES=$((REOPEN_FAILURES + 1))
  fi
}
for kind in ordinary nested; do
  for owner_status in REOPENED IN_PROGRESS; do
    fixture "$kind" "$owner_status"
    reopen_expect 0 "$kind/$owner_status/frozen-IN_PROGRESS"
  done
done
for mutation in drift missing owner current suspended-owner suspended-missing suspended-without-active extra-frontier blocked extra-blocked predecessor baseline-extra; do
  fixture nested IN_PROGRESS
  expression=.
  diagnostic=''
  case "$mutation" in
    drift) sed -i.bak 's/| S02 | IN_PROGRESS |/| S02 | HUMAN_ACTION_REQUIRED |/' "$STATE"; diagnostic='successor baseline S02=IN_PROGRESS differs' ;;
    missing) expression='del(.activeReopen.successorBaseline.S02)'; diagnostic='must freeze every successor' ;;
    owner) expression='.activeReopen.ownerStage="S01"'; diagnostic='differs from currentStage' ;;
    current) replace_json "$STATE" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S02"}'; diagnostic='differs from currentStage' ;;
    suspended-owner) expression='.activeReopen.previousCurrentStage="S02"'; diagnostic='must resume the suspended owner' ;;
    suspended-missing) expression='del(.suspendedReopen.successorBaseline.S02)'; diagnostic='suspendedReopen must freeze every successor' ;;
    suspended-without-active) expression='.activeReopen=null'; diagnostic='cannot exist without activeReopen' ;;
    extra-frontier) sed -i.bak 's/| S03 | NOT_STARTED |/| S03 | IN_PROGRESS |/' "$STATE"; expression='.activeReopen.successorBaseline.S03="IN_PROGRESS" | .suspendedReopen.successorBaseline.S03="IN_PROGRESS"'; diagnostic='extra frozen execution frontier' ;;
    blocked) fixture ordinary IN_PROGRESS; sed -i.bak 's/| S02 | IN_PROGRESS |/| S02 | BLOCKED |/' "$STATE"; expression='.activeReopen.successorBaseline.S02="BLOCKED"'; diagnostic='non-current BLOCKED stage must be suspended owner' ;;
    extra-blocked) sed -i.bak 's/| S02 | IN_PROGRESS |/| S02 | BLOCKED |/' "$STATE"; expression='.activeReopen.successorBaseline.S02="BLOCKED" | .suspendedReopen.successorBaseline.S02="BLOCKED"'; diagnostic='non-current BLOCKED stage must be suspended owner' ;;
    predecessor) sed -i.bak 's/| S00 | IN_PROGRESS |/| S00 | HUMAN_ACTION_REQUIRED |/; s/| S01 | BLOCKED |/| S01 | IN_PROGRESS |/' "$STATE"; expression='.activeReopen=.suspendedReopen | .suspendedReopen=null'; replace_json "$STATE" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S01"}'; diagnostic='non-current stage is not a frozen successor' ;;
    baseline-extra) expression='.activeReopen.successorBaseline.S00="IN_PROGRESS"'; diagnostic='baseline contains a non-successor' ;;
  esac
  replace_json "$STATE" kowa-stage-reopen.v1 "$(printf '%s' "$REOPEN" | jq "$expression")"
  reopen_expect 1 "$mutation" "$diagnostic"
done
fixture nested IN_PROGRESS
sed -i.bak 's/| S00 | IN_PROGRESS |/| S00 | DONE |/; s/| S01 | BLOCKED |/| S01 | REOPENED |/' "$STATE"
replace_json "$STATE" kowa-stage-reopen.v1 "$(printf '%s' "$REOPEN" | jq '.activeReopen=.suspendedReopen | .suspendedReopen=null')"
replace_json "$STATE" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S01"}'
replace_json "$TEST_ROOT/doc/Kowa后端设计/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":"S01"}'
replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":"S01","frontendCurrentStage":null}'
reopen_expect 0 nested/resume-REOPENED
sed -i.bak 's/| S01 | REOPENED |/| S01 | IN_PROGRESS |/' "$STATE"
reopen_expect 0 nested/resume-IN_PROGRESS
sed -i.bak 's/| S01 | IN_PROGRESS |/| S01 | DONE |/' "$STATE"
reset_reopen "$STATE"
replace_json "$STATE" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S02"}'
replace_json "$TEST_ROOT/doc/Kowa后端设计/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":"S02"}'
replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":"S02","frontendCurrentStage":null}'
reopen_expect 0 nested/complete-restore-frontier
if [ "$REOPEN_FAILURES" -ne 0 ]; then
  printf 'REOPEN_TEST_FAILED: %s cases\n' "$REOPEN_FAILURES" >&2
  exit 1
fi
printf 'REOPEN_TEST_PASS: 7 positive, 12 negative cases\n'
rm -rf "$TEST_ROOT/doc"
mv "$TEST_ROOT/original-doc" "$TEST_ROOT/doc"
"$CHECK"

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
  cp "$PROJECT_ROOT/$mutation_file" "$TEST_ROOT/$mutation_file"
  python3 - "$TEST_ROOT/$mutation_file" "$mutation_key" "$mutation_kind" <<'PYMUTATE'
import json, pathlib, re, sys
p, key, kind = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
changed = []
def mutate(match):
    value = json.loads(match[1])
    if key not in value:
        return match[0]
    if kind == "missing":
        value["omitted_" + key] = value.pop(key)
    else:
        value[key] = ""
    changed.append(key)
    return "```json\n" + json.dumps(value, indent=2) + "\n```"
body = re.sub(r"```json\s*\n(.*?)\n```", mutate, p.read_text(), flags=re.S)
assert changed == [key], changed
p.write_text(body)
PYMUTATE
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
  reset_reopen "$TEST_ROOT/doc/$track/总体设计与进度.md"
  replace_json "$TEST_ROOT/doc/$track/总体设计与进度.md" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":null}'
  replace_json "$TEST_ROOT/doc/$track/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":null}'
done
replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":null,"frontendCurrentStage":null}'
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
# A real contract pin for the independent legacy cross-track fixtures.
python3 - "$TEST_ROOT" <<'PYBIND'
import pathlib, json, hashlib
root=pathlib.Path(__import__('sys').argv[1]);directory=root/'api/cross-end/v1';directory.mkdir(parents=True,exist_ok=True)
data=json.dumps({'title':'kowa.cross-end.v1'}).encode();(directory/'schema.json').write_bytes(data)
data=json.dumps({'contract':'kowa.cross-end.v1','owner':'backend:S00','schema':'schema.json','schemaSha256':hashlib.sha256(data).hexdigest(),'operations':{'fixture':'schema'}}).encode();(directory/'delivery.json').write_bytes(data)
p=root/'doc/Kowa前端设计/stage/S00-contract-consumer.md'
import re
s=re.sub(r'^- 本端前置阶段：.*$', '- 本端前置阶段：无。', p.read_text(), flags=re.M)
s=re.sub(r'^- 跨端阶段：.*$', '- 跨端阶段：backend:S00。', s, flags=re.M)
req={'owner':'backend:S00','contract':'kowa.cross-end.v1','operation':'fixture','manifest':'api/cross-end/v1/delivery.json','sha256':hashlib.sha256(data).hexdigest(),'level':'schema','evidence':'doc/Kowa后端设计/record/S00.md'}
s+='\n```json\n'+json.dumps({'schemaVersion':'kowa-stage-consumption.v1','requires':[req]})+'\n```\n';p.write_text(s)
PYBIND
printf '| S00 | NOT_STARTED | 无 | contract owner fixture |\n' >> \
  "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
printf '| S00 | IN_PROGRESS | backend:S00 | contract consumer fixture |\n' >> \
  "$TEST_ROOT/doc/Kowa前端设计/总体设计与进度.md"
for track in Kowa后端设计 Kowa前端设计; do
  replace_json "$TEST_ROOT/doc/$track/总体设计与进度.md" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S00"}'
  replace_json "$TEST_ROOT/doc/$track/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":"S00"}'
done
replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":"S00","frontendCurrentStage":"S00"}'
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
  reset_reopen "$TEST_ROOT/doc/$track/总体设计与进度.md"
  cp "$PROJECT_ROOT/doc/$track/当前阶段与下一步.md" \
     "$TEST_ROOT/doc/$track/当前阶段与下一步.md"
  replace_json "$TEST_ROOT/doc/$track/总体设计与进度.md" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":null}'
  replace_json "$TEST_ROOT/doc/$track/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":null}'
done
cp "$PROJECT_ROOT/doc/当前进展.md" "$TEST_ROOT/doc/当前进展.md"
replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":null,"frontendCurrentStage":null}'
rm -f "$TEST_ROOT/doc/Kowa后端设计/stage/"S[0-9][0-9]-*.md \
      "$TEST_ROOT/doc/Kowa后端设计/record/"S[0-9][0-9].md \
      "$TEST_ROOT/doc/Kowa前端设计/stage/"S[0-9][0-9]-*.md \
      "$TEST_ROOT/doc/Kowa前端设计/record/"S[0-9][0-9].md \
      "$TEST_ROOT/valid-cross-contract-stage.md"

cp "$PROJECT_ROOT/doc/wiki/operations/stage-template.md" \
   "$TEST_ROOT/doc/Kowa后端设计/stage/S00-fixture.md"
printf '| S00 | IN_PROGRESS | 无 | fixture goal |\n' >> "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md"
replace_json "$TEST_ROOT/doc/Kowa后端设计/总体设计与进度.md" kowa-stage-frontier.v1 '{"schemaVersion":"kowa-stage-frontier.v1","currentStage":"S00"}'
replace_json "$TEST_ROOT/doc/Kowa后端设计/当前阶段与下一步.md" kowa-stage-handoff.v1 '{"schemaVersion":"kowa-stage-handoff.v1","currentStage":"S00"}'
replace_json "$TEST_ROOT/doc/当前进展.md" kowa-progress-projection.v1 '{"schemaVersion":"kowa-progress-projection.v1","backendCurrentStage":"S00","frontendCurrentStage":null}'
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

python3 "$SCRIPT_DIR/check-stage-contracts-test.py"
