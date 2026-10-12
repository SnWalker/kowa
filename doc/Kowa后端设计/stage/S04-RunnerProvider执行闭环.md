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
