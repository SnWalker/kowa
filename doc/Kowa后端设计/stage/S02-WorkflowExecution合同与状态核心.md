# S02：Workflow/Execution 合同与状态核心

> 状态只在总体设计维护；执行事实进入 record/S02.md。

## 阶段设计依据

Workflow/Execution v1 核心已交付；本轮只追溯 Runtime 登记一致性，不扩大为完整 Web 应用。

## 现行勘误（租约返工历史与本轮定位）

2026-09-30：S03 隔离验收取得 CODE_CONFIRMED / DATA_CONFIRMED 新证据：错误 lease ID 报告使未过期当前 Task 从 LEASED 变为 EXPIRED，正确报告随后被拒绝。现有 `ReportResult` 的拒收路径违反本阶段“旧租约/迟到报告不得覆盖当前事实”约束，先前退出测试未覆盖此交错。

最早责任 owner 为 S02；证据见 [record/S02.md](../record/S02.md)。2026-10-01 该租约返工已完成并收口，characterization、目标不变量及两适配器证据见 record/S02。以下原验收流程供追溯；本轮新登记缺陷与范围见末尾责任追溯，不据旧记录反推实时状态。

## 唯一工程目标

冻结并实现 WorkflowDefinition/Run/NodeRun/Task/Runtime 的状态核心、编译器与跨端/Runner wire v1。

## 前置与跨端依赖

- 本端前置阶段：S01。
- 跨端阶段：无。
- 共享契约 owner：backend:S02。
- 冻结契约及版本：kowa.workflow-execution.v1。

## 必读

执行架构、生命周期、执行尝试合同、跨端语义契约、Workflow/Capability 设计、候选 schema、验收 A08—A15/B01—B12/C03/C08。

## 推荐 Skills

- `golang-how-to`
- `golang-concurrency`
- `golang-context`
- `golang-design-patterns`
- `golang-data-structures`
- `golang-database`
- `golang-safety`
- `golang-testing`

## 核心文件与修改范围

### 核心文件

未来 Workflow/Execution domain、编译器、状态仓储、任务派发端口、RunView/NodeView schema 与协议测试。

### 允许修改

定义 parse/compile/freeze、状态转换、轮次、Task/lease/fencing、Retry/Rerun/取消意图、运行投影与错误类别。

### 明确禁止

Provider 执行、GitHub 写入、人审事实、前端状态机或任意 DAG 编辑器。

### 并行写入范围

独占 Workflow/Execution 模块及 kowa.workflow-execution.v1。

## 当前问题与证据

- CONFIRMED：状态、尝试身份、冻结与失败语义已批准。
- CODE_CONFIRMED：当前已有 Workflow/Execution 核心；旧租约拒收已修复，本轮 MemoryStore 的 Runtime 同 epoch 比较遗漏 Provider/认证字段。
- DATA_CONFIRMED：历史隔离固定时钟曾复现租约污染；本轮内存探针复现登记变化被接受后仍按旧登记派发，未证明 PostgreSQL 同样错误。
- STRONG_INFERENCE：普通 DAG 加显式有界返修可承载固定流程。
- HYPOTHESIS：轮询时限和租约数值需阶段内测试确定。
- 文档与源码差异：正式 v1 已实现，当前问题修订为登记一致性；租约返工证据保留在 record。
- 证据不足项：登记一致性永久目标矩阵、两适配器验证及未来返工最终 head CI。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

定义作者/Server 生产冻结计划和 Task；状态核心转换；Runner、Web 投影、HumanTask、审计消费。

## 本阶段新增或修改的模型

CompiledPlan、WorkflowRun、NodeRun、Task、Lease、RuntimeRegistration、RunView/NodeView；各身份不得合并。

## 约束

未知字段拒绝；同名同版本异内容冲突；progress 不推进；Retry 保持输入；旧租约/迟到报告不得覆盖当前事实。

## 实施清单

- [x] 从 B01—B12 与旧租约/重复报告建立红灯。
- [x] 实现编译、冻结、持久状态和条件接受。
- [x] 补齐机器 Git 身份、操作范围及外部未知信封。
- [x] 发布 schema、golden、正反协议样例和迁移。

## 测试与自动验证

### Characterization 与目标红灯

首次实施为纯绿地；本次存量返工 characterization 适用，临时 overlay 冻结旧错误，永久测试只保护正确行为。覆盖 LEASED/RUNNING 的错误 lease/fencing、非法报告拒收、真正过期边界、重复/冲突及迟到报告；正确报告在错误拒收后仍可接受。

本次定向与隔离入口：`mise exec -- go test -count=1 -json ./internal/workflow/... ./internal/execution/...`；`mise exec -- make test-workflow-execution-integration`；原复现：`mise exec -- bash doc/Kowa后端设计/record/evidence/S03-lease-mismatch-repro.sh`。

### 定向测试

mise exec -- go test ./internal/workflow/... ./internal/execution/...

### 受影响回归、构建与静态门禁

mise exec -- go test ./...；mise exec -- go vet ./...；mise exec -- go build ./...；文档门禁；git diff --check。

## 运行时验收

隔离数据库中执行编译、派发、重复报告、过期租约和重启恢复；不调用真实 Provider。

## 人工操作边界

不适用。

## 退出条件

kowa.workflow-execution.v1 完成机器 schema/摘要、正反样例、Producer/Consumer 核对、失败语义、矩阵映射和迁移验证。

本次须完成 characterization、目标红灯、定向绿灯、受影响回归、隔离 PostgreSQL、构建/静态/文档与 diff 门禁；最终 PR head 的必需 lint/verify 全部 SUCCESS 才能确认退出。实现提交 CI 通过后登记收口，最终状态提交继续核对当前 head checks，并在 PR 描述记录 SHA/run/job，避免 record 自引用循环。该租约返工当时恢复 S03，历史基线见 record；本轮 S02 登记返工应在 S01 新退出后独立登记，退出恢复实际 S04 前沿，后继以本次重开时基线为准。

## 停止点

完成后停止，不实现 Artifact、知识或 Runner Worker。

## 本轮责任追溯（2026-10-01）

原合同违约：MemoryStore.RegisterRuntime 对同 epoch 的 Provider ID/version/authentication 变化不冲突；PostgreSQL 会比较这些字段，v1 要求异内容冲突。审计探针中三种变更返回 nil，认证 false 后仍可按旧登记派发 Task。S01 独立返工退出后再登记 S02 重开；本轮不改状态。

返工仅修登记一致性与内存/PostgreSQL 等价矩阵：重复幂等、异内容拒绝且登记不变、新 epoch、Provider/机器身份匹配及后续合法派发。保持 workflow-execution.v1；WorkItem 创建、完整恢复和 Web 查询不因此转归 S02。
