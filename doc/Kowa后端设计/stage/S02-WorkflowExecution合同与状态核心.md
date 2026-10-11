# S02：Workflow/Execution 合同与状态核心

> 状态只在总体设计维护；执行事实进入 record/S02.md。

## 阶段设计依据

Workflow/Execution v1 核心已交付；2026-10-01 返工只追溯 Runtime 登记一致性，不扩大为完整 Web 应用。

## 现行勘误（租约返工历史与本轮定位）

2026-09-30：S03 隔离验收取得 CODE_CONFIRMED / DATA_CONFIRMED 新证据：错误 lease ID 报告使未过期当前 Task 从 LEASED 变为 EXPIRED，正确报告随后被拒绝。现有 `ReportResult` 的拒收路径违反本阶段“旧租约/迟到报告不得覆盖当前事实”约束，先前退出测试未覆盖此交错。

最早责任 owner 为 S02；证据见 [record/S02.md](../record/S02.md)。2026-10-01 该租约返工已完成并收口，characterization、目标不变量及两适配器证据见 record/S02。以下原验收流程供追溯；本轮新登记缺陷与范围见末尾责任追溯，不据旧记录反推实时状态。

2026-10-01 勘误（Runtime 登记返工）：本阶段第二次重开，范围由“同 epoch 字段比较遗漏”扩展为同一 Runtime 登记事实责任的三处合同违约，均已由直接证据确认，见 [record/S02.md](../record/S02.md) 与 `record/evidence/S02-runtime-registration-characterization.json`：(1) MemoryStore 同 epoch 不比较 Provider ID/version/认证且按顺序比较能力；(2) PostgreSQL 并发首次登记返回裸主键冲突而非幂等；(3) 两适配器新 epoch 登记后旧 epoch 租约仍可接受 progress/result，违反“租约现行须匹配 Runtime epoch / 旧 epoch 为 STALE_RESULT”。wire v1 字段、迁移与 schema 不变；只补齐 README 与 wiki 的规则文字。

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
- CODE_CONFIRMED：当前已有 Workflow/Execution 核心；旧租约拒收与 Runtime 登记返工已退出；2026-10-11 MemoryStore queued Rerun 后的旧轮次隔离违约已复现，见末尾新责任重开。
- DATA_CONFIRMED：历史隔离固定时钟曾复现租约污染；历史内存登记探针与两适配器修复证据见 record；本次固定时钟探针直接复现旧轮次失败改变当前 Run，PostgreSQL 本次仅源码核对。
- STRONG_INFERENCE：普通 DAG 加显式有界返修可承载固定流程。
- HYPOTHESIS：轮询时限和租约数值需阶段内测试确定。
- 文档与源码差异：正式 v1 已交付；当前问题是 MemoryStore 不满足旧轮次不得影响当前事实的原不变量，既有租约/登记历史保留于 record。
- 证据不足项：当前轮次本地修复与新两适配器矩阵已通过，最终 PR head CI 尚缺；epoch 顺序无冻结合同（不透明标识，不假设单调）；无租约清扫器（属 S04 Runner 租约循环，不在此实现）。
- 固化旧错误的测试：无直接断言旧错误的永久测试；本次 characterization 探针仅临时存在，源码留证于 `record/evidence/S02-runtime-registration-characterization-*.go.txt`，不进入永久回归。

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
- [x] Runtime 登记返工：characterization 探针冻结旧错误，建立内存/PostgreSQL 共享登记合同矩阵并取得目标红灯。
- [x] 以单一 `RuntimeRegistration.SameDeclaration` 修复同 epoch 内容比较，PostgreSQL 以主键仲裁并发首次登记，新 epoch 使旧 epoch 租约不再现行。
- [x] 内存与隔离 PostgreSQL 两适配器同矩阵通过，README/wiki 规则澄清，退出证据与最终 PR head CI 登记。

## 测试与自动验证

### Characterization 与目标红灯

首次实施为纯绿地；本次存量返工 characterization 适用，临时 overlay 冻结旧错误，永久测试只保护正确行为。覆盖 LEASED/RUNNING 的错误 lease/fencing、非法报告拒收、真正过期边界、重复/冲突及迟到报告；正确报告在错误拒收后仍可接受。

本次定向与隔离入口：`mise exec -- go test -count=1 -json ./internal/workflow/... ./internal/execution/...`；`mise exec -- make test-workflow-execution-integration`；原复现：`mise exec -- bash doc/Kowa后端设计/record/evidence/S03-lease-mismatch-repro.sh`。

