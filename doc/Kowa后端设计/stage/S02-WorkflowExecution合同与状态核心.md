# S02：Workflow/Execution 合同与状态核心

> 状态只在总体设计维护；执行事实进入 record/S02.md。

## 阶段设计依据

固定流程、冻结定义、Task 尝试身份和生命周期已有正式语义，但候选 wire、编译规则和持久映射未实现。

## 现行勘误（仅 REOPENED 时填写）

不适用。

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
- CODE_CONFIRMED：只有候选 JSON，无实现消费者。
- DATA_CONFIRMED：无运行数据。
- STRONG_INFERENCE：普通 DAG 加显式有界返修可承载固定流程。
- HYPOTHESIS：轮询时限和租约数值需阶段内测试确定。
- 文档与源码差异：候选 schema 缺完整机器 Git 身份和业务查询面。
- 证据不足项：并发事务和崩溃恢复实测。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

定义作者/Server 生产冻结计划和 Task；状态核心转换；Runner、Web 投影、HumanTask、审计消费。

## 本阶段新增或修改的模型

CompiledPlan、WorkflowRun、NodeRun、Task、Lease、RuntimeRegistration、RunView/NodeView；各身份不得合并。

## 约束

未知字段拒绝；同名同版本异内容冲突；progress 不推进；Retry 保持输入；旧租约/迟到报告不得覆盖当前事实。

## 实施清单

- [ ] 从 B01—B12 与旧租约/重复报告建立红灯。
- [ ] 实现编译、冻结、持久状态和条件接受。
- [ ] 补齐机器 Git 身份、操作范围及外部未知信封。
- [ ] 发布 schema、golden、正反协议样例和迁移。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖定义错误、普通环、非法绑定、版本漂移、重复/迟到报告和 Retry/Rerun 区分。

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

## 停止点

完成后停止，不实现 Artifact、知识或 Runner Worker。
