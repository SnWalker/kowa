# S02：WorkItem 与知识绑定

> 状态只在总体设计维护；执行事实进入 record/S02.md。

## 阶段设计依据

MVP 创建入口只有一个固定流程，知识必须先推荐、可预览并由用户显式确认，失败不能显示成空知识。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现 WorkItem 创建、唯一固定流程启动入口及版本化知识推荐/选择/确认体验。

## 前置与跨端依赖

- 本端前置阶段：S01。
- 跨端阶段：backend:S01、backend:S05、backend:S06。
- 共享契约 owner：backend:S01（`kowa.identity-workspace.v1` 的 `workspace.config` 只读复用）、backend:S05（`kowa.workitem.v1`、`kowa.delivery-governance.v1` 的 HumanTask）、backend:S06（`kowa.web-application.v1` 的运行准入与组合运行查询）；逐操作责任见消费准入。
- 冻结契约及版本：四个 v1 合同均由对应 owner 冻结于 main，并以 manifest 文件 SHA-256 与 `schemaSha256` 双重 pin（见消费准入块），准入证据见 [record/S02-消费准入证据](<../record/S02-消费准入证据.md>)。
- 间接影响：backend:S02（Workflow/Execution 核心与 Task wire）与 backend:S03（冻结知识输入与不可变 Artifact）是这些操作背后的模块生产者，其事实经 backend:S06 的 `kowa.web-application.v1` 与 backend:S05 的 HumanTask 载体到达浏览器；两者没有可绑定的本阶段 HTTP 操作（见准入勘误 G1），属间接责任，不构成本阶段直接消费绑定。

## 必读

MVP 流程、前端信息架构、backend:S02/S03 合同与 record、候选合同知识章节、验收 A01—A06/C01—C03/C08。

### 2026-10-16 准入解除与合同绑定更新（CODE_CONFIRMED）

`backend:S06` 已 `DONE` 并合入 main（PR #33，HEAD `6d88be1`），本阶段唯一缺口解除；开工前按固定恢复顺序完成审计，并在该 HEAD 上**重算**全部双 pin（未沿用候选值）。旧 `消费准入` 中两条 `level=application` 的死条目（`backend:S02 run.view`、`backend:S03 snapshot.read`）在固定 SHA 与 main 均无对应 manifest 文件（全仓仅 7 个 `delivery.json`），已按 G1 移除并以真实 HTTP 载体替换（运行/历史读取 → backend:S06 `run.detail-history`；知识内容读取 → `intake.start-knowledge` 响应 + backend:S05 `human.detail`）；同时按 G2/G3 补登 `workitem.list`、`workitem.detail`、`human.detail`、`human.list`，并按 `workspace.config` 的实际消费补登 backend:S01。因 backend:S02/S03 不再有可绑定的 HTTP 操作，跨端依赖收敛为 backend:S01/S05/S06（与 S01 对 backend:S02 的处置同例）。这是合同绑定更新，不重开任何 `DONE` 阶段；依据见 [record/S02-消费准入证据](<../record/S02-消费准入证据.md>) §3。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

`src/workitems/`（列表/创建/详情页、知识绑定确认、展示辅助）、`src/api/{contracts,decode,client}.ts`（S02 合同类型、严格解码、客户端方法）、`src/app/{App,AppShell}.tsx` 与 `src/styles/global.css`（路由/导航/样式）、`tests/fixtures/`（合同绑定 fixture）、`tests/unit/`（组件与解码测试）、`tests/e2e/`（旅程与 HTTP 双）、`tests/webharness/main.go`（test-only 控制面装配）。

### 允许修改

单仓需求与验收标准、固定流程提示、推荐/搜索/增删/确认、内容摘要和版本展示、失败恢复。（实际实现范围同上。）

### 明确禁止

多模板选择、多仓写入、默认值代替确认、向量检索假设、未确认内容驱动下游。

### 并行写入范围

独占创建/知识页面；只消费下列明确 owner 的冻结操作。

## 当前问题与证据

- CONFIRMED：单流程、双仓和知识确认语义已冻结。
- CODE_CONFIRMED：进入时无对应页面。
- DATA_CONFIRMED：无真实知识仓运行。
- STRONG_INFERENCE：内容快照与确认事件分离可直接映射 UI。
- HYPOTHESIS：大文档预览性能需实现时测量。
- 文档与源码差异：以合同 fixture 为准。
- 证据不足项：真实私有知识仓错误体验。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

用户输入和后端推荐生产候选；UI 选择/确认转换；Workflow 启动与用户消费确认结果。

