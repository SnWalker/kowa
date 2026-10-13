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

- `CODE_CONFIRMED / BLOCKING_FAIL`：2026-10-13 S04 M1 准入审计确认两adapter实际 TaskLease producer 的 GitScope JSON 使用 `repository_id`，与冻结 schema 必需 `repositoryId` / 禁止未知字段冲突。S02 已登记 REOPENED；取消意图及更早返工已退出事实仍有效，不重做这些已闭环责任。
- `DATA_CONFIRMED`：新 Service/Store 探针与隔离 PG17.11 同场景，characterization 各1 PASS、目标各1 FAIL；固定完整 schema validator 拒绝两份实际产物、批准 fixture PASS，详见 record 最新时间线。
- 文档差异：PR28 已合入但接力/投影仍写 OPEN，已先勘误。新责任状态由总体设计持有。
- 证据不足项：尚无 producer 修复、永久正反矩阵或新交付 CI；transport/清扫/Worker 等待交付项仍归 S04。
- 固化旧错误的测试：没有发现直接保护 snake_case 的永久测试；TestTaskLeaseWireShape 使用空 scopes，fixture 门禁未检验实际 producer。旧错误断言只在 evidence overlay 的 characterize 模式。
- 拟新增目标：两adapter真实 LeaseTask 的非空 scope 实际序列化满足 schema、严格 consumer 不丢仓库身份；拒绝未知/缺字段，scope 无损；内部存储往返与对外 wire 区分验证，不改原冻结字段语义。

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

最早责任 owner 为 S02。原开工审计只登记 REOPENED 并停止 S04；后续 S02 修复与收口见下文。后继基线：S03 DONE，S04—S08 NOT_STARTED；完整 activeReopen 见总体设计。证据与复跑入口见 record/S02.md 的 2026-10-11 时间线。

下一返工会话只处理当前轮次的 producer（Rerun/Revise supersession）、transition（领取与租约现行性）及 consumer（进度/结果接受），核对内存与 PostgreSQL 等价不变量。先冻结既有问题，再建立目标红灯；拒收后现行事实不变且随后合法请求成功。保留 wire、迁移和依赖；不得顺带实现 S04。登记时尚未修复；2026-10-11 分叉接力的本地实现与门禁见下一节和 record/S02.md，最终远端退出仍待交付。

### 当前轮次返工实施边界与测试入口

2026-10-11 分叉接力执行本责任返工；只继承原树 activeReopen。目标矩阵通过实际 Store/Service 端口创建合成运行：queued Rerun/Revise 取消被替代 Task，只领取当前轮次；有在飞租约时拒绝换轮且不改变任何事实；真正到期后合法换轮，旧 failed/completed/progress 拒绝并保持 Run/NodeRun/Task 及版本，随后当前报告成功；已接受旧结果的同租约重报与异摘要冲突保留原语义。不得通过手改业务数据制造目标通过。

新入口（包建立后实际执行并记录）：`go test -count=1 -json ./internal/execution/... -run CurrentRound`；隔离 PG 通过 `scripts/test-workflow-execution-integration.sh` 增加 `TestWorkflowExecutionCurrentRoundPostgres`，共用 `internal/execution/executiontest/current_round.go`。内存与 PG 的全字段 snapshot 仅供拒绝路径只读断言，不能代替真实端口调用。没有真实 WorkflowRun/Provider/GitHub 写入。

Characterization 使用已有 `S02-current-round` overlay 与另一只读树的旧在飞轮次探针，仅保留历史源码文本与新运行结果；修复后这些旧行为断言失败是问题消除，不将其纳入正常回归。原 wire/schema/迁移/common/decision 不变；最终 PR head CI 缺失时本地结果保持 IN_PROGRESS。

当前轮次本地实现已通过上述矩阵与原回归：独立限制领取为最新轮次；Memory 在换轮前拒绝在飞、再取消旧 queued；未接受报告核对当前轮次，已接受结果的幂等/冲突不随换轮丢失。本地验证时尚未退出；代码 head CI 后按下文完成收口，不因单独本地绿灯推进状态。

审阅补齐（2026-10-11）：共享矩阵增加 Task ID 碰撞、换轮后旧 Retry/Resume 拒绝及当前合法 Retry/Resume；PG 真实端口同时验证 r.version 语句。拒绝必须先于所有 mutation，并保留旧已接受结果重报。该范围仍属本 S02 原当前轮次/尝试身份合同。

退出收口（2026-10-11）：当前轮次责任返工的原合同、producer/transition/consumer、新共享矩阵、原回归、隔离 PG 与代码 head CI 均已交叉验证；退出证据在 record/S02.md。已恢复原 S04 NOT_STARTED，最后文档提交的三项 CI 必须独立核验，最终 SHA/run/job 记录在 PR #26 描述；PR 未合并，不执行 S04。


