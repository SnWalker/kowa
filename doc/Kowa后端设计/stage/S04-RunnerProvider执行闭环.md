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

- `../总体设计与进度.md`、`../当前阶段与下一步.md`、`../验证规则.md`。
- `../../wiki/architecture/workflow-execution.md`、`../../wiki/contracts/execution-attempts.md`、`../../../api/workflow-execution/v1/README.md`；S02 v1 是本阶段不能重释的上游。
- `../../research/workflow-capability-design.md`、`../../research/mvp-identity-security-design.md`：只消费未被上位共识替代的候选设计，不把历史 Agent 直接 Git/gh 表述当作现行授权。
- `../MVP模块与数据设计.md` 的 Provider 最小适配验收清单、`../../Kowa验收与运行/MVP验收矩阵.md` 的 A13—A18/B07/B10—B11/C07。
- `../../research/s04-prerisk-spike-20261002.md` 的 §8、§10、§13，以及 `../../Kowa验收与运行/测试仓库约定.md`。spike 是带日期的历史证据，C1 的当前决定以 common §2.7/§3.3 与 decision 为准。

## 推荐 Skills

- `golang-how-to`
- `golang-concurrency`
- `golang-context`
- `golang-troubleshooting`
- `golang-security`
- `golang-testing`
- `golang-observability`

## 核心文件与修改范围

### 核心文件

未来 Runner host、HTTP client、Worker/Provider ports、Codex/Claude adapters、工作区物化和进程监管测试。

### 允许修改

注册/探测、轮询、租约/心跳/报告、取消、结构化输出、session 恢复、任务目录与项目命令；Runner 受控 Git 通道的获准操作检查、冻结仓库物化、commit/push、身份与 OID 证据及未知副作用对账；Runtime 探测/授权查询、执行配置和本阶段组合根。PR/Review/关闭业务门禁仍由 S05 拥有。

### 明确禁止

Runner 决定业务门禁、修改 S02 wire、把 CLI 存在或登录态当实际可执行、把宿主默认模型当冻结配置、声称同账号已硬隔离；让 Coder 直接使用宿主 Git/gh 凭证作为正常交付路径；提前实施 S05 的 PR/Review/关闭门禁或 S06 的固定工作流。

### 并行写入范围

独占 Runner/Worker/Provider Adapter；共享 wire 只能消费 S02 v1。

## 当前问题与证据

- `CONFIRMED`：出站长轮询、机器 GitHub 身份及 Runner 受控 Git 通道已选。C1 已由 PR #21 落地；Coder 只交付工作区变更，Runner 在获准 Task 的 `gitScopes` 范围执行物化、commit/push，不把机器权限交给 Agent 作为交付接口。
- `CODE_CONFIRMED`：已有 `cmd/runner/main.go`、`internal/runner/app.go` 与 Runtime 生命周期骨架；尚无本阶段的领取/执行/报告闭环及 Provider Adapter。
- `DATA_CONFIRMED`：2026-10-02 spike 记录 stdin、凭证路径、进程取消与未知远端副作用；这些实验不等于 S04 新实现已验收。2026-10-10 §13 已更正 Codex 默认模型不可用的结论：0.162.1 与当时账号默认模型可执行，Runner 仍应显式固定并记录实际模型/参数。非交互关闭 stdin 的历史要求在 Adapter 中重新验证。
- `STRONG_INFERENCE`：统一 Worker host 可以隔离 Provider 协议差异；同账号清理环境和控制调用路径不能单独证明凭证与文件硬隔离。
- `HYPOTHESIS`：并发任务、Runner 重启、session 恢复、长轮询取消延迟、环境标记被清洗后的进程残留发现待验证。
- 文档与源码差异：2026-10-11 准备会话已将本合同的 C1 待裁决、Codex 默认模型不可用、无 Runner 骨架等旧措辞勘误；完整影响与验证记录见 `../record/S04.md`。本次只完成治理准备，不表示执行闭环已实现。
- 证据不足项：当前 Provider 的真实最小执行与隔离、支持分支保护的验收环境、双仓真实物化与知识仓负向、Server 读凭证路线和取消恢复证据。第二真人账号 `snflyer` 已是沙箱 write 协作者；有效 Review/合并的真实验收归 S05，账号就绪不等于该验收已通过。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

Server 生产 TaskLease/ArtifactRef；Runner 转换为 Worker 调用与报告；Workflow/审计消费接受结果。

## 本阶段新增或修改的模型

RunnerHost、Worker、ProviderAdapter、ExecutionWorkspace；不复制 Task/NodeRun 业务状态。

## 约束

重复交付不重复 Execute；输入来自中心引用；session 不是任务身份；模型/项目脚本分别记录版本、权限和退出证据。

