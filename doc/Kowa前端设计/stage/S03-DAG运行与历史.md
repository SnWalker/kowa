# S03：DAG、运行与历史

> 状态只在总体设计维护；执行事实进入 record/S03.md。

## 阶段设计依据

用户必须区分节点执行状态、业务 verdict、当前轮次和历史结果；History View 不能变成 Replay。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现工作项详情中的真实 DAG、运行/节点/Task 状态、产物与版本历史只读体验。

## 前置与跨端依赖

- 本端前置阶段：S02。
- 跨端阶段：backend:S02、backend:S03、backend:S05、backend:S06。
- 共享契约 owner：backend:S02、backend:S03、backend:S05、backend:S06（逐操作责任见消费准入）。
- 冻结契约及版本：既有 v1 为核心基线；新增操作待 owner 冻结，不能准入。

## 必读

生命周期、执行尝试、前端信息架构、backend:S02 合同/record、质量观测设计、验收 A10/A14—A18/B06/B09。

## 推荐 Skills

- `vercel-react-best-practices`
- `vercel-composition-patterns`
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

## 消费准入（2026-10-01）

完整目标保持；下列为待冻结需求，不是已交付 API。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明。已有 schema 不等于生产装配或用户旅程；准入须补齐 delivery 摘要及正反证据。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S02","contract": "kowa.workflow-execution.v1","operation": "run.view","manifest": "api/workflow-execution/v1/delivery.json","sha256": null,"level": "application","evidence": null},
    {"owner": "backend:S03","contract": "kowa.knowledge-artifact.v1","operation": "artifact.read","manifest": "api/knowledge-artifact/v1/delivery.json","sha256": null,"level": "application","evidence": null},
    {"owner": "backend:S05","contract": "kowa.workitem.v1","operation": "workitem.detail","manifest": "api/workitem/v1/delivery.json","sha256": null,"level": "http","evidence": null},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "run.detail-history","manifest": "api/web-application/v1/delivery.json","sha256": null,"level": "http","evidence": null}
  ]
}
```
