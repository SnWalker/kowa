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
- 跨端阶段：backend:S02、backend:S03、backend:S05、backend:S06。
- 共享契约 owner：backend:S02、backend:S03、backend:S05、backend:S06（逐操作责任见消费准入）。
- 冻结契约及版本：既有 v1 为核心基线；新增操作待 owner 冻结，不能准入。

## 必读

MVP 流程、前端信息架构、backend:S02/S03 合同与 record、候选合同知识章节、验收 A01—A06/C01—C03/C08。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

WorkItem 列表/创建、需求表单、知识推荐/预览/确认、artifact viewer、合同 providers/tests。

### 允许修改

单仓需求与验收标准、固定流程提示、推荐/搜索/增删/确认、内容摘要和版本展示、失败恢复。

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

- [ ] 用知识失败、空结果、过期确认和同键冲突 fixture 建红灯。
- [ ] 实现创建/列表和固定流程入口。
- [ ] 实现知识选择、预览、摘要与确认。
- [ ] 覆盖加载、错误、窄屏、键盘和防重复体验。

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

## 消费准入（2026-10-01）

完整目标保持；下列为待冻结需求，不是已交付 API。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明。已有 schema 不等于生产装配或用户旅程；准入须补齐 delivery 摘要及正反证据。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S02","contract": "kowa.workflow-execution.v1","operation": "run.view","manifest": "api/workflow-execution/v1/delivery.json","sha256": null,"level": "application","evidence": null},
    {"owner": "backend:S03","contract": "kowa.knowledge-artifact.v1","operation": "snapshot.read","manifest": "api/knowledge-artifact/v1/delivery.json","sha256": null,"level": "application","evidence": null},
    {"owner": "backend:S05","contract": "kowa.workitem.v1","operation": "workitem.intake","manifest": "api/workitem/v1/delivery.json","sha256": null,"level": "http","evidence": null},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "human.response","manifest": "api/delivery-governance/v1/delivery.json","sha256": null,"level": "http","evidence": null},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "intake.start-knowledge","manifest": "api/web-application/v1/delivery.json","sha256": null,"level": "http","evidence": null}
  ]
}
```