- S02 的 epoch 不透明、无冻结排序；新 epoch 替换当前登记并使旧租约失效，同 epoch 登记内容不可变。可变探测观察不能绕过这些规则悄悄改写登记。
- 长轮询路由/时限/退避、持久去重与重启恢复、Runtime epoch 生成与旧登记迟到、心跳与实际租约到期清扫、取消收束必须在 M1 写出目标合同和正反测试。不假设心跳自动续租；若实现需要 S02 未交付的端口或语义，先定位是规划漏项还是原合同违约，不改 wire 或在 Runner 自造业务状态。
- Git 操作先核对当前 Task、仓库身份、角色、ref、操作和执行身份；知识仓只物化不提交，项目仓只提交到获准工作分支。不对未知 push 结果盲重试；按固定 OID/远端事实对账。
- Coder 不经受控通道发起远端操作；宿主个人凭证的物理可见范围仍需负向实测，不能把此逻辑边界描述为硬隔离已成立。
- Server 读取 GitHub 事实的凭证路线尚未冻结，fake 只读端口可先验证；真实查询失败或身份漂移必须保持未知。独立只读 PAT 未准备不阻止 M1 fake 开发，但不能以假凭证证明真实观察可用。

## 阶段内里程碑与准入

保留唯一 backend:S04；M1—M4 是本阶段内部的顺序工作包，不新增阶段编号、状态表或跨阶段执行权。前一包的目标与验证证据完整后再扩展下一包；任一包完成都不等于整个 S04 DONE。

| 工作包 | 修改与合同冻结范围 | 定向正反证据 | 真实前置 |
| :--- | :--- | :--- | :--- |
| M1 监管内核与 transport | 出站协议、租约循环/清扫边界、epoch、持久去重、Worker host、取消/结果解码 | scripted fake：断连/重复/响应丢失不重复执行；重启先查明；旧租约不污染现行事实；截断/缺结果拒收；取消保留残留证据 | S02 v1、S03 已退出；不需要 Provider 或 GitHub 写入 |
| M2 Provider 与 Runtime 探测 | Codex Adapter、Claude unavailable 路径、版本/模型/参数、环境与秘密过滤、session 恢复、探测观察 | fake 失败矩阵与 schema；少量新真实 CLI 调用验证存在/兼容/认证/可执行四层，执行终态和结构化结果一致；session 失效不丢持久答复 | 用户维护本机登录/API；真实调用需在当次会话获准 |
| M3 工作区与受控 Git | 项目/知识固定提交物化，`gitScopes` 对应操作、机器身份、commit/push 与未知副作用对账 | fake Git 命令负向；隔离目录清理后按固定引用重建；新真实沙箱引用/OID/命令证据；知识仓写操作在通道拒绝 | 双仓基线、实际凭证、范围明确的写入/清理授权；平台保护与宿主绕过风险单独核验 |
| M4 授权查询与组合根 | Runtime observation/transport、runtime-web v1、管理员执行配置、Server 只读观测 seam 和生产装配 | 实际 producer/schema 与组合根；跨 Workspace/撤权/旧 epoch/陈旧水位/重复冲突/秘密过滤正反证据 | 真实 GitHub 观察前选定读凭证；不实现 S05 Review/关闭门禁 |

S04 运行时验收按下节授权层次执行。知识仓基线、平台保护和读凭证准备详见 `../../Kowa验收与运行/测试仓库约定.md`，不复制到本合同成为第二环境权威。

## 实施清单

- [ ] 用 scripted fake 先覆盖断连、重复、取消和截断输出。
- [ ] 实现 Runner 出站协议和进程监管。
- [ ] 实现至少 Codex Adapter，Claude 不可用时明确 capability unavailable。
- [ ] 完成隔离测试数据上的本机 Git/gh 与目录负向检查。
- [ ] M1 先冻结 epoch/清扫/取消与未知执行的恢复规则，再建立目标红灯；M2—M4 依顺序交付上述证据。
- [ ] 落实 C1 受控 Git 通道，清除本阶段内让 Agent 直接远端 Git/gh 的旧消费者或临时兼容路径。
- [ ] 发布 runtime-web v1 的实际 schema、操作/层次、冻结摘要和消费者正反证据，不把候选合同当完成交付。

## 测试与自动验证

### Characterization 与目标红灯

Runner 执行闭环和 Provider Adapter 是纯绿地，骨架没有既有任务执行行为可冻结，characterization 对这部分不适用；若修改现有 S02/S03 行为，仍须先建立该行为的 characterization，不因本阶段绿地整体豁免。目标红灯覆盖 B11、C07、旧租约、响应丢失、本地目录依赖及 M1—M4 的拒绝后事实保持与后续合法请求成功。

### 定向测试

准备会话尚未建立 `internal/provider`，不得把不存在包的命令记为已通过。实施者建立包与首批目标测试后，执行并记录真实红灯与绿灯：

```bash
mise exec -- go test ./internal/runner/... ./internal/provider/...
```

新增其他包的定向命令在实现时按实际路径补齐；文档准备仅运行治理检查及自测，不虚构业务红灯。

### 受影响回归、构建与静态门禁

mise exec -- go test ./...；mise exec -- go vet ./...；mise exec -- go build ./...；文档门禁；git diff --check。

