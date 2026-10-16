# S04：DAG、运行与历史

> 状态只在总体设计维护；执行事实进入 record/S04.md。
> 本阶段原编号为 S03；2026-10-16 前端阶段重排（新增 S03 UI 基座）后顺延为 S04，更早文档中的旧编号按当时语义阅读。

## 阶段设计依据

用户必须区分节点执行状态、业务 verdict、当前轮次和历史结果；History View 不能变成 Replay。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现工作项详情中的真实 DAG、运行/节点/Task 状态、产物与版本历史只读体验。

## 前置与跨端依赖

- 本端前置阶段：S03。
- 跨端阶段：backend:S05、backend:S06。
- 共享契约 owner：backend:S05（`kowa.workitem.v1`）、backend:S06（`kowa.fixed-workflow.v1`、`kowa.web-application.v1`）；逐操作责任与 P/T/C 链路见消费准入。
- 冻结契约及版本：全部为已交付的 v1 `http` 操作，双 pin（`delivery.json` 文件 SHA-256 + 其所载 `schemaSha256`）在本阶段准入 HEAD 上重算，见消费准入。

本端直接前置为 S03（UI 基座与 GitHub 主题）：页面骨架、组件与主题令牌来自该阶段，UI 基座自身的前置为更早的页面阶段；本阶段的真实 DAG/历史实现沿用它们已交付的详情页与解码层。

## 必读

生命周期、执行尝试、前端信息架构、backend:S02 合同/record、质量观测设计、验收 A10/A14—A18/B06/B09。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

WorkItem detail、DAG renderer、Run/Node/Task panels、history/version views、事件订阅/轮询和 tests。

### 允许修改

当前/历史轮次、verdict、阻塞原因、Artifact 链接、事件时间/陈旧水位、修复运行关联和 URL 阅读状态。

### 明确禁止

前端推断放行/关闭、progress 推进 DAG、History View 创建运行、隐藏 skipped/unknown/error 差异。

### 并行写入范围

独占详情/DAG/历史；只消费核心与组合查询冻结合同。

## 当前问题与证据

- CONFIRMED：状态与 verdict 必须分离。
- CODE_CONFIRMED：进入时无详情实现。
- DATA_CONFIRMED：无真实事件流。
- STRONG_INFERENCE：服务端投影加显式当前指针足够渲染。
- HYPOTHESIS：大型 DAG 性能不属于 MVP，但需避免明显阻塞。
- 文档与源码差异：以冻结 RunView/NodeView 为准。
- 证据不足项：真实断连/重连节奏。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

后端投影/事件生产事实；UI 缓存和选择转换；用户只读消费。

## 本阶段新增或修改的模型

前端只读 view model；不得成为 WorkItem/Run/NodeRun 状态权威。

## 约束

历史轮次明确标记且无恢复按钮；事件断连显示陈旧；Task completed 不等于 verdict pass；本地绝对路径不展示为交付引用。

- 本阶段 UI 必须基于 S03 交付的组件库与 GitHub 主题；不得手搓基础组件、不得绕过主题令牌。

## 实施清单

- [ ] 以 progress 误放行、旧轮次混色和断连陈旧建立红灯。
- [ ] 实现 DAG、节点详情、Task/产物与历史。
- [ ] 实现事件更新、重连和 last-observed 水位。
- [ ] 覆盖 URL 状态、键盘导航和多状态文字。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖 A10/A14—A18、B06/B09。

### 定向测试

mise exec -- pnpm test -- work-item-detail dag history

### 受影响回归、构建与静态门禁

pnpm lint/typecheck/test/build、详情/事件 E2E、文档门禁、git diff --check。

## 运行时验收

对隔离控制面投影执行事件断开、乱序和历史轮次选择，不声称真实 Runner 已通过。

## 人工操作边界

不适用。

## 退出条件

状态/verdict/轮次/陈旧水位均由合同驱动且正反 E2E 通过；无前端业务状态权威。

## 停止点

完成后停止，不实现人工任务命令。

## 消费准入（2026-10-16 重算）

