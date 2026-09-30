#!/usr/bin/env bash

set -u

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_ROOT=${KOWA_ROOT:-$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)}
FAILURES=0

fail() {
  printf 'DOC_GOVERNANCE_FAIL: %s\n' "$1" >&2
  FAILURES=$((FAILURES + 1))
}

require_file() {
  if [ ! -f "$PROJECT_ROOT/$1" ]; then
    fail "missing required file: $1"
  fi
}

require_dir() {
  if [ ! -d "$PROJECT_ROOT/$1" ]; then
    fail "missing required directory: $1"
  fi
}

require_text() {
  file=$1
  pattern=$2
  description=$3
  if [ -f "$PROJECT_ROOT/$file" ] && ! grep -Eq "$pattern" "$PROJECT_ROOT/$file"; then
    fail "$file: missing $description"
  fi
}

REQUIRED_FILES="
AGENTS.md
mise.toml
doc/common.md
doc/decision.md
doc/bootstrap-record.md
doc/当前进展.md
doc/wiki/index.md
doc/wiki/architecture/index.md
doc/wiki/contracts/index.md
doc/wiki/design/index.md
doc/wiki/domain/index.md
doc/wiki/glossary/index.md
doc/wiki/modules/index.md
doc/wiki/operations/index.md
doc/wiki/operations/ai-staged-delivery.md
doc/wiki/operations/stage-template.md
doc/wiki/operations/record-template.md
doc/Kowa后端设计/总体设计与进度.md
doc/Kowa后端设计/当前阶段与下一步.md
doc/Kowa后端设计/验证规则.md
doc/Kowa前端设计/总体设计与进度.md
doc/Kowa前端设计/当前阶段与下一步.md
doc/Kowa前端设计/验证规则.md
"

for path in $REQUIRED_FILES; do
  require_file "$path"
done

REQUIRED_DIRS="
doc/Kowa后端设计/stage
doc/Kowa后端设计/record
doc/Kowa前端设计/stage
doc/Kowa前端设计/record
doc/Kowa验收与运行
"

for path in $REQUIRED_DIRS; do
  require_dir "$path"
done