## 运行时验收

区分三层，不互相替代：

1. scripted fake、假凭证、一次性隔离数据验证确定性合同；不调用真实 Provider/GitHub 写入。
2. 获准的真实本机 Provider 最小执行记录新探测 ID、实际 CLI/模型/参数、耗时、终态和严格结果；仅证明当时探测能力，不证明 Kowa Runner 闭环。假凭证用于拒绝路径，不能证明真实认证成功。
3. 用户于当次会话授权后的沙箱物化与工作分支操作，记录双仓 OID、实际 GitHub 身份、操作前后 refs、未知副作用对账、Runner 恢复与上游目录移除后的新证据；不借此触发真实 WorkflowRun，PR 业务门禁归 S05。

Provider 波动在确定性链路合法时为 OBSERVATION_ONLY；Kowa producer/transition/consumer 违反冻结合同为 BLOCKING_FAIL。真实能力 unavailable 必须如实表示，必要真实退出证据缺失时整个 S04 不得 DONE。

## 人工操作边界

登录/切换真实 Provider 或 gh、配置凭证/Git helper、账号计划升级与分支保护、App 授权由用户操作，除非当次任务明确另行授权。运行真实 Provider、沙箱写入或清理也须明确范围；历史 spike 或本阶段文档不构成常设授权。需要人工操作时，先完成当时可运行的自动门禁，记录版本与 refs 水位，给出精确清单；实施已开始时按 AGENTS §6 登记 HUMAN_ACTION_REQUIRED 并停止。阶段尚 NOT_STARTED 的治理准备不为后续验收提前制造状态迁移。

## 退出条件

Runner 协议合同测试、真实 Provider 最小适配、进程恢复和隔离负向结果均有证据；失败能力不被伪装为可用。

## 停止点

完成后停止，不实现 PR/Review/关闭门禁。

## 补充交付边界（2026-10-01）

交付层次：http 与执行旅程。保持原 Runner/Provider 完整目标，补交 Runtime 探测观察、受控 Workspace 授权范围、执行/验证配置和 Web 查询及 Server 装配。S01/S02 已确认责任修复退出后方可实施；消费 S01 身份授权、S02 核心 wire、S03 固定输入/Artifact，不能另造业务状态。

本阶段唯一拥有 Runtime observation/transport 与 runtime-web 合同（目标 v1，待冻结）。分别呈现 CLI 存在、版本兼容、认证和可执行；机器 gh 与 Web actor 分离；陈旧/未知/离线不能假绿。查询逐请求验证会话、成员和 Runtime 归属；Runtime 自报不能授予 Workspace，查询不触发执行。配置仅管理员可变，含版本/幂等，S06 消费冻结配置。

退出增加：实际探测/报告/投影链路、跨 Workspace/撤权/旧 epoch/重复冲突/陈旧水位/秘密过滤正反测试和真实组合根；发布操作/层次登记及冻结摘要。不得把部分查询完成当 S04 全部 DONE。


## 2026-10-13 M1 开工审计停止

最新绿色 main 已核验，PR #26 合入事实已同步接力和投影。M1 准入审计发现 S02 MemoryStore 在取消后接受在飞失败报告，将 CANCELING 改为 PAUSED；同矩阵 PostgreSQL 保持取消意图。按原冻结取消规则登记 S02 REOPENED 并停止。M1 未冻结、未实现，S04 保持 NOT_STARTED 后继基线；责任与复现入口见 [S02 最新记录](../record/S02.md)。这不是 S04 尚未实现清扫器的缺口，也不重开 S02 的不透明 epoch 排序规则。返工退出并恢复 S04 前沿后，新的 M1 会话再执行目标合同与红灯。

## 2026-10-13 M1 准入审计：wire producer 责任停止

PR28 已合入 main 6721e233 且三项 CI SUCCESS 后，实际 producer 审计确认非空 GitScope 使用 `repository_id`，被冻结 S02 v1 `repositoryId` / additionalProperties=false 拒绝。两adapter与机器 schema 新证据见 [S02 最新记录](../record/S02.md)。已登记最早 owner S02 REOPENED；S04 保持 NOT_STARTED 后继基线。M1 详细合同未冻结、未建立 M1 红绿灯、未实施；不以兼容解码绕过上游 wire 责任。清扫/epoch传输/去重/Worker等规划漏项仍归 M1，不混入此重开范围。

## 2026-10-14 M1 目标合同冻结与实施（IN_PROGRESS）

M1 准入复核（本会话）：以 HEAD `3a4242a1`（=origin/main，PR #29 合入）为基线，S02 责任返工已全部退出；全量单测 232/232 pass、隔离 PG17.11 workflow-execution 集成矩阵 PASS、main 三项 CI 在该 SHA success。此前三次 M1 停止引用的违约（cancel intent、current round、GitScope wire producer）均有绿测覆盖，M1 准入依赖（S02 v1 冻结、S03 DONE、不需要 Provider 或 GitHub 写入）继续成立。M1 详设、实现与证据见 [record/S04.md](../record/S04.md) 最新时间线。

