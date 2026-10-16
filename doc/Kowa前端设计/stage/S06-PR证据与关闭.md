# S06：PR、证据与关闭

> 状态只在总体设计维护；执行事实进入 record/S06.md。
> 本阶段原编号为 S05；2026-10-16 前端阶段重排（新增 S03 UI 基座）后顺延为 S06，更早文档中的旧编号按当时语义阅读。

## 阶段设计依据

交付页必须同时展示 Web 发起人、机器账号、Agent、提交、PR、真人 Review、合并、验证和关闭适用性，不能压成单一绿色状态。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现 GitHub 身份与 PR/Review/merge、质量证据、外部未知、集成验证和关闭/修复关联展示。

## 前置与跨端依赖

- 本端前置阶段：S05。
- 跨端阶段：backend:S01、backend:S05、backend:S06、backend:S07。
- 共享契约 owner：backend:S01（`kowa.identity-workspace.v1` 的 `workspace.config`，本阶段唯一可信外链来源）、backend:S05（`kowa.delivery-governance.v1` 的 `delivery.query`，交付证据主载体）、backend:S06（`kowa.web-application.v1` 的 `run.detail-history`/`delivery.artifact-fixrun`）、backend:S07（`kowa.observability.v1` 的 `observability.watermarks`，最后成功观察与陈旧水位），逐操作责任与冻结引用见下方消费准入。
- 冻结契约及版本：四条均为已交付的 v1 `http` 操作（`schema.json` 的 `title` 与 manifest 的 `contract` 一致），逐操作 manifest 文件 `sha256` 与 `schemaSha256` 在本阶段准入 HEAD `2f4cd6631dac4d98628562f250b8bcfe79841c6d` 上重算，见消费准入。

## 必读

MVP 流程/质量设计、身份术语、backend:S05 合同/record、GitHub 交付设计、验收 A19—A28/C05—C06。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

PR/evidence panels、identity labels、observation watermark、closure/fix-run views、artifact/report viewers 和 tests。

### 允许修改

机器作者/发起人/Agent 分列、Review 适用提交、merge OID、CI/验证、未知/陈旧状态、关闭清单和修复关联。

### 明确禁止

把 Web 发起人冒充 GitHub 作者、页面口头反馈当合并、旧 Review/head 放行、隐藏已合并但门禁失败的事实。

### 并行写入范围

独占交付证据/关闭 UI；消费 S05 交付与 S06 组合查询冻结合同。

## 当前问题与证据

- CONFIRMED：身份、Review、合并、版本失效和关闭规则已冻结。
- CODE_CONFIRMED：进入时无交付页实现。
- DATA_CONFIRMED：无真实 PR 证据。
- STRONG_INFERENCE：ClosureEvidence 可避免浏览器拼接成功。
- HYPOTHESIS：GitHub 原始错误细节需脱敏后展示。
- 文档与源码差异：以冻结合同为准。
- 证据不足项：真实规则集/CI 组合。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

GitHubObservation/Verification/Closure 投影生产事实；UI 分组和适用性呈现转换；用户消费。

## 本阶段新增或修改的模型

只读 DeliveryView；不得拥有 Review、merge 或关闭决定。

## 约束

最后成功观察时间可见；EXTERNAL_UNKNOWN 独立；已合并无有效 Review仍阻断关闭；知识建议/离线评测失败单独显示。

- 本阶段 UI 必须基于 S03 交付的组件库与 GitHub 主题；不得手搓基础组件、不得绕过主题令牌。

## 实施清单

- [ ] 以身份混淆、head 漂移、已合并缺 Review 和外部未知建立红灯。
- [ ] 实现 PR/Review/merge/CI/验证时间线。
- [ ] 实现 ClosureEvidence、修复运行和辅助失败展示。
- [ ] 覆盖链接安全、脱敏、窄屏与非颜色提示。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖 A19—A28、C05—C06。

### 定向测试

mise exec -- pnpm test -- delivery evidence closure

### 受影响回归、构建与静态门禁

pnpm lint/typecheck/test/build、交付详情 E2E、文档门禁、git diff --check。

## 运行时验收

隔离后端 fixture 验证 PR 响应丢失、旧 Review、已合并但阻断、集成失败及修复关联。

## 人工操作边界

不创建、Review 或合并真实 PR。

## 退出条件

所有身份/版本/证据来源可辨，未知和失败不假绿；合同/组件/E2E 通过。

## 停止点

完成后停止，不宣称完整跨端真实闭环。

## 消费准入（2026-10-16 重算）