## 本阶段新增或修改的模型

仅消费 WorkItemIntake、KnowledgeSnapshot、BindingConfirmation、ArtifactRef；不自建状态机。

## 约束

无相关知识与读取失败分开；同内容重复确认仍显示新动作；预填字段不等于批准；第二可写仓前端和后端均拒绝。

## 实施清单

- [x] 用知识失败、空结果、过期确认和同键冲突 fixture 建红灯。
- [x] 实现创建/列表和固定流程入口。
- [x] 实现知识选择、预览、摘要与确认。
- [x] 覆盖加载、错误、窄屏、键盘和防重复体验。

完成事实与逐条证据见 [record/S02](<../record/S02.md>)。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖 A02—A06、A11—A12、C01—C03/C08。

### 定向测试

mise exec -- pnpm test -- work-item knowledge

### 受影响回归、构建与静态门禁

pnpm lint/typecheck/test/build、关键流程 E2E、文档门禁、git diff --check。

## 运行时验收

以隔离后端/Artifact Store 验证确认前不启动下游、失败与空结果区分；不触发真实 WorkflowRun。

## 人工操作边界

不适用。

## 退出条件

实际消费知识、WorkItem、人工答复与固定流程应用合同；组件/合同/E2E 正反路径通过，未知字段与版本冲突诚实阻断。

## 停止点

完成后停止，不实现完整 DAG 详情。

## 消费准入（2026-10-16 准入并 pin）

完整目标保持；下列操作均由已 `DONE` 的 owner 冻结为实际 `http` 层次，并 pin manifest 文件 SHA-256；`schemaSha256` 在准入时逐份核对一致。逐操作 producer→transition→consumer、正反样例与核验命令见 [record/S02-消费准入证据](<../record/S02-消费准入证据.md>)。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明；前端不新增 endpoint、不重新解释错误类别。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.config","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.workitem.v1","operation": "workitem.intake","manifest": "api/workitem/v1/delivery.json","sha256": "cb612d1d71ad30445df720e23c78ea10f8a683a078c4157ca12712f9796739ff","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.workitem.v1","operation": "workitem.list","manifest": "api/workitem/v1/delivery.json","sha256": "cb612d1d71ad30445df720e23c78ea10f8a683a078c4157ca12712f9796739ff","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.workitem.v1","operation": "workitem.detail","manifest": "api/workitem/v1/delivery.json","sha256": "cb612d1d71ad30445df720e23c78ea10f8a683a078c4157ca12712f9796739ff","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "human.list","manifest": "api/delivery-governance/v1/delivery.json","sha256": "10a3bf1957a78a79feb42ca568425c4e3eb327390c6c96238e8df90875e66a71","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "human.detail","manifest": "api/delivery-governance/v1/delivery.json","sha256": "10a3bf1957a78a79feb42ca568425c4e3eb327390c6c96238e8df90875e66a71","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "human.response","manifest": "api/delivery-governance/v1/delivery.json","sha256": "10a3bf1957a78a79feb42ca568425c4e3eb327390c6c96238e8df90875e66a71","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "intake.start-knowledge","manifest": "api/web-application/v1/delivery.json","sha256": "fbd76a2ae231ecc68854c0bb4ebfeb209844b5e6343c484156059929a397edc8","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "run.detail-history","manifest": "api/web-application/v1/delivery.json","sha256": "fbd76a2ae231ecc68854c0bb4ebfeb209844b5e6343c484156059929a397edc8","level": "http","evidence": "doc/Kowa前端设计/record/S02-消费准入证据.md"}
  ]
}
```

准入勘误（2026-10-16）：

- 移除 `backend:S02 / kowa.workflow-execution.v1 / run.view` 与 `backend:S03 / kowa.knowledge-artifact.v1 / snapshot.read`：两者在 main 均无 `delivery.json`（全仓仅 7 个），且层次为 `application`，不能作为 HTTP 消费；替换载体为 `run.detail-history` 与 `intake.start-knowledge` 响应 + `human.detail`（G1）。
- 补登 `workitem.list`、`workitem.detail`、`human.detail`、`human.list`（G2/G3），并补登创建页实际消费的 `workspace.config`（backend:S01，与 S01 同一 pin）。
- `fixed.workflow` 默认不用（G4）：S02 停止点不实现 DAG/定义渲染。
- 跨端依赖收敛为 backend:S01/S05/S06；backend:S02/S03 作为模块生产者属间接影响，不做直接绑定（与 S01 对 backend:S02 的处置同例）。