以下为 M1 冻结目标合同；实现与正反测试以本节为准，后续包不得静默改写：

### M1-1 出站 HTTP 传输（S04 唯一拥有；不改 S02 wire/schema）
- 路由（仅 `KOWA_RUNTIME_ENABLED=true` 且配置了 Runtime 凭证与数据库时挂载）：
  - `POST /api/v1/runtime/register`：body 为冻结 v1 `RuntimeRegistration` 严格解码；`Authorization: Bearer` 凭证绑定 runtimeId，body.runtimeId 必须等于绑定值，否则 403；无/坏凭证 401；同 epoch 异声明 409 VERSION_CONFLICT；否则 200（幂等或换代）。响应与所有传输应答为 S04 传输 envelope，不是 v1 kind。
  - `POST /api/v1/runtime/tasks/lease`：body `{runtimeId, runtimeEpoch, waitSeconds, leaseSeconds}`；waitSeconds 夹到 [0,50]，服务端每 250ms 调一次 S02 `LeaseTask`；有任务→200+冻结 v1 `TaskLease`；等待期满→204 空体；epoch 非当前→409 RESOURCE_UNAVAILABLE。收到租约即视为交付；响应丢失不由传输层补偿。
  - `POST /api/v1/runtime/tasks/{taskId}/progress`：body 冻结 v1 `ProgressEvent`，路径与 body.taskId 必须一致；200 传输 ack。进度不续租。
  - `POST /api/v1/runtime/heartbeat`：传输层 body `{runtimeId, runtimeEpoch, taskIds[]}`；心跳仅存活观察，绝不改租约到期；响应 `{cancelTaskIds:[...]}`。epoch 非当前→409。
  - `POST /api/v1/runtime/tasks/{taskId}/result`：body 冻结 v1 `TaskResult`；200 `{accepted:true, duplicate:bool}`；同身份异 digest→409 VERSION_CONFLICT；旧租约/旧 epoch→409 STALE_RESULT。
  - `GET /api/v1/runtime/tasks/{taskId}`：只读查证（重启恢复用）；仅本 Runtime 的任务，否则 403；返回最小传输视图 `{taskId, state, leaseId, fencingToken, runtimeEpoch, leaseExpiresAt, resultAccepted}`，不含 scopes/输出。
- 错误体一律冻结 v1 `APIError`；映射 400/401/403/409/500 按错误类别。
- 凭证：服务端配置 runtimeId→token；token 只出现在 Authorization 头，绝不入日志/证据；撤销=移除配置+重启（MVP 单机），撤销后仅凭 runtime_id 不能恢复访问。
- 客户端：网络与**任何 5xx**（包括 503 承载的 RESOURCE_UNAVAILABLE，例如传输未启用的关闭失败 shim）都是传输暂时不可用，退避 1s→2s→4s→上限 30s（抖动，成功后复位）并保留 journal；"已结算"仅限 STALE_RESULT、FORBIDDEN、404 资源不存在与 409 superseded；401/403 与 epoch 冲突为致命——Runner 停止并以非零码退出，不静默重注册抢占。

### M1-2 epoch 与旧登记迟到
- Runner 的 epoch 持久化在 `<workRoot>/state/runtime.json`，随状态生成一次（≥128bit 随机、不可排序）并在重启后复用，使重启前已租约的尝试与已算出但未上报的结果仍属当前代、可按 M1-5 收束；清空/更换状态目录或声明冲突时轮换新 epoch。
- 声明冲突（同 epoch 异声明）语义保持 S02：控制面 409 拒绝且不落地；Runner 收到后自动轮换一次 epoch 并重新登记（声明确实变了，例如 Provider CLI 升级）；再次冲突即致命退出，不循环。
- 控制面保持 S02 语义：同 epoch 同声明幂等、新登记替换当前代。epoch 无冻结排序，"旧登记迟到"即最近一次认证登记为当前代；同 runtimeId 并进程是 MVP 不支持场景（凭证撤销+纪律）。固定正反测试后果：旧 epoch 的 lease/heartbeat/progress/result 全部 409（STALE/RUNTIME），不发生状态变化；Runner 收到后停止工作并退出。

### M1-3 租约、心跳与清扫
- 心跳不续租不延长；租约时长由 Runner 在 lease 请求给出（`KOWA_RUNNER_LEASE_SECONDS`，默认 1800）。
- Runner 执行死线 = min(TaskSpec.deadlineAt, lease.expiresAt − 30s 报告余量)；到点先收束进程再上报，保证租约有效期内写回。
- 清扫（控制面后台 15s 周期；Memory/Postgres 等价实现）：LEASED/RUNNING 且 `lease_expires_at ≤ now` → EXPIRED；CANCELING 且该 run 无 LEASED/RUNNING 任务 → CANCELED（version++，updated_at=now）。清扫不改已接受结果、不触碰 QUEUED、不复活任务。