## 2026-10-13 取消意图责任重开记录

PR #26 已合入且最新 main `e7a6bb5` 三项 CI 成功；本次新的直接证据与上一轮 queued Rerun 修复分开。合法领取 → CancelRun 持久化 CANCELING → 未过期在飞 failed ReportResult，MemoryStore 将运行改为 PAUSED；PostgreSQL 同矩阵保持 CANCELING。违反 [v1 冻结取消规则](../../wiki/contracts/workflow-execution-v1.md)“取消先停止新派发；仍有在飞租约时 CANCELING，收束后才 CANCELED”。结果接受不得抹除取消意图；不把最终 CANCELED 或未交付清扫器当本次目标。

最早 owner 为 S02，DONE→REOPENED、currentStage=S02；后继基线 S03 DONE、S04—S08 NOT_STARTED 不变。characterization 及目标红灯使用证据目录 overlay，不修改生产代码或永久回归；精确命令、适配器结果、源码与继续范围见 record/S02.md 最新时间线。责任登记会话到重开即停止；后续串行接力先冻结旧行为和目标不变量；不在 S04 打补丁。规划漏项（epoch 排序、清扫、新 transport/Worker 端口）仍归 S04，wire/迁移不变。

### 2026-10-13 串行接力初版实施合同（后续交付修正见下节）

本次只修 MemoryStore 在 CANCELING 下接受 failed 结果时覆盖取消意图的原合同违约；与 PostgreSQL 既有 ACTIVE 条件对齐。Characterization 适用且只留证；永久共享 `CancellationIntent` 矩阵通过真实 Service/Store 端口驱动 LEASED/RUNNING × completed/failed/awaiting_input，验证取消后 Run 状态及版本保持、Task/NodeRun 结果接受、停止派发、拒绝路径全字段不变及随后合法报告成功、重复/冲突和真正过期；正常 ACTIVE 失败仍 PAUSED。queued 取消仍 CANCELED。

测试入口：`go test -count=1 -json ./internal/execution/... -run CancellationIntent`；`bash scripts/test-workflow-execution-integration.sh` 纳入 `TestWorkflowExecutionCancellationIntentPostgres`，同一共享矩阵。受影响回归、全量/race、make verify、固定 lint、治理与 diff 门禁记录实际证据。本次不实施 Retry/Resume 取消交错、最终取消清扫、transport 或 Worker；wire/schema/迁移/依赖不变。

本地门禁通过后仍保持 IN_PROGRESS 与 activeReopen；最终代码/退出 head CI 是原退出条件，未授权 Git 交付时不标 DONE、不清空重开、不推进 S04。

本轮本地结果：Memory/PG取消矩阵各21叶子通过，定向108、全量/race各187、PG完整81叶子通过；make verify、固定lint、治理/diff通过。结果仅证明上述状态核心边界，不证明S04清扫或真实Runner可用；最终head CI未取得，保持IN_PROGRESS。

### 获准交付审阅发现的同 owner 取消旁路（2026-10-13）

初审窄结果修复无Spec阻断；源码风险随后由新Service端口/隔离PG直接证据升级为BLOCKING_FAIL：Cancel→合法failed/awaiting_input→Retry/Resume，以及无在飞时Rerun/Revise，Memory在CANCELING/CANCELED均恢复ACTIVE并再次派发，PG恢复命令同样复活且换轮漏CANCELING（CANCELED换轮本来正确拒绝）。违反原v1“取消先停止新派发”及取消生命周期；同一S02责任，不是S04清扫新需求。Characterization两适配器各8PASS，目标红灯Memory8FAIL/PG6FAIL2PASS，源码/脚本/原始日志留证。

本轮必要修正边界补为：Memory在已有mutex下、PG在已有Run事务锁下拒绝CANCELING/CANCELED的Retry/Resume/Rerun/Revise，拒绝必须先于任何mutation；原正常ACTIVE/PAUSED恢复和换轮仍成功，accepted report的幂等/冲突不变。永久共享CancellationIntent追加取消恢复矩阵与正常对照；无新增字段、权威、锁序、依赖或迁移。原窄矩阵是历史证据，不再作为本次全部退出范围。最终head CI前保持IN_PROGRESS。

交付版新验证：两adapter共享各33叶子、定向120、全量/race各199、PG完整93；make verify、固定lint均通过。Standards/Spec独立复审无剩余阻断；取消恢复纳入原owner责任，最终head CI前不退出。

### 取消意图返工退出登记（2026-10-13）

最终实现范围包含合法结果保持取消意图及取消态恢复命令拒绝；原v1字段/版本不变。新characterization/目标红灯、共享33叶子、定向/全量/race/隔离PG、make verify/lint与独立Standards/Spec复审及代码head三项CI全部成功，见record与S02-cancellation-exit-code-ci.json。按原后继基线重新DONE并恢复S04 NOT_STARTED，完整责任周期后清空activeReopen。退出文档新head仍必须独立核验三项CI，最终SHA/run/job由PR28描述持有；未合并、不执行S04。