完整目标保持。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明；已有 schema 不等于生产装配或用户旅程。下列四条均为已交付且在本阶段准入 HEAD `e8584401bf71281bdeaa0abb9e3dd841dd10c93c` 上重算双 pin 的 `http` 操作（未沿用 2026-10-01 的候选值、聊天结论或旧记录数字）。逐操作的 Producer → Transition → Consumer、实际入口行号与正反证据见 [S04 消费准入证据](../record/S04-消费准入证据.md)。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S05","contract": "kowa.workitem.v1","operation": "workitem.detail","manifest": "api/workitem/v1/delivery.json","sha256": "cb612d1d71ad30445df720e23c78ea10f8a683a078c4157ca12712f9796739ff","level": "http","evidence": "doc/Kowa前端设计/record/S04-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.fixed-workflow.v1","operation": "fixed.workflow","manifest": "api/fixed-workflow/v1/delivery.json","sha256": "334ed687673e53a40060c5fc7747c2d71430524be74dee14f4280a133bcbd3a9","level": "http","evidence": "doc/Kowa前端设计/record/S04-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "run.detail-history","manifest": "api/web-application/v1/delivery.json","sha256": "fbd76a2ae231ecc68854c0bb4ebfeb209844b5e6343c484156059929a397edc8","level": "http","evidence": "doc/Kowa前端设计/record/S04-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "delivery.artifact-fixrun","manifest": "api/web-application/v1/delivery.json","sha256": "fbd76a2ae231ecc68854c0bb4ebfeb209844b5e6343c484156059929a397edc8","level": "http","evidence": "doc/Kowa前端设计/record/S04-消费准入证据.md"}
  ]
}
```

### 准入勘误（2026-10-16）

- **删除 `backend:S02 run.view`（`api/workflow-execution/v1/delivery.json`）与 `backend:S03 artifact.read`（`api/knowledge-artifact/v1/delivery.json`）**：`CODE_CONFIRMED`，两个 manifest **在仓库中不存在**（`api/workflow-execution/v1/` 与 `api/knowledge-artifact/v1/` 只有 `README.md`/`schema.json`/`examples/`），且原条目自标 `level=application`，本就不是 HTTP 消费面，不得凭 `schema.json` 存在而 pin。与 S02 的 G1 判定同例（规划漏项 + 文档漂移）。
- **新增 `backend:S06 fixed.workflow`（DAG 拓扑的真实载体）**：`CODE_CONFIRMED`，`run.detail-history` 的 `NodeHistory` **没有边**（字段仅 `nodeId`/`type`/`iteration`/`state`/`verdict`/`inputDigest`/`output`/`attempts`），真实 DAG 的节点集合、`dependsOn` 边、`order` 拓扑序与节点责任只能来自 `fixed.workflow`（`GET /api/v1/workspaces/{workspaceID}/fixed-workflow`，实际路由 `internal/interfaces/httpapi/web_application.go:90` → `handler.fixedWorkflow`）。S02 的 G4 曾判其"不需要"，那是因为 S02 不渲染 DAG，不构成对 S04 的结论。
- **新增 `backend:S06 delivery.artifact-fixrun`（产物/证据只读清单与修复运行谱系）**：本阶段"产物/证据只读链接"与"修复运行关联"的真实载体。
- **不新增 `backend:S07 observability.watermarks`**：该 v1 契约自述消费者是 S08 真实集成验收与运维视图；本阶段的"last-observed 水位"由 `run.detail-history.watermark`（`observedAt`/`runUpdatedAt`）+ 轮询断连态覆盖，不需要前端自造陈旧阈值，故不扩依赖。
- **依赖声明收敛**：由于两个 `application` 死条目被移除且 backend:S02/S03 无可绑定的 HTTP 操作，本阶段跨端依赖收敛为 backend:S05、backend:S06（其事实仍经 backend:S06 的组合投影到达浏览器，属间接影响而非直接消费绑定）。这是合同绑定更新，不重开任何 `DONE` 阶段。

### 本阶段消费要点（约束的来源）

- `fixed.workflow` 描述**定义结构**（本轮/历史轮次与 verdict 由运行投影提供），前端只渲染服务端返回的节点责任，不推断放行、不本地改图、不把 `progress` 当作推进 DAG 的依据。
- `run.detail-history` 的 `nodes` 是**全部已物化节点轮次（含历史 iteration）**；`Task COMPLETED` 不等于 verdict 通过，历史轮次不得与当前轮次混色。
- 运行 / 节点 / Task 三层身份与 `verdict`/`state` 分离展示。
- 恢复动作（`run.recovery`）属 S05，本阶段不实现。