### M1-4 取消收束
- Run 进入 CANCELING 后 QUEUED 任务立即 CANCELED（S02 既有）；Runner 经心跳响应收到 cancelTaskIds（条件：run∈{CANCELING,CANCELED} 且 task∈{LEASED,RUNNING,EXPIRED}）。
- Runner 收到取消：进程组终止+逃逸残留扫描，残留证据写任务目录；租约仍有效则上报 failed(`canceled`, retryable=false)，已 STALE 则丢弃并记本地证据。
- 取消期 failed 报告不得把 CANCELING 改回 PAUSED（S02 #28 已修）；清扫在无在飞任务后置 CANCELED。

### M1-5 持久去重、重启与未知执行恢复
- Runner 先写 `<workRoot>/state/attempts/<taskId>.json`（收到租约后、执行前）再执行；结果先写 `<workRoot>/state/results/<taskId>.json`（精确上报字节）再上报。
- `resultDigest` 摘要对象（消除自引用）：对**结果内容**的 canonical JSON 计算——即 `{status, summary, output|error|inputRequest}` 四类内容字段（`omitempty` 语义固定：completed 含 output、failed 含 error、awaiting_input 含 inputRequest），**不包含** `resultDigest`、`schemaVersion`、`kind`、`taskId`、`leaseId`、`fencingToken` 与自身；S02 冻结语义（相同身份同 digest 幂等、同身份异 digest 冲突）在 `TaskResult` envelope 上按该 digest 比较，与 S02 的 `ResultReport` 校验/幂等实现一致。上报与重发使用同一持久化字节序列（含 digest），故重发恒为 duplicate，不需要重算摘要；持久化字节与幂等重放由 M1 循环测试固定（响应丢失→duplicate、journal 重放→duplicate）。
- 同一 node 的最新 provider session 另存 `state/sessions/`，仅用于 resume 优化，缺失不改变业务结果。
- 重启恢复：先做残留进程扫描（env 标记，见 M1-6）；逐条非终态 journal：有结果文件→按原字节重报；无→`GET task` 查证：租约仍属我且未过期→上报 failed(`runtime_restarted`, retryable=true)；已接受/已过期/已换代/已取消→仅本地证据并清 journal。绝不以重启为由重新 Execute。
- 传输重复/响应丢失：同一 taskId 同一 lease 不产生第二次执行；结果交付短暂失败在报告窗口内退避重试，超窗保留 journal 供下次启动重发。

### M1-6 Worker host 与进程监管
- host 接受 {TaskSpec, LeaseIdentity, Provider Adapter, Workspace, Deadline, Env}，返回 {Outcome, Telemetry, Residual[]}。
- 子进程新进程组启动，stdin=/dev/null，cwd=任务目录，env 仅白名单（PATH/HOME/LANG/LC_ALL/任务隔离 TMPDIR + 显式非秘密附加项；匹配 token/secret/key/password/credential 的项拒绝启动）+ 每尝试随机 env 标记。
- 到点/取消：进程组 SIGTERM→10s 宽限→SIGKILL；随后按 env 标记扫描逃逸进程（setsid 残留），残留证据（PID/命令摘要/时间）写 `<workspace>/residual.json`；取消/超时绝不伪装成功。
- 严格结果解码：DisallowUnknownFields+必填+枚举+无尾随数据；截断/缺结果/多对象一律拒收为 `output_invalid`。
- 错误分类至少：`provider_timeout/provider_auth/provider_quota/provider_network/provider_protocol/output_invalid/canceled/runtime_restarted/deadline_exceeded/workspace_error`；仅明确可重试类 retryable=true。

### M1-7 范围说明
- **冻结输入交付链归 S04（2026-10-14 合同反馈后勘误）**：按 S03 冻结的 Runner consumer 行（"S04 后续按冻结 EffectiveInput 与 ArtifactRef 获取/校验，不能读生产者目录或分支最新值"），S04 拥有：受权获取、摘要校验、交付 Worker 的实际消费链（路由/客户端/校验/任务目录交付/正反测试），见"M4-5 冻结输入交付链"。`task → 冻结输入/ArtifactRef` 的**绑定生产者**（run admission 在派发时持久化 node run 的输入引用）仍属 S05/S06；S04 在合同与本记录中登记该 seam 与预检结论，不以 probe/fake 替代消费链本身。
- M1 不做 Provider 真实业务调用（M2）、不动物化/Git（M3）、不做 runtime-web/配置（M4）。新增 `execution.Store` 读端口（GetTask）与清扫操作属传输阶段规划漏项，不改既有 wire 与状态语义。
- 证据规范（2026-10-14 合同反馈后固定）：所有 record/evidence 捕获必须保留实际测试命令的退出码与 stderr（`set -o pipefail`、`PIPESTATUS[0]` 或先落盘再判读），不得只保存管道末端命令的退出码；审计已存证据如有管道取码问题须重捕获。

