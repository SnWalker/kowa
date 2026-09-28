# S05：PR、证据与关闭

> 状态只在总体设计维护；执行事实进入 record/S05.md。

## 阶段设计依据

交付页必须同时展示 Web 发起人、机器账号、Agent、提交、PR、真人 Review、合并、验证和关闭适用性，不能压成单一绿色状态。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现 GitHub 身份与 PR/Review/merge、质量证据、外部未知、集成验证和关闭/修复关联展示。

## 前置与跨端依赖

- 本端前置阶段：S04。
- 跨端阶段：backend:S05。
- 共享契约 owner：backend:S05。
- 冻结契约及版本：kowa.delivery-governance.v1。

## 必读

MVP 流程/质量设计、身份术语、backend:S05 合同/record、GitHub 交付设计、验收 A19—A28/C05—C06。

## 推荐 Skills

- `vercel-react-best-practices`
- `vercel-composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

PR/evidence panels、identity labels、observation watermark、closure/fix-run views、artifact/report viewers 和 tests。

### 允许修改

机器作者/发起人/Agent 分列、Review 适用提交、merge OID、CI/验证、未知/陈旧状态、关闭清单和修复关联。

### 明确禁止

把 Web 发起人冒充 GitHub 作者、页面口头反馈当合并、旧 Review/head 放行、隐藏已合并但门禁失败的事实。

### 并行写入范围

独占交付证据/关闭 UI；只消费 backend:S05 v1。

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
