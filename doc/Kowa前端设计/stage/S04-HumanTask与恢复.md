# S04：HumanTask 与恢复

> 状态只在总体设计维护；执行事实进入 record/S04.md。

## 阶段设计依据

知识/方案/恢复答复和 GitHub 外部操作提醒必须共用服务端 HumanTask，但提交方式、版本和放行证据不同。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现版本化 HumanTask、方案审批以及 Retry/Rerun/取消/人工返修恢复交互。

## 前置与跨端依赖

- 本端前置阶段：S03。
- 跨端阶段：backend:S02、backend:S05、backend:S06。
- 共享契约 owner：backend:S02、backend:S05、backend:S06（逐操作责任见消费准入）。
- 冻结契约及版本：既有 v1 为核心基线；新增操作待 owner 冻结，不能准入。

## 必读

生命周期、执行尝试、MVP 流程、backend:S02/S05 合同与 record、前端信息架构、验收 A07—A15/A23—A25。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

HumanTask list/detail、动态字段 renderer、审批/反馈、恢复命令、冲突与失效 UI、tests。

### 允许修改

in_app_response 表单、external_fact 提示、对象版本、幂等键、返修预算、Retry/Rerun/取消和修复运行入口。

### 明确禁止

给外部 GitHub 提示增加 Kowa 批准按钮、允许未知必填字段提交、修改失败事实、浏览器决定动作权限。

### 并行写入范围

独占 HumanTask/恢复 UI；消费 S02/S05/S06 冻结合同。

## 当前问题与证据

- CONFIRMED：两种 HumanTask completion mode 和恢复语义已定义。
- CODE_CONFIRMED：进入时无实现。
- DATA_CONFIRMED：无人工操作证据。
- STRONG_INFERENCE：服务端字段/动作描述可支撑统一 renderer。
- HYPOTHESIS：复杂方案审阅最终采用页面或抽屉可按可用性测试选择。
- 文档与源码差异：以正式 schema 为准。
- 证据不足项：真实多人并发答复。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

服务端生产请求/允许动作；用户答复或外部事实转换；Workflow 与用户消费结果。

## 本阶段新增或修改的模型

仅消费 HumanTaskRequest/Response 与 RecoveryCommand；前端不拥有任务状态。

## 约束

预填不等于确认；过期/越权/同键异内容拒绝；确定性失败不能由继续按钮变 pass；external_fact 无 HumanResponse。

## 实施清单

- [ ] 以过期请求、重复冲突、未知字段和外部按钮建立红灯。
- [ ] 实现待办列表、版本化详情和动态表单。
- [ ] 实现返修预算及 Retry/Rerun/取消交互。
- [ ] 覆盖焦点、错误关联、草稿保留和竞态刷新。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖 A07—A15、A23—A25。

### 定向测试

mise exec -- pnpm test -- human-task recovery

### 受影响回归、构建与静态门禁

pnpm lint/typecheck/test/build、关键命令 E2E、文档门禁、git diff --check。

## 运行时验收

隔离控制面中验证答复接受、过期、预算耗尽、恢复与外部提示自动收束投影。

## 人工操作边界

不执行真实 GitHub Review/merge。

## 退出条件

两种 completion mode、命令冲突和失效路径均有合同测试与 E2E；前端不提供越权替代动作。

## 停止点

完成后停止，不实现完整 PR/证据关闭页。

## 消费准入（2026-10-01）

完整目标保持；下列为待冻结需求，不是已交付 API。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明。已有 schema 不等于生产装配或用户旅程；准入须补齐 delivery 摘要及正反证据。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S02","contract": "kowa.workflow-execution.v1","operation": "run.recovery-core","manifest": "api/workflow-execution/v1/delivery.json","sha256": null,"level": "application","evidence": null},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "human.response","manifest": "api/delivery-governance/v1/delivery.json","sha256": null,"level": "http","evidence": null},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "run.recovery","manifest": "api/web-application/v1/delivery.json","sha256": null,"level": "http","evidence": null}
  ]
}
```