### M2 Provider 适配与探测（2026-10-14 实施并实测）

- **冻结 envelope**：所有 adapter 的结构化输出必须是 `{status: completed|awaiting_input, summary, output|inputRequest}`，经严格解码（未知字段/尾随/截断/缺终态一律拒收为 `output_invalid`）；capability 自身结果放 `output`。
- **Codex adapter**：`codex exec -m <显式模型> [--ephemeral] --sandbox <workspace-write|read-only> --json --ignore-user-config --ignore-rules --output-schema <文件> -o <文件> [resume <thread_id>] <prompt>`；stdin 关闭由监管层保证；**exit 0 不足以判定完成**——必须同时出现 `turn.completed` 事件且 envelope 可严格解码，否则 `output_invalid`；`thread_id` → SessionRef；失败按报文分类 `provider_quota/provider_auth/provider_network/provider_protocol`。0.162.1 实测 `-s/--sandbox`、`--ephemeral`、`--ignore-user-config`、`--ignore-rules`、`--output-schema`、`--json` 均存在。
- **Codex 严格 schema 承载（2026-10-14 真实探测驱动）**：真实 CLI 对 `--output-schema` 有严格模式约束——每个对象必须显式 `additionalProperties:false`，且 `required` 必须列出全部属性，因此自由形态的 `output` 对象无法声明。冻结合同不变（Outcome 仍为 `status/summary/output|inputRequest`）；Codex 的 schema 承载改为 `outputJson/inputRequestJson`（JSON 编码字符串，允许空串表示未用），adapter 严格解析回冻结 envelope 后再走同一套规则；Claude 继续用直接对象形态。真实两层探测由 canExecute=false（invalid_json_schema）逐条修正到 canExecute=true。
- **Claude adapter**：`claude -p --output-format json --json-schema <冻结 envelope> --strict-mcp-config --mcp-config <空 MCP 文件> --setting-sources user --max-turns N [--model] [--no-session-persistence] [--resume <session_id>] <prompt>`；`session_id` → SessionRef。2026-10-14 对 2.1.296 实测并据此冻结两点：`--setting-sources project` 在无环境凭据时会因排除用户级凭据而 "Not logged in"（改用 `user`：保留宿主登录、排除仓库级设置）；`--mcp-config` 需文件路径而非内联 JSON（adapter 每尝试写空 MCP 文件）。
- **resume 失效回退**：会话类失败（报文含 session/resume 语义）→ 去掉 resume 以完整输入重跑一次；实测两 adapter 均回退成功且任务不失败。
- **探测分层**：existence/version/auth 在注册前执行（version 缓存）；第 4 层"真实最小执行"仅在 `KOWA_RUNNER_PROBE=execute` 时执行并记录四层（消耗配额）；注册的 `providerAuthenticated` 只取认证层，`canExecute` 如实进入观察日志，不伪装可用。
- **环境与秘密**：adapter 只消费 host 构建的白名单 env；**三层探测与第 4 层执行使用同一白名单**（组合根显式注入 Worker 白名单；未注入时 adapter 默认仅转发 PATH/HOME/LANG/LC_ALL/TERM，绝不透传 Runner 自身凭证或其它 secret 形态变量）；证据与 telemetry 不含 env 值、prompt 正文或凭据。
- **真实证据**：`record/evidence/S04-m2-real-provider-probes.txt`（Claude canExecute=true / 版本 / 结构化终态一致；Codex 四层仅"可执行"层因该账号配额受限为 false，如实记录 `provider_quota` 分类）；fake CLI 失败矩阵覆盖截断、缺终态、未知字段、配额、认证、超轮次、取消、超时与 resume 回退。

### M4-5 冻结输入交付链（S04 拥有；2026-10-14 合同反馈后冻结）

按 S03 `api/knowledge-artifact/v1/README.md` 的 Runner consumer 行：S04 负责按冻结引用**受权获取、摘要校验并交付 Worker**，不读生产者目录或分支最新值；组装（绑定生成）由 run admission（S05/S06）拥有。

- **路由**（runner 传输，随 M1 凭证与任务归属校验）：
  - `GET /api/v1/runtime/tasks/{taskId}/input`：返回传输 envelope `{bundle: <base64>, bundleDigest, artifacts: [<冻结 v1 ArtifactRef>…]}`。服务端在应答前必须自行核验 `sha256(bundle)==TaskSpec.inputDigest`，不符则拒绝（不把未核验内容送达 Runner）；任务无已绑定输入（run admission 尚未持久化绑定）时返回 RESOURCE_UNAVAILABLE 并注明原因。响应上限独立于普通传输体（32 MiB）。
  - `GET /api/v1/runtime/tasks/{taskId}/input/artifacts/{artifactId}`：按冻结 ArtifactRef 返回原始字节；仅该 Task 归属 Runtime 可读，ref 必须出现在该 Task 的 manifest 中，否则 403/404。
