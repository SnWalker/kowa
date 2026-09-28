# S04：Runner/Provider 执行闭环

> 状态只在总体设计维护；执行事实进入 record/S04.md。

## 阶段设计依据

控制面不能执行 Agent 任务；独立 Runner 必须通过出站协议领取冻结输入、监管 Worker 并可靠报告。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现独立 Runner、出站长轮询、统一 Worker host 与至少一个真实 Provider Adapter 的可恢复执行闭环。

## 前置与跨端依赖

- 本端前置阶段：S03。
- 跨端阶段：无。
- 共享契约 owner：backend:S02。
- 冻结契约及版本：kowa.workflow-execution.v1。

## 必读

执行架构、执行尝试合同、Workflow/Capability 设计、身份安全设计、后端 Provider 验收清单、验收 A13—A18/B07/B10—B11/C07。

## 推荐 Skills

- `golang-how-to`
- `golang-concurrency`
- `golang-context`
- `golang-cli`
- `golang-troubleshooting`
- `golang-security`
- `golang-testing`
- `golang-observability`

## 核心文件与修改范围

### 核心文件

未来 Runner host、HTTP client、Worker/Provider ports、Codex/Claude adapters、工作区物化和进程监管测试。

### 允许修改

注册/探测、轮询、租约/心跳/报告、取消、结构化输出、session 恢复、任务目录、项目命令与 Git/gh 证据采集。

### 明确禁止

Runner 决定业务门禁、修改 S02 wire、把 CLI 存在当认证可用、声称同账号已硬隔离。

### 并行写入范围

独占 Runner/Worker/Provider Adapter；共享 wire 只能消费 S02 v1。

## 当前问题与证据

- CONFIRMED：出站长轮询、原生 Git/gh 和机器身份已选。
- CODE_CONFIRMED：无 Runner 实现。
- DATA_CONFIRMED：基线审查曾见 Codex/gh，不能替代本阶段复核。
- STRONG_INFERENCE：统一 host seam 可隔离 Provider 差异。
- HYPOTHESIS：Claude 可用性、取消延迟和 shell 沙箱能力待实测。
- 文档与源码差异：无实现。
- 证据不足项：真实 Provider 登录、Git remote、子进程停止和凭证可见范围。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

Server 生产 TaskLease/ArtifactRef；Runner 转换为 Worker 调用与报告；Workflow/审计消费接受结果。

## 本阶段新增或修改的模型

RunnerHost、Worker、ProviderAdapter、ExecutionWorkspace；不复制 Task/NodeRun 业务状态。

## 约束

重复交付不重复 Execute；输入来自中心引用；session 不是任务身份；模型/项目脚本分别记录版本、权限和退出证据。

## 实施清单

- [ ] 用 scripted fake 先覆盖断连、重复、取消和截断输出。
- [ ] 实现 Runner 出站协议和进程监管。
- [ ] 实现至少 Codex Adapter，Claude 不可用时明确 capability unavailable。
- [ ] 完成隔离测试数据上的本机 Git/gh 与目录负向检查。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；目标红灯覆盖 B11、C07、旧租约、响应丢失和本地目录依赖。

### 定向测试

mise exec -- go test ./internal/runner/... ./internal/provider/...

### 受影响回归、构建与静态门禁

mise exec -- go test ./...；mise exec -- go vet ./...；mise exec -- go build ./...；文档门禁；git diff --check。

## 运行时验收

在隔离测试仓和假凭证上执行真实本机 CLI、重启 Runner、移除上游目录并验证从正式引用恢复；不触发真实 WorkflowRun。

## 人工操作边界

若需登录/切换真实 Provider 或 gh、修改 Git helper，由用户操作；AI 记录水位后只读验收。

## 退出条件

Runner 协议合同测试、真实 Provider 最小适配、进程恢复和隔离负向结果均有证据；失败能力不被伪装为可用。

## 停止点

完成后停止，不实现 PR/Review/关闭门禁。