完整目标保持。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明；已有 schema 不等于生产装配或用户旅程。下列四条均为已交付且在本阶段准入 HEAD `2f4cd6631dac4d98628562f250b8bcfe79841c6d` 上重算双 pin 的 `http` 操作（未沿用 2026-10-01 的候选值、聊天结论或旧记录数字）。逐操作的 Producer → Transition → Consumer、实际入口行号、正反证据与未准入候选的理由见 [S06 消费准入证据](../record/S06-消费准入证据.md)。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.config","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S06-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "delivery.query","manifest": "api/delivery-governance/v1/delivery.json","sha256": "10a3bf1957a78a79feb42ca568425c4e3eb327390c6c96238e8df90875e66a71","level": "http","evidence": "doc/Kowa前端设计/record/S06-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "run.detail-history","manifest": "api/web-application/v1/delivery.json","sha256": "fbd76a2ae231ecc68854c0bb4ebfeb209844b5e6343c484156059929a397edc8","level": "http","evidence": "doc/Kowa前端设计/record/S06-消费准入证据.md"},
    {"owner": "backend:S06","contract": "kowa.web-application.v1","operation": "delivery.artifact-fixrun","manifest": "api/web-application/v1/delivery.json","sha256": "fbd76a2ae231ecc68854c0bb4ebfeb209844b5e6343c484156059929a397edc8","level": "http","evidence": "doc/Kowa前端设计/record/S06-消费准入证据.md"},
    {"owner": "backend:S07","contract": "kowa.observability.v1","operation": "observability.watermarks","manifest": "api/observability/v1/delivery.json","sha256": "e146055829c985b62d9fd36c4cff06bfbcdcffbea056db2e9a9d6913a89c3848","level": "http","evidence": "doc/Kowa前端设计/record/S06-消费准入证据.md"}
  ]
}
```

### 准入勘误（2026-10-16）

- **无 `application` 面死条目**：阶段文件 2026-10-01 版的两条 `requires` 经本 HEAD 逐项核对都是真实存在且已交付 `http` 的操作（与 S04 的 `run.view`/`artifact.read`、S05 的 `run.recovery-core` 不同）。`delivery.query` 保留为主载体并 pin；`delivery.artifact-fixrun` 保留（报告产物引用核对）。
- **新增 `backend:S01 workspace.config`**：GitHub 外链的唯一可信来源——冻结投影里只有数字 `repositoryId`，仓库 `owner/repo` 只存在于工作区登记的 `displayName`。页面把它当待校验候选（`owner/repo` 形状 + 数字 ID 一致），失败即不渲染链接。
- **新增 `backend:S06 run.detail-history`**：运行级身份（Web 发起人 `startedByGitHubUserId`、Runner 机器账号 `gitExecutionUserId`）与关闭记录的定义/运行逐项对照。
- **新增 `backend:S07 observability.watermarks`**：最后成功观察与陈旧语义的 owner 交付（`freshness`/`freshnessReason`/`supersededByFailure` 与 `freshnessWindowSeconds`）；本阶段不再靠前端自造阈值。S05 曾按「本阶段不渲染关闭与陈旧水位」的理由不准入，本阶段按自己的消费重新判定并准入了它。
- **不新增 `backend:S06 journey`**：`JourneyView.closure` 在冻结合同里是无约束对象，消费它会形成第二个未经校验的关闭权威；关闭事实统一取 `delivery.query.closure`。
- **依赖声明扩展**：本阶段跨端依赖由 `backend:S05、backend:S06` 扩展为 `backend:S01、backend:S05、backend:S06、backend:S07`。这是合同绑定更新，不重开任何 `DONE` 阶段。

### 本阶段消费要点（约束的来源）

- `delivery.query` 的 `facts` 是每主体**最新一条**，`unknown=true` 的行**不携带 payload**：失败的读取既不是事实也不是“不存在”，必须单独列出；「最后成功观察时间」只能取成功读取的 `observedAt` 最大值。
- 契约**没有** merge/approve/close 写操作：页面不提供第二个合并批准按钮；`merged=true` 只是观察事实，不等于关闭。
- Review 的适用提交是 `review.commitOid` 与 PR 观察到的 `headOid` 的比较，缺任一侧即“适用提交未知”。
- `checks.state` 的 `none` 不是通过；`verification.source` 的 `simulated` 不满足门禁；`knowledge_suggestion`/`offline_evaluation` 失败不阻塞关闭。
- `observability.watermarks` 的 `lastAttemptKnown=false` 只说明最后一次读取失败，该主体最后成功读取的时间不在该水位里；页面不外推，也不把心跳当作进度。