- **Runner 消费规则**：收到后再次核验 bundle 摘要 == `TaskSpec.inputDigest`、每个 artifact 的字节摘要与 size == 冻结 ref；全部通过后写入任务目录（`input/bundle.json` 为**收到字节的原样**、`input/artifacts/<artifactId>`、`input/manifest.json` 记录已核验引用）；任一校验失败 → 不执行、上报 failed(`input_invalid`, retryable=false) 并保留本地证据；不可用/传输失败 → 在尝试死线内有限退避，仍失败则 failed(`input_unavailable`, retryable=true)。
- **Worker 交付**：host 把 `BundlePath` 传给 adapter（prompt 已引用该路径）；adapter 只能读任务目录中的已核验内容。
- **S05/S06 seam 预检结论**：run admission 必须在派发时持久化"node run → 输入引用"（冻结 EffectiveInput id 与其 canonical 内容 + 上游 ArtifactRef 列表），使 S04 路由可解析；本阶段生产组合根在 seam 未实现前 fail closed（RESOURCE_UNAVAILABLE + 明确日志），测试以 S03 真实生产者（`knowledge.Assemble` 的 canonical 字节、`artifact.Service` 发布内容）经真实路由/客户端/校验到 Worker 交付，不以 probe/fake 代替链条。
- **消费正反证据**：正确摘要送达（bundle 与 artifact 均可在任务目录核验）；篡改 bundle/artifact 摘要 → 拒绝执行且上报 input_invalid；未绑定输入 → input_unavailable；越权 artifact（不属于本 task/本 runtime）→ 403/404 且无内容泄漏。

### M3 工作区物化与受控 Git 通道（2026-10-14 冻结）

- **仓库登记**：Runner 部署配置 `KOWA_RUNNER_REPOSITORIES=<repositoryId>=<url>[,…]`（十进制 id 与远端 URL）；TaskSpec 的 `gitScopes` 只携带 Kowa 冻结的仓库身份，URL 由本机登记解析；未登记 repositoryId 一律拒绝（fail closed），不猜测远端。
- **ref 语法（S02 冻结形态，唯一语法）**：`GitScope.Ref` 为 `refs/heads/<branch>`（S02 examples 与 executiontest 实际 producer 形态）或 40 位 commit OID；通道内部派生 branch 用于 checkout/保护名单比较，push/ls-remote 使用完整 ref；裸分支名或其它形态一律 `git_scope_refused`（fail closed，不引入第二语法）；保护名单按派生 branch 比较（配置可写 `main` 或完整 ref，等价）；**operation 超出冻结枚举同样拒绝**（不静默忽略）。
- **物化**（role∈{project,knowledge}，operation∈{clone,fetch,pull}）：物化目录 `<attempt workspace>/repos/<repositoryId>/`；已存在则 `git fetch origin`，随后按 scope.Ref 检出——40 位 OID 用 `--detach` 且 HEAD 必须精确等于该 OID（对象缺失时先按 OID fetch）；分支名用 `origin/<ref>`；检出 OID 与命令/退出码写入物化证据。目录删除后按同一冻结引用重建必须得到同一 OID（正反测试）。知识仓只物化、永不写入。
- **受控交付**（仅 role=project，operation∈{commit,push}）：执行前核对——任务归属与租约（既有）、`task.gitExecutionUserId` 与本机实际 GitHub 数字身份一致（`gh api user --jq .id`，漂移即拒绝）、scope.Ref 不在保护名单（`KOWA_RUNNER_PROTECTED_REFS`，默认 `main,master`）、仓库目录已按冻结引用物化且 repositoryId 匹配；`git add -A` + commit（**不覆盖**本机 git 身份，记录 user.name/user.email/author ident）；`git push <url> HEAD:refs/heads/<ref>`（显式 refspec，不用 origin 别名）。无变更 → `no_changes` 失败（S06 可复议）。
- **未知 push 结果对账**：push 报错/超时绝不盲重试；立即 `git ls-remote <url> refs/heads/<ref>` 对账——远端 OID == 本地 HEAD → 记为成功（`reconciled=true`）；远端 OID 不同 → `git_push_conflict`；无法对账 → `git_push_unknown`（retryable=false，交控制面处置）。推送前基线 OID 与推送后远端 OID 一并入证据。
- **拒绝路径**：role=knowledge 的 commit/push/publish_pr 在通道直接拒绝（S02 创建层已拒绝此类 scope，此处为通道纵深）；`publish_pr` 在 S04 不实现（属 S05/S06 门禁），出现即拒绝并如实上报，不静默忽略；仓库身份/执行身份/ref 任一不符即拒绝并留证。
- **证据**：`delivery.json` / `materialization.json`（每 attempt 一份）含 repositoryId、role、operation、ref、baseOid、localHead、remoteOid、命令与退出码、身份数字 ID、reconciled；结果 `output.delivery` 仅含 `{repositoryId, branch, commitOid, baseOid, reconciled}`。
- **错误分类新增**：`git_materialize_failed`、`git_scope_refused`、`git_identity_mismatch`、`git_push_failed`、`git_push_conflict`、`git_push_unknown`、`no_changes`；只有明确可重试类 retryable=true。
- **真实证据（M3 退出）**：沙箱双仓（业务仓 + 知识仓）真实物化 OID、受控分支推送、`git ls-remote` 对账、远端身份核对、知识仓写拒绝；命令/退出码/OID 前后水位入 record；PR/Review/合并门禁归 S05，不在本阶段触发真实 WorkflowRun。