Runtime 登记返工入口：`mise exec -- go test -count=1 -json ./internal/execution/...`（`TestMemoryStoreRuntimeRegistrationContract` 与 `SameDeclaration` 单测）；`mise exec -- make test-workflow-execution-integration`（`TestWorkflowExecutionRuntimeRegistrationPostgres`，与内存适配器共用 `internal/execution/executiontest` 矩阵）。

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

原合同违约（直接证据见 record/S02.md）：同 epoch 登记在 MemoryStore 与 PostgreSQL 的内容比较不等价；PostgreSQL 并发首次登记不幂等；新 epoch 登记不使旧 epoch 租约失效。S01 返工退出并合入 main 后，已独立登记 S02 REOPENED（重开前 currentStage=S04，后继基线 S03 DONE、S04—S08 NOT_STARTED）。

返工只修登记事实、两适配器等价矩阵、新 epoch 与租约现行性：重复幂等、异内容拒绝且登记不变、新 epoch 替换、Provider/认证/机器身份匹配与后续合法派发。保持 workflow-execution.v1 字段与迁移不变；WorkItem 创建、完整恢复、Runtime Web 查询和租约清扫器不因此转归 S02。

## 2026-10-11 责任重开：当前轮次隔离

S04 M1 开工审计取得 `CODE_CONFIRMED` 新证据：通过合法的 Service.RerunNode 创建第 2 轮后，MemoryStore 保留第 1 轮 QUEUED Task，LeaseTask 仍派发它；ReportResult 未核对当前 NodeRun 轮次，接受旧轮次失败并将当前 Run 从 ACTIVE 改为 PAUSED。违反原 v1 冻结的“旧轮次不得推进/覆盖当前事实”，不是 M1 新需求或清扫规划漏项。

最早责任 owner 为 S02。本次只登记 REOPENED，停止 S04，不执行 S02 修复。后继基线：S03 DONE，S04—S08 NOT_STARTED；完整 activeReopen 见总体设计。证据与复跑入口见 record/S02.md 的 2026-10-11 时间线。

下一返工会话只处理当前轮次的 producer（Rerun/Revise supersession）、transition（领取与租约现行性）及 consumer（进度/结果接受），核对内存与 PostgreSQL 等价不变量。先冻结既有问题，再建立目标红灯；拒收后现行事实不变且随后合法请求成功。保留 wire、迁移和依赖；不得顺带实现 S04。登记时尚未修复；2026-10-11 分叉接力的本地实现与门禁见下一节和 record/S02.md，最终远端退出仍待交付。

### 当前轮次返工实施边界与测试入口

2026-10-11 分叉接力执行本责任返工；只继承原树 activeReopen。目标矩阵通过实际 Store/Service 端口创建合成运行：queued Rerun/Revise 取消被替代 Task，只领取当前轮次；有在飞租约时拒绝换轮且不改变任何事实；真正到期后合法换轮，旧 failed/completed/progress 拒绝并保持 Run/NodeRun/Task 及版本，随后当前报告成功；已接受旧结果的同租约重报与异摘要冲突保留原语义。不得通过手改业务数据制造目标通过。

新入口（包建立后实际执行并记录）：`go test -count=1 -json ./internal/execution/... -run CurrentRound`；隔离 PG 通过 `scripts/test-workflow-execution-integration.sh` 增加 `TestWorkflowExecutionCurrentRoundPostgres`，共用 `internal/execution/executiontest/current_round.go`。内存与 PG 的全字段 snapshot 仅供拒绝路径只读断言，不能代替真实端口调用。没有真实 WorkflowRun/Provider/GitHub 写入。

Characterization 使用已有 `S02-current-round` overlay 与另一只读树的旧在飞轮次探针，仅保留历史源码文本与新运行结果；修复后这些旧行为断言失败是问题消除，不将其纳入正常回归。原 wire/schema/迁移/common/decision 不变；最终 PR head CI 缺失时本地结果保持 IN_PROGRESS。

当前轮次本地实现已通过上述矩阵与原回归：独立限制领取为最新轮次；Memory 在换轮前拒绝在飞、再取消旧 queued；未接受报告核对当前轮次，已接受结果的幂等/冲突不随换轮丢失。只发布本地修复，不声称最终退出；currentStage/activeReopen 不因本地绿灯变化。

审阅补齐（2026-10-11）：共享矩阵增加 Task ID 碰撞、换轮后旧 Retry/Resume 拒绝及当前合法 Retry/Resume；PG 真实端口同时验证 r.version 语句。拒绝必须先于所有 mutation，并保留旧已接受结果重报。该范围仍属本 S02 原当前轮次/尝试身份合同。