## 2026-10-13 责任重开：GitScope wire producer

本次只做责任登记并停止。被违反的原冻结承诺是本阶段“冻结并实现跨端/Runner wire v1”和退出条件“机器 schema / Producer/Consumer 核对”，不是 S04 新增 transport 需求。`internal/execution/types.go` 的 GitScope 是两adapter共用序列化类型，实际 LeaseTask 非空 scopes 必须满足 `api/workflow-execution/v1/schema.json` 的原字段；不修改 schema 来迎合错误 producer，也不允许 Runner 兼容旧字段。

下一串行返工核对 Service.CreateRun 输入、Task scope 持久化、taskSpec/executionTaskSpec 映射、JSON serializer 与严格 consumer；内部 PG JSONB 与对外 TaskLease 各有自己的边界，修复须保护既有 scope 数据读取。Characterization 适用；证据 overlay 不进入正常回归。复跑入口 `bash doc/Kowa后端设计/record/evidence/S02-git-scope-wire-audit.sh characterize /tmp/kowa-wire-characterize` 与 `target /tmp/kowa-wire-target`，使用命令级固定 Go。新永久矩阵、修复和退出归下一会话，本次不执行。

### 2026-10-13 GitScope wire producer 责任返工合同（本地实施）

本次只修共用 `GitScope` 对外/对内 wire 的字段名：`internal/execution/types.go` 的 `GitScope.RepositoryID` JSON tag 由 `repository_id` 改为 `repositoryId`。被违反的原冻结承诺是 S02 唯一目标的“跨端/Runner wire v1”与退出条件的“机器 schema、Producer/Consumer 核对”；`api/workflow-execution/v1/schema.json` 的 `$defs.GitScope` 保持原 `repositoryId`/`additionalProperties:false`，不改 schema、迁移、依赖或 common/decision。

内部存储与外部 wire 边界：`GitScope` 同时被内部 `Task`（PG `execution_task.git_scopes` JSONB，读写均 `json.Marshal/Unmarshal` 同一 struct）与外部 `TaskSpec`（`executionTaskSpec` → `TaskLease.task.gitScopes`）使用；无 SQL 按键名读取该列，迁移不写入 `execution_task` 行，因此单 tag 修复同时校正内部往返与外部投影，无需迁移。Runner 不新增接受 snake_case 的兼容旁路。

永久目标矩阵：共享 `internal/execution/executiontest/git_scope_wire.go`（`GitScopeWire`），内存入口 `TestMemoryStoreGitScopeWireContract`、PG 入口 `TestWorkflowExecutionGitScopeWirePostgres`，均通过真实 Service/Store 端口：非空多 scope 经内部存储往返及 `TaskLease` 投影无损、严格 consumer（v1 字段名 + `DisallowUnknownFields`）无损取得 repositoryId/role/operation/ref、完整冻结 schema（仓库固定 `jv@v0.7.0`）接受实际产物；缺 `repositoryId`、未知 `repository_id`、非十进制 `repositoryId` 按冻结 schema 拒绝；非法 scope 的 CreateRun 拒绝后现行事实不变、随后合法请求成功；Retry/Resume/Rerun 恢复路径保留 scope。

定向入口：`go test -count=1 ./internal/execution -run GitScopeWire`；PG 由 `scripts/test-workflow-execution-integration.sh` 纳入。契约门禁 `scripts/check-workflow-execution-contract.sh` 增加用固定 `jv` 校验 `positive-*.json` 与完整 schema，与身份/知识契约门禁对齐。旧错误 characterization 只保留在 evidence overlay，不进入正常回归；修复后其“old wire symptom changed”失败即为问题消除。

### 2026-10-13 GitScope wire producer 责任返工退出登记

本地门禁全绿（内存矩阵 8/8、定向 141、全量/race 各 232、隔离 PG17.11 113、make verify、固定 lint 0、契约/治理/diff）；交付 [PR #29](https://github.com/SnWalker/kowa/pull/29)，代码 head `33b149a2a9dc79bd21c5dbdcea178ea42a25b5d0` 的必需 lint/verify 与 web-verify 全部 SUCCESS（Backend run 38411879422：verify job 115346027272、lint job 115346027827；Frontend run 38411878467：web-verify job 115346024699）。原 v1 字段/语义与迁移不变，无兼容 facade。按后继基线重新 `DONE` 并恢复 S04 NOT_STARTED，责任周期完整后清空 `activeReopen`。退出文档形成新 head 仍须独立核验三项 CI，最终 SHA/run/job 由 PR #29 描述持有；未合并、不执行 S04。