### M4 Runtime 观察、runtime-web v1 与执行配置（2026-10-14 冻结）

- **Runtime 观察上报（runner 传输）**：`POST /api/v1/runtime/observations`，body `{runtimeId, runtimeEpoch, observedAt, executable, cliVersion, versionCompatible, authenticated, canExecute, detail}`（四层分别呈现，均为机器事实）；凭证 + 当前 epoch 校验（非当前 → 409）；服务端以自己的接收时间记水位 `receivedAt`；`(runtimeId, runtimeEpoch, observedAt)` 幂等。runner 注册成功后上报一次；`KOWA_RUNNER_PROBE=execute` 时以第 4 层结果上报；上报失败只影响新鲜度，不影响任务执行。
- **runtime-web v1（S04 唯一拥有；新契约目录 `api/runtime-web/v1/`）**：
  - `GET /api/v1/workspaces/{workspaceID}/runtimes` 列出该 Workspace 已授权（管理员执行配置启用）的 Runtime 投影；`GET .../runtimes/{runtimeID}` 单个详情。
  - 逐请求授权：会话（S01）→ 当前 Workspace 成员（撤权后立即拒绝）→ Runtime 归属（仅管理员启用的 Workspace 可见）；Runtime 自报不能授予 Workspace；查询不触发执行。
  - 层次：分别呈现 `executable / versionCompatible / authenticated / canExecute` 与 `cliVersion`，不合并成单一"健康"；新鲜度 `fresh`（属当前 epoch 且 receivedAt 在 TTL 内）/`stale`（过期或旧 epoch，附原因）/`unknown`（从未观察）；stale/unknown 绝不呈现为可用。
  - 契约产物：`schema.json` + 正反 examples + `README.md` + `delivery.json`（operations 与层次登记、schema sha256 摘要）。
- **管理员执行配置**：`GET/PUT /api/v1/workspaces/{workspaceID}/execution-config`；body `{requestKey, expectedVersion, capabilities:[{id,version}], runtimes:[runtimeId]}`；仅管理员可写；`(caller, requestKey)` 幂等、同 key 异内容冲突、`expectedVersion` 不符冲突、版本单调递增；S06 按具体冻结版本消费。
  - **workspaceId 语法归 S01**：服务端不重施 runtime ID 语法（S01 为 base64url 字母表，可含前导 `-`/`_`）；本地仅拒绝空/超长，授权由逐请求成员查询决定；runtime-web schema 的 `$defs.WorkspaceID` 与之一致。
  - **零配置形状**：未写入时 `version=0` 且审计字段（updatedByGitHubUserId/updatedAt）**省略**，不得输出空十进制 id 或零时间戳（冻结 schema 会拒绝）；已写入时给出完整审计字段。
- **Server 只读 GitHub 观测 seam**：`GitHubObservation` 端口 + 同机 `gh` 只读适配器（先核验活动账号数字 ID；查询失败/账号漂移保持 `unknown`，绝不假绿）；机器 gh 与 Web actor 显式分离；不实现 PR/Review/关闭门禁（S05）。组合根中已装配（`KOWA_GITHUB_MACHINE_USER_ID` 配置后提供非 nil Observer，未配置为 nil），首个消费者随 S05 到达。
- **正反证据（M4 退出）**：探测→上报→投影全链（真实 PG 组合根 + 本机 runner 进程/scripted provider）；跨 Workspace 拒绝；撤权后拒绝；旧 epoch/超 TTL → stale、未观察 → unknown；重复上报幂等与执行配置同 key 异内容冲突；秘密过滤（响应/错误/日志扫描）；真实只读 GitHub 查询作为附加证据。
- **持久化与迁移 000004（2026-10-14 核清）**：M4 的观察水位与执行配置需要 PostgreSQL 真源（common §3.2）；迁移 000004 仅新增 `runtime_observation` 与 `workspace_execution_config` 两表（up 33 行 / down 2 行），不改 000001—000003。Coordinator 已核验并裁定允许保留为本阶段交付物（不改旧迁移/上游冻结 schema、不迁移真实数据库；独立审阅核验约束/幂等/跨租户/重启）。原 Task 约束中"数据库 schema/迁移"的"既有 vs 新增"歧义已登记；后续新 SQL 仅限这两表的必要修复并走隔离门禁。
- **范围**：不实现 S05 Review/关闭门禁；不触发真实 WorkflowRun；前端 runtime-web 消费者保持 NOT_STARTED，仅发布冻结契约与其摘要。