require_text "AGENTS.md" '^### 1\. 固定恢复入口$' 'fixed recovery entry'
require_text "AGENTS.md" '^### 5\. 测试与验证闭环$' 'verification loop'
require_text "AGENTS.md" '^### 7\. 工作区纪律$' 'workspace discipline'
require_text "doc/common.md" '^### 0\.4 维护规则$' 'common maintenance rules'
require_text "doc/wiki/index.md" '^## 页面登记格式$' 'wiki registration format'
require_text "doc/wiki/operations/stage-template.md" '^## 阶段设计依据$' 'stage design basis'
require_text "doc/wiki/operations/stage-template.md" '^## 必读$' 'required reading'
require_text "doc/wiki/operations/stage-template.md" '^## 推荐 Skills$' 'recommended skills'
require_text "doc/wiki/operations/stage-template.md" '^## 核心文件与修改范围$' 'core files and change scope'
require_text "doc/wiki/operations/stage-template.md" '^## 本阶段新增或修改的模型$' 'stage-owned models'
require_text "doc/wiki/operations/stage-template.md" '^## 约束$' 'stage constraints'
require_text "doc/wiki/operations/stage-template.md" '^## 运行时验收$' 'runtime acceptance'
require_text "doc/wiki/operations/stage-template.md" '^## 停止点$' 'stage stop point'
require_text "doc/wiki/operations/record-template.md" '^- 计划主线：$' 'record plan throughline'
require_text "doc/wiki/operations/record-template.md" '^- 状态变化：$' 'record status transition'
require_text "doc/wiki/operations/record-template.md" '^- 决策触发事实、`doc/decision\.md` 链接及其对本阶段的实际影响：$' 'record decision impact link'
require_text "doc/wiki/operations/record-template.md" '^- 用户提供的 task / WorkflowRun / artifact / PR / 数据 ID：$' 'record user-provided IDs'
require_text "doc/wiki/operations/record-template.md" '^- 下一会话提示词：$' 'record next-session prompt'
validate_toolchain_versions() {
  mise_file="$PROJECT_ROOT/mise.toml"
  common_file="$PROJECT_ROOT/doc/common.md"
  [ -f "$mise_file" ] && [ -f "$common_file" ] || return

  mise_go=$(sed -nE 's/^go[[:space:]]*=[[:space:]]*"([^"]+)"$/\1/p' "$mise_file")
  mise_node=$(sed -nE 's/^node[[:space:]]*=[[:space:]]*"([^"]+)"$/\1/p' "$mise_file")
  mise_pnpm=$(sed -nE 's/^pnpm[[:space:]]*=[[:space:]]*"([^"]+)"$/\1/p' "$mise_file")
  common_go=$(sed -nE 's/^- 控制面与 Runner 使用 Go ([0-9.]+)。$/\1/p' "$common_file")
  common_node=$(sed -nE 's/^- Web UI 使用 Node\.js ([0-9.]+)、pnpm ([0-9.]+)、.*$/\1/p' "$common_file")
  common_pnpm=$(sed -nE 's/^- Web UI 使用 Node\.js ([0-9.]+)、pnpm ([0-9.]+)、.*$/\2/p' "$common_file")

  for pair in \
    "Go|$mise_go|$common_go" \
    "Node.js|$mise_node|$common_node" \
    "pnpm|$mise_pnpm|$common_pnpm"; do
    name=${pair%%|*}
    values=${pair#*|}
    mise_value=${values%%|*}
    common_value=${values#*|}
    if [ -z "$mise_value" ] || [ -z "$common_value" ]; then
      fail "toolchain version missing for $name in mise.toml or doc/common.md"
    elif [ "$mise_value" != "$common_value" ]; then
      fail "toolchain version mismatch for $name: mise.toml=$mise_value common.md=$common_value"
    fi
  done
}

validate_toolchain_versions

extract_json_block() {
  file=$1
  schema=$2
  awk -v schema="$schema" '
    /^```json[[:space:]]*$/ { in_block = 1; block = ""; next }
    in_block && /^```[[:space:]]*$/ {
      if (index(block, schema) > 0) {
        printf "%s", block
        exit
      }
      in_block = 0
      block = ""
      next
    }
    in_block { block = block $0 ORS }
  ' "$file"
}

require_filled_field() {
  rel=$1
  label=$2
  if [ -f "$PROJECT_ROOT/$rel" ] && ! grep -Eq "^-[[:space:]]*${label}：[[:space:]]*[^[:space:]].*" "$PROJECT_ROOT/$rel"; then
    fail "$rel: missing filled field $label"
  fi
}

require_cross_contract_binding() {
  local rel=$1
  local file="$PROJECT_ROOT/$rel"
  local owner
  local frozen
  [ -f "$file" ] || return

  owner=$(sed -nE 's/^- 共享契约 owner：[[:space:]]*(.+)$/\1/p' "$file" | head -n 1)
  frozen=$(sed -nE 's/^- 冻结契约及版本：[[:space:]]*(.+)$/\1/p' "$file" | head -n 1)

  if ! printf '%s\n' "$owner" | grep -Eq '(backend|frontend):S[0-9]{2}'; then
    fail "$rel: cross-track dependency requires a concrete shared-contract owner such as backend:S00"
  fi
  if ! printf '%s\n' "$frozen" | grep -Eq '(^|[^[:alnum:]_])(v[0-9]+([.][0-9]+)*|version[=:：[:space:]]+[0-9]+([.][0-9]+)*|版本[=:：[:space:]]+[0-9]+([.][0-9]+)*|sha256:[0-9a-fA-F]{64})($|[^[:alnum:]_])'; then
    fail "$rel: cross-track dependency requires a frozen contract version or sha256 digest"
  fi
}

stage_status() {
  local state_file=$1
  local target_stage=$2
  awk -F'|' -v target="$target_stage" '
    function trim(value) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      return value
    }
    /^\|/ {
      stage = trim($2)
      status = trim($3)
      if (stage == target) {
        print status
        exit
      }
    }
  ' "$state_file"
}

validate_state_file() {
  rel=$1
  file="$PROJECT_ROOT/$rel"
  [ -f "$file" ] || return

  result=$(awk -F'|' '
    function trim(value) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      return value
    }
    BEGIN { errors = 0 }
    /^\|/ {
      stage = trim($2)
      status = trim($3)
      prerequisites = trim($4)
      goal = trim($5)
      if (stage ~ /^S[0-9][0-9]$/) {
        if (seen[stage]++) {
          printf "duplicate stage %s", stage
          errors++
        }
        if (status != "NOT_STARTED" && status != "IN_PROGRESS" &&
            status != "HUMAN_ACTION_REQUIRED" && status != "BLOCKED" &&
            status != "DONE" && status != "REOPENED") {
          printf "invalid status %s for %s", status, stage
          errors++
        }
        if (prerequisites == "") {
          printf "missing prerequisites for %s", stage
          errors++
        }
        if (goal == "") {
          printf "missing unique goal for %s", stage
          errors++
        }
      }
    }
    END {
      exit(errors > 0 ? 1 : 0)
    }
  ' "$file" 2>&1)
  if [ $? -ne 0 ]; then
    fail "$rel: $result"
  fi
}

validate_track() {
  track=$1
  state_rel="doc/${track}/总体设计与进度.md"
  handoff_rel="doc/${track}/当前阶段与下一步.md"
  stage_rel="doc/${track}/stage"
  record_rel="doc/${track}/record"
  state_file="$PROJECT_ROOT/$state_rel"
  handoff_file="$PROJECT_ROOT/$handoff_rel"

  validate_state_file "$state_rel"
  [ -f "$state_file" ] || return

  frontier_json=$(extract_json_block "$state_file" "kowa-stage-frontier.v1")
  if [ -z "$frontier_json" ] || ! printf '%s\n' "$frontier_json" | jq -e '
      type == "object" and
      .schemaVersion == "kowa-stage-frontier.v1" and has("currentStage") and
      (.currentStage == null or (.currentStage | type == "string" and test("^S[0-9]{2}$")))
    ' >/dev/null 2>&1; then
    fail "$state_rel: invalid current-stage frontier"
    current_stage=""
  else
    current_stage=$(printf '%s\n' "$frontier_json" | jq -r '.currentStage // empty')
  fi

  handoff_json=$(extract_json_block "$handoff_file" "kowa-stage-handoff.v1")
  if [ -z "$handoff_json" ] || ! printf '%s\n' "$handoff_json" | jq -e '
      type == "object" and
      .schemaVersion == "kowa-stage-handoff.v1" and has("currentStage") and
      (.currentStage == null or (.currentStage | type == "string" and test("^S[0-9]{2}$")))
    ' >/dev/null 2>&1; then
    fail "$handoff_rel: invalid handoff projection"
    handoff_stage=""
  else
    handoff_stage=$(printf '%s\n' "$handoff_json" | jq -r '.currentStage // empty')
  fi

  if [ "$handoff_stage" != "$current_stage" ]; then
    fail "$handoff_rel: currentStage=$handoff_stage differs from authoritative $current_stage"
  fi

  reopen_json=$(extract_json_block "$state_file" "kowa-stage-reopen.v1")
  reopen_valid=1
  if [ -z "$reopen_json" ] || ! printf '%s\n' "$reopen_json" | jq -e '
      type == "object" and
      .schemaVersion == "kowa-stage-reopen.v1" and
      has("activeReopen") and has("suspendedReopen") and
      ((.activeReopen == null) or (.activeReopen | type == "object")) and
      ((.suspendedReopen == null) or (.suspendedReopen | type == "object"))
    ' >/dev/null 2>&1; then
    fail "$state_rel: invalid reopen JSON"
    reopen_valid=0
  fi

  for stage_file in "$PROJECT_ROOT/$stage_rel"/S[0-9][0-9]-*.md; do
    [ -e "$stage_file" ] || continue
    stage=$(basename "$stage_file" | sed -E 's/^(S[0-9][0-9])-.*/\1/')
    if ! grep -Eq "^\|[[:space:]]*${stage}[[:space:]]*\|" "$state_file"; then
      fail "$stage_rel: $stage_file has no state-table row"
    fi
    stage_doc=${stage_file#$PROJECT_ROOT/}
    for requirement in \
      '阶段设计依据|stage design basis' \
      '现行勘误|current correction' \
      '唯一工程目标|unique engineering goal' \
      '前置与跨端依赖|prerequisites and cross-track dependencies' \
      '必读|required reading' \
      '推荐 Skills|recommended skills' \
      '核心文件与修改范围|core files and change scope' \
      '当前问题与证据|current evidence' \
      'Producer、Transition、Consumer|producer transition consumer' \
      '本阶段新增或修改的模型|stage-owned models' \
      '约束|stage constraints' \
      '实施清单|implementation checklist' \
      '测试与自动验证|tests and automatic verification' \
      '运行时验收|runtime acceptance' \
      '人工操作边界|human-operation boundary' \
      '退出条件|exit criteria' \
      '停止点|stop point'; do
      heading=${requirement%%|*}
      description=${requirement#*|}
      require_text "$stage_doc" "^## ${heading}(（.*）)?$" "$description"
    done
  done

  stage_rows=$(awk -F'|' '
    function trim(value) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      return value
    }
    /^\|/ {
      stage = trim($2)
      status = trim($3)
      prerequisites = trim($4)
      goal = trim($5)
      if (stage ~ /^S[0-9][0-9]$/) print stage "|" status "|" prerequisites "|" goal
    }
  ' "$state_file")

  reopened=0
  non_current_nonterminal=""
  stage_count=0
  while IFS= read -r row; do
    [ -n "$row" ] || continue
    stage=${row%%|*}
    remaining=${row#*|}
    status=${remaining%%|*}
    remaining=${remaining#*|}
    prerequisites=${remaining%%|*}
    stage_count=$((stage_count + 1))
    matches=$(find "$PROJECT_ROOT/$stage_rel" -maxdepth 1 -type f -name "${stage}-*.md" | wc -l | tr -d ' ')
    if [ "$matches" -ne 1 ]; then
      fail "$state_rel: $stage must have exactly one stage contract, found $matches"
      stage_doc=""
    else
      stage_doc=$(find "$PROJECT_ROOT/$stage_rel" -maxdepth 1 -type f -name "${stage}-*.md")
      stage_doc=${stage_doc#$PROJECT_ROOT/}
    fi

    if [ "$status" != "NOT_STARTED" ]; then
      if [ ! -f "$PROJECT_ROOT/$record_rel/${stage}.md" ]; then
        fail "$state_rel: $stage with status $status must have $record_rel/${stage}.md"
      else
        require_text "$record_rel/${stage}.md" '^## 执行时间线$' 'execution timeline'
      fi
    fi

    case "$status" in
      IN_PROGRESS|HUMAN_ACTION_REQUIRED|BLOCKED|REOPENED)
        if [ "$stage" != "$current_stage" ]; then
          if [ -n "$non_current_nonterminal" ]; then
            non_current_nonterminal="$non_current_nonterminal $stage:$status"
          else
            non_current_nonterminal="$stage:$status"
          fi
        fi
        ;;
    esac

    if [ "$status" = "REOPENED" ]; then
      reopened=$((reopened + 1))
    fi

    if [ "$status" = "BLOCKED" ]; then
      require_filled_field "$record_rel/${stage}.md" 'BLOCKED 复现方式'
      require_filled_field "$record_rel/${stage}.md" 'BLOCKED 影响与本阶段不可消除依据'
      require_filled_field "$record_rel/${stage}.md" 'BLOCKED 解除条件和责任人'
    fi

    if [ "$status" = "REOPENED" ] && ! grep -Eq '^- `(CODE_CONFIRMED|DATA_CONFIRMED)`：[[:space:]]*[^[:space:]].*' "$PROJECT_ROOT/$record_rel/${stage}.md"; then
      fail "$record_rel/${stage}.md: REOPENED requires filled CODE_CONFIRMED or DATA_CONFIRMED evidence"
    fi

    if [ "$status" = "DONE" ]; then
      require_filled_field "$record_rel/${stage}.md" '开始 HEAD'
      require_filled_field "$record_rel/${stage}.md" '预存工作区'
      require_filled_field "$record_rel/${stage}.md" '实际修改边界'
      require_filled_field "$record_rel/${stage}.md" '自动验证命令、退出码与测试数量'
      require_filled_field "$record_rel/${stage}.md" '残余风险和未完成项'
      require_filled_field "$record_rel/${stage}.md" '阶段退出结论'
      require_filled_field "$record_rel/${stage}.md" '下一阶段准入结论'
    fi

    normalized_prerequisites=$(printf '%s\n' "$prerequisites" | sed -E 's/[、,]/ /g')
    for prerequisite in $normalized_prerequisites; do
      case "$prerequisite" in
        无|-)
          ;;
        S[0-9][0-9])
          if ! grep -Eq "^\|[[:space:]]*${prerequisite}[[:space:]]*\|" "$state_file"; then
            fail "$state_rel: $stage references missing prerequisite $prerequisite"
          fi
          ;;
        backend:S[0-9][0-9])
          prerequisite_stage=${prerequisite#backend:}
          if ! grep -Eq "^\|[[:space:]]*${prerequisite_stage}[[:space:]]*\|" "$PROJECT_ROOT/doc/Kowa后端设计/总体设计与进度.md"; then
            fail "$state_rel: $stage references missing cross-track prerequisite $prerequisite"
          elif [ "$status" != "NOT_STARTED" ] && [ "$(stage_status "$PROJECT_ROOT/doc/Kowa后端设计/总体设计与进度.md" "$prerequisite_stage")" != "DONE" ]; then
            fail "$state_rel: $stage cannot be $status until cross-track prerequisite $prerequisite is DONE"
          fi
          [ -z "$stage_doc" ] || require_cross_contract_binding "$stage_doc"
          ;;
        frontend:S[0-9][0-9])
          prerequisite_stage=${prerequisite#frontend:}
          if ! grep -Eq "^\|[[:space:]]*${prerequisite_stage}[[:space:]]*\|" "$PROJECT_ROOT/doc/Kowa前端设计/总体设计与进度.md"; then
            fail "$state_rel: $stage references missing cross-track prerequisite $prerequisite"
          elif [ "$status" != "NOT_STARTED" ] && [ "$(stage_status "$PROJECT_ROOT/doc/Kowa前端设计/总体设计与进度.md" "$prerequisite_stage")" != "DONE" ]; then
            fail "$state_rel: $stage cannot be $status until cross-track prerequisite $prerequisite is DONE"
          fi
          [ -z "$stage_doc" ] || require_cross_contract_binding "$stage_doc"
          ;;
        *)
          fail "$state_rel: $stage has invalid prerequisite token $prerequisite"
          ;;
      esac
    done
  done <<< "$stage_rows"

  if [ "$stage_count" -eq 0 ] && [ -n "$current_stage" ]; then
    fail "$state_rel: currentStage must be null before the first stage exists"
  elif [ "$stage_count" -gt 0 ]; then
    if [ -z "$current_stage" ]; then
      fail "$state_rel: currentStage is required when stages exist"
    elif ! grep -Eq "^\|[[:space:]]*${current_stage}[[:space:]]*\|" "$state_file"; then
      fail "$state_rel: currentStage $current_stage has no state-table row"
    fi
  fi

  if [ "$reopen_valid" -eq 1 ]; then
    active_owner=""
    previous_current_stage=""
    active_type=$(printf '%s\n' "$reopen_json" | jq -r '.activeReopen | type')
    suspended_type=$(printf '%s\n' "$reopen_json" | jq -r '.suspendedReopen | type')
    if [ "$active_type" = "null" ]; then
      if [ "$reopened" -ne 0 ]; then
        fail "$state_rel: activeReopen must describe the REOPENED stage"
      fi
      if [ "$suspended_type" != "null" ]; then
        fail "$state_rel: suspendedReopen cannot exist without activeReopen"
      fi
      if [ -n "$non_current_nonterminal" ]; then
        fail "$state_rel: non-current nonterminal stage(s) lack suspendedReopen: $non_current_nonterminal"
      fi
      if [ "$stage_count" -gt 0 ]; then
        expected_current_stage=$(awk -F'|' '
          function trim(value) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
            return value
          }
          /^\|/ {
            stage = trim($2)
            status = trim($3)
            if (stage ~ /^S[0-9][0-9]$/) {
              last = stage
              if (expected == "" && status != "DONE") expected = stage
            }
          }
          END { print (expected == "" ? last : expected) }
        ' "$state_file")
        if [ "$current_stage" != "$expected_current_stage" ]; then
          fail "$state_rel: currentStage must be the first non-DONE stage, or the last stage when all are DONE; expected $expected_current_stage"
        fi
        normal_sequence_error=$(awk -F'|' -v current="$current_stage" '
          function trim(value) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
            return value
          }
          /^\|/ {
            stage = trim($2)
            status = trim($3)
            if (stage !~ /^S[0-9][0-9]$/) next
            if (stage < current && status != "DONE") {
              print stage " predecessor is " status
              exit 1
            }
            if (stage > current && status != "NOT_STARTED") {
              print stage " successor is " status
              exit 1
            }
          }
        ' "$state_file" 2>&1) || fail "$state_rel: invalid normal stage sequence: $normal_sequence_error"
      fi
    else
      if ! printf '%s\n' "$reopen_json" | jq -e '
          .activeReopen as $reopen |
          ($reopen | type) == "object" and
          ($reopen.ownerStage | type) == "string" and ($reopen.ownerStage | length) > 0 and
          $reopen.ownerPreviousStatus == "DONE" and
          (($reopen.previousCurrentStage == null) or (($reopen.previousCurrentStage | type) == "string")) and
          (($reopen.reopenedAt | type) == "string" and
            ($reopen.reopenedAt | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$"))) and
          ($reopen.successorBaseline | type) == "object"
        ' >/dev/null 2>&1; then
        fail "$state_rel: activeReopen schema is incomplete or ownerPreviousStatus is not DONE"
      else
        active_owner=$(printf '%s\n' "$reopen_json" | jq -r '.activeReopen.ownerStage')
        previous_current_stage=$(printf '%s\n' "$reopen_json" | jq -r '.activeReopen.previousCurrentStage // empty')
        if [ "$active_owner" != "$current_stage" ]; then
          fail "$state_rel: activeReopen owner $active_owner differs from currentStage $current_stage"
        fi
        if ! grep -Eq "^\|[[:space:]]*${active_owner}[[:space:]]*\|[[:space:]]*(REOPENED|IN_PROGRESS|HUMAN_ACTION_REQUIRED|BLOCKED)[[:space:]]*\|" "$state_file"; then
          fail "$state_rel: activeReopen owner $active_owner must be the active rework stage"
        fi
        if [ -n "$previous_current_stage" ] && ! grep -Eq "^\|[[:space:]]*${previous_current_stage}[[:space:]]*\|" "$state_file"; then
          fail "$state_rel: activeReopen previousCurrentStage $previous_current_stage has no state-table row"
        fi
        baseline_rows=$(printf '%s\n' "$reopen_json" | jq -r '.activeReopen.successorBaseline | to_entries[] | "\(.key)=\(.value)"')
        for baseline in $baseline_rows; do
          baseline_stage=${baseline%%=*}
          baseline_status=${baseline#*=}
          if ! [[ "$baseline_stage" =~ ^S[0-9][0-9]$ ]] || [[ "$baseline_stage" < "$active_owner" ]] || [ "$baseline_stage" = "$active_owner" ]; then
            fail "$state_rel: active baseline contains a non-successor: $baseline_stage"
          fi
          if ! grep -Eq "^\|[[:space:]]*${baseline_stage}[[:space:]]*\|[[:space:]]*${baseline_status}[[:space:]]*\|" "$state_file"; then
            fail "$state_rel: successor baseline $baseline_stage=$baseline_status differs from state table"
          fi
        done
        expected_successors=$(awk -F'|' -v owner="$active_owner" '
          function trim(value) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
            return value
          }
          /^\|/ {
            stage = trim($2)
            status = trim($3)
            if (stage ~ /^S[0-9][0-9]$/ && stage > owner) print stage "=" status
          }
        ' "$state_file")
        for successor in $expected_successors; do
          successor_stage=${successor%%=*}
          successor_status=${successor#*=}
          actual_status=$(printf '%s\n' "$reopen_json" | jq -r --arg stage "$successor_stage" '.activeReopen.successorBaseline[$stage] // empty')
          if [ "$actual_status" != "$successor_status" ]; then
            fail "$state_rel: activeReopen must freeze every successor; expected $successor_stage=$successor_status"
          fi
        done
      fi
      if [ "$suspended_type" != "null" ] && ! printf '%s\n' "$reopen_json" | jq -e '
          .suspendedReopen as $reopen |
          ($reopen | type) == "object" and
          ($reopen.ownerStage | type) == "string" and ($reopen.ownerStage | length) > 0 and
          $reopen.ownerPreviousStatus == "DONE" and
          (($reopen.previousCurrentStage == null) or (($reopen.previousCurrentStage | type) == "string")) and
          (($reopen.reopenedAt | type) == "string" and
            ($reopen.reopenedAt | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$"))) and
          ($reopen.successorBaseline | type) == "object"
        ' >/dev/null 2>&1; then
        fail "$state_rel: suspendedReopen schema is incomplete or ownerPreviousStatus is not DONE"
      elif [ "$suspended_type" != "null" ]; then
        suspended_owner=$(printf '%s\n' "$reopen_json" | jq -r '.suspendedReopen.ownerStage')
        suspended_previous=$(printf '%s\n' "$reopen_json" | jq -r '.suspendedReopen.previousCurrentStage // empty')
        if ! grep -Eq "^\|[[:space:]]*${suspended_owner}[[:space:]]*\|[[:space:]]*BLOCKED[[:space:]]*\|" "$state_file"; then
          fail "$state_rel: suspendedReopen owner $suspended_owner must be BLOCKED"
        fi
        if [[ "$suspended_owner" < "$active_owner" ]] || [ "$suspended_owner" = "$active_owner" ] || [ "$previous_current_stage" != "$suspended_owner" ]; then
          fail "$state_rel: activeReopen must resume the suspended owner after the earlier owner finishes"
        fi
        if [ -n "$suspended_previous" ] && ! grep -Eq "^\|[[:space:]]*${suspended_previous}[[:space:]]*\|" "$state_file"; then
          fail "$state_rel: suspendedReopen previousCurrentStage $suspended_previous has no state-table row"
        fi
        suspended_baseline_rows=$(printf '%s\n' "$reopen_json" | jq -r '.suspendedReopen.successorBaseline | to_entries[] | "\(.key)=\(.value)"')
        for baseline in $suspended_baseline_rows; do
          baseline_stage=${baseline%%=*}
          baseline_status=${baseline#*=}
          if ! [[ "$baseline_stage" =~ ^S[0-9][0-9]$ ]] || [[ "$baseline_stage" < "$suspended_owner" ]] || [ "$baseline_stage" = "$suspended_owner" ]; then
            fail "$state_rel: suspended baseline contains a non-successor: $baseline_stage"
          fi
          if ! grep -Eq "^\|[[:space:]]*${baseline_stage}[[:space:]]*\|[[:space:]]*${baseline_status}[[:space:]]*\|" "$state_file"; then
            fail "$state_rel: suspended successor baseline $baseline_stage=$baseline_status differs from state table"
          fi
        done
        expected_suspended_successors=$(awk -F'|' -v owner="$suspended_owner" '
          function trim(value) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
            return value
          }
          /^\|/ {
            stage = trim($2)
            status = trim($3)
            if (stage ~ /^S[0-9][0-9]$/ && stage > owner) print stage "=" status
          }
        ' "$state_file")
        for successor in $expected_suspended_successors; do
          successor_stage=${successor%%=*}
          successor_status=${successor#*=}
          actual_status=$(printf '%s\n' "$reopen_json" | jq -r --arg stage "$successor_stage" '.suspendedReopen.successorBaseline[$stage] // empty')
          if [ "$actual_status" != "$successor_status" ]; then
            fail "$state_rel: suspendedReopen must freeze every successor; expected $successor_stage=$successor_status"
          fi
        done
      fi
      # Baseline equality does not grant execution authority. Only the former
      # frontier may remain in progress; the suspended owner alone may BLOCK.
      frozen_frontier=$(printf '%s\n' "$reopen_json" | jq -r '(.suspendedReopen // .activeReopen).previousCurrentStage // empty')
      suspended_owner=$(printf '%s\n' "$reopen_json" | jq -r '.suspendedReopen.ownerStage // empty')
      for entry in $non_current_nonterminal; do
        stage=${entry%%:*}
        status=${entry#*:}
        if [ "$status" = "BLOCKED" ]; then
          if [ "$stage" != "$suspended_owner" ]; then
            fail "$state_rel: non-current BLOCKED stage must be suspended owner: $entry"
          fi
        elif [[ "$stage" < "$current_stage" ]] || [ "$(printf '%s\n' "$reopen_json" | jq -r --arg stage "$stage" '.activeReopen.successorBaseline[$stage] // empty')" != "$status" ]; then
          fail "$state_rel: non-current stage is not a frozen successor: $entry"
        elif [ "$stage" != "$frozen_frontier" ] || [ "$status" = "REOPENED" ]; then
          fail "$state_rel: extra frozen execution frontier: $entry"
        fi
      done
    fi
  fi

  for record_file in "$PROJECT_ROOT/$record_rel"/S[0-9][0-9].md; do
    [ -e "$record_file" ] || continue
    stage=$(basename "$record_file" .md)
    if ! grep -Eq "^\|[[:space:]]*${stage}[[:space:]]*\|" "$state_file"; then
      fail "$record_rel: $record_file has no state-table row"
    fi
    require_text "${record_file#$PROJECT_ROOT/}" '^## 执行时间线$' 'execution timeline'
  done

  if [ "$track" = "Kowa后端设计" ]; then
    backend_current_stage=$current_stage
  else
    frontend_current_stage=$current_stage
  fi
}

backend_current_stage=""
frontend_current_stage=""
validate_track "Kowa后端设计"
validate_track "Kowa前端设计"

progress_file="$PROJECT_ROOT/doc/当前进展.md"
progress_json=$(extract_json_block "$progress_file" "kowa-progress-projection.v1")
if [ -z "$progress_json" ] || ! printf '%s\n' "$progress_json" | jq -e '
    type == "object" and
    .schemaVersion == "kowa-progress-projection.v1" and
    has("backendCurrentStage") and has("frontendCurrentStage") and
    ((.backendCurrentStage == null) or (.backendCurrentStage | type == "string" and test("^S[0-9]{2}$"))) and
    ((.frontendCurrentStage == null) or (.frontendCurrentStage | type == "string" and test("^S[0-9]{2}$")))
  ' >/dev/null 2>&1; then
  fail "doc/当前进展.md: invalid progress projection"
else
  progress_backend=$(printf '%s\n' "$progress_json" | jq -r '.backendCurrentStage // empty')
  progress_frontend=$(printf '%s\n' "$progress_json" | jq -r '.frontendCurrentStage // empty')
  if [ "$progress_backend" != "$backend_current_stage" ]; then
    fail "doc/当前进展.md: backend projection $progress_backend differs from $backend_current_stage"
  fi
  if [ "$progress_frontend" != "$frontend_current_stage" ]; then
    fail "doc/当前进展.md: frontend projection $progress_frontend differs from $frontend_current_stage"
  fi
fi

for category_dir in "$PROJECT_ROOT"/doc/wiki/*; do
  [ -d "$category_dir" ] || continue
  index="$category_dir/index.md"
  [ -f "$index" ] || continue
  for page in "$category_dir"/*.md; do
    [ -e "$page" ] || continue
    [ "$(basename "$page")" = "index.md" ] && continue
    name=$(basename "$page")
    if ! grep -Fq "($name)" "$index"; then
      fail "${index#$PROJECT_ROOT/}: unregistered page $name"
    fi
  done
done

FORMAL_TEXT_FILES="
AGENTS.md
mise.toml
scripts/check-doc-governance.sh
scripts/check-doc-governance-test.sh
doc/common.md
doc/decision.md
doc/bootstrap-record.md
doc/当前进展.md
"

for rel in $FORMAL_TEXT_FILES; do
  file="$PROJECT_ROOT/$rel"
  [ -f "$file" ] || continue
  if LC_ALL=C grep -nE '[[:blank:]]+$' "$file" >/dev/null; then
    fail "$rel: contains trailing whitespace"
  fi
  if [ -s "$file" ] && [ "$(tail -c 1 "$file" | wc -l | tr -d ' ')" -eq 0 ]; then
    fail "$rel: missing final newline"
  fi
done

for root in "$PROJECT_ROOT/doc/wiki" "$PROJECT_ROOT/doc/research" "$PROJECT_ROOT/doc/Kowa后端设计" "$PROJECT_ROOT/doc/Kowa前端设计" "$PROJECT_ROOT/doc/Kowa验收与运行"; do
  [ -d "$root" ] || continue
  while IFS= read -r file; do
    rel=${file#$PROJECT_ROOT/}
    if LC_ALL=C grep -nE '[[:blank:]]+$' "$file" >/dev/null; then
      fail "$rel: contains trailing whitespace"
    fi
    if [ -s "$file" ] && [ "$(tail -c 1 "$file" | wc -l | tr -d ' ')" -eq 0 ]; then
      fail "$rel: missing final newline"
    fi
  done < <(find "$root" -type f -name '*.md' -print)
done

if [ "$FAILURES" -ne 0 ]; then
  printf 'DOC_GOVERNANCE_FAILED: %s issue(s)\n' "$FAILURES" >&2
  exit 1
fi

printf 'DOC_GOVERNANCE_PASS\n'
