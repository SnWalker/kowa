# MVP 模块与数据设计

状态：设计草案，非工程实现或阶段合同。更新：2026-09-25。

## 与已实现代码的差异（as-built 提示）

本节于 2026-10-01 补充，仅陈述可由仓库核实的事实，不改变下文设计草案。

- 仓库已有 Go 工程与迁移 `db/migrations/000001`–`000003`（身份与 Workspace、Workflow/Execution、Knowledge/Artifact）。
- 第 2.1 节建议主键统一为 `uuid`；现有迁移未使用 `uuid` 类型，主键与外键 ID 均为文本类型，例如 `web_identity.github_user_id text primary key`、`workspace.workspace_id text primary key`、`workflow_run.workflow_run_id text primary key`、`artifact.artifact_id text primary key`；审计表另有 `id bigint generated always as identity primary key`。
- 第 2.1 节的表名与列名是候选名，不是冻结 DDL，与现有迁移的命名并不一一对应；实际 schema 以 `db/migrations` 为准，阶段范围与证据以对应 `stage/`、`record/` 为准。

技术基线引用 [common](../common.md#34-技术栈基线)，产品条件引用 [MVP 流程](../wiki/design/mvp-delivery.md)。本文件只拥有后端内部模块、存储和事务建议，不复制共享契约字段。

## 1. 模块责任

| 模块 | 拥有的责任 |
| :--- | :--- |
| Workspace | 仓库、知识源、资源与策略配置 |
| WorkItem | 目标、交付运行关联、关闭判断 |
| Workflow | 定义冻结、节点轮次、依赖与结果应用 |
| Execution | Task、Runtime、能力匹配、租约和执行报告 |
| HumanTask | 请求、答复、处理权限、并发版本 |
| Knowledge | 绑定、确认快照与差异 |
| Artifact | 内容发布、引用、获取与校验 |
| Evaluation | 评测事实、门禁输入与离线评测 |
| GitHub Adapter | 记录并核对 Runner 本机个人 gh 的远端 Git/PR 事实、CI/Review/合并结果；控制面仍拥有业务准入，原生 Agent 写入的硬限制与独立读凭证见[权限审计](../research/github-permission-audit.md) |

沿用 interfaces → app → domain ← infra 的职责方向；不按某个 Provider 或 Runner 进程新造业务领域。net/http 位于接口层，Fx 位于组合根与生命周期，不进入纯领域规则。三篇历史扩展材料已核对，具体边界见 [Workflow/Capability 扩展设计](../research/workflow-capability-design.md)。

建议 workflowdef 负责作者格式加载与发布，Workflow 负责定义/编译规则和运行语义，Execution 负责任务与租约，统一 Runner host 监管 Worker，Provider Adapter 隔离供应方差异。模块通过 app 用例协调；不因历史总览一句“跨 BC 只经事件”而禁止所有同步应用协作，也不宣称现成 outbox 已覆盖全部链路。具体 package 路径仍以正式工程合同为准。

## 2. 逻辑存储集合

| 数据集合（非冻结表名） | 核心约束 |
| :--- | :--- |
| workspace / repository binding | 仓库外部身份、用途、授权和配置版本明确 |
| work item / run | 当前推进权与工作项关联一致；关闭记录可追溯 |
| node run / task | 节点轮次唯一、输入不可变、重试关系明确 |
| human task / response | 请求版本、对象版本、处理人和幂等请求身份 |
| knowledge snapshot | 共享内容身份与确认事件分开 |
| artifact / reference | 不可变发布内容、所属范围、引用与删除保护 |
| review / verification | 被审/被测版本、配置与真实/模拟来源明确 |
| external operation | 请求意图、授权引用、外部结果与未知状态 |
| github observation | PR/Review/合并/CI 外部身份与最后成功观察水位；重复观察不能重复推进 |
| audit / event / durable work | 命令、结果、关联对象、后续动作可恢复 |
| closure evidence | 一次关闭实际采用的证据版本清单 |

身份、状态、外键、版本、租约、唯一键使用明确关系字段。JSONB 用于有 schema 的输入输出、冻结定义和交互描述，不用一个 payload 代替关键约束。大型内容进入 Artifact Store。

### 2.1 关系 schema 候选

下表是 PostgreSQL 关系形状的设计草案，列名是候选名，不是已执行 DDL。主键建议统一为 `uuid`；GitHub repository/installation/user ID 保存为不丢精度的十进制字符串，commit OID 保存完整算法与值，不把可改的仓库名当主键。所有业务写入记录 `created_at`、`updated_at` 及必要的 `version`，实际时钟由数据库/服务端产生。

| 集合 | 必需关系字段与约束 | 可使用 JSONB 的内容 |
| :--- | :--- | :--- |
| `workspace`、`workspace_member` | Workspace ID/版本；成员以 `(workspace_id, github_user_id)` 唯一，角色与撤销时间明确；首个管理员由部署允许名单建立 | 经 schema 约束的非安全策略配置 |
| `github_user_authorization`、`runtime_git_identity` | Web 成员稳定 GitHub ID 与 Kowa 登录授权；Runner 登记本机个人 `gh` 活动账号、Git remote 协议、commit 署名和核验水位；任务记录实际机器 GitHub 身份 | 不把 Web 发起人当作远端写入操作者；Server 事实读取凭证路线待定 |
| `repository_binding` | `(workspace_id, github_repository_id, role)` 有效绑定唯一；`role=project/knowledge`；installation ID、允许用途、版本；首期一个可写项目仓与一个只读知识仓由配置用例及数据库唯一约束双重限制 | 仅展示元数据，不保存 token |
| `work_item` | ID、workspace ID、目标项目仓绑定、需求 ArtifactRef、验收版本、状态、`current_run_id`、乐观版本；关闭/取消不可回到 OPEN | 需求结构化表单的冻结副本 |
| `workflow_run` | ID、work_item ID、认证的 `started_by_github_user_id`、首次可写 Task 派发前冻结的 `git_execution_user_id`、定义名/版本/digest、运行状态、来源修复运行、输入摘要、版本；运行归属与 `current_run_id` 一致性由事务核对 | 编译后定义、冻结策略与输入引用摘要 |
| `runtime_registration` | Runtime ID、绑定的 Runner/环境身份、获准 Workspace 与 Capability、Kowa 凭证引用/派发世代、撤销和最后心跳水位；另关联当前本机个人 gh 的稳定 GitHub ID，不把 Runtime token 与 GitHub 登录态混同 | 经版本约束的能力/环境摘要 |
| `node_run` | ID、run ID、定义 node ID、iteration、状态、verdict、有效输入摘要、结果 ArtifactRef、版本；`(run_id,node_id,iteration)` 唯一 | 类型化节点输入/输出摘要；大型正文不入库 |
| `task` | ID、node_run ID、尝试序号、前次 Task、能力 ID/版本、Runner/派发世代、租约终点、状态、输入摘要与报告摘要；`(node_run_id,attempt_no)` 唯一 | 已验证 TaskSpec/TaskResult 的冻结小型内容 |
| `human_task`、`human_response` | 请求 ID、run/node 归属、完成方式、类型、指派范围、对象版本、状态/版本；Kowa 内答复引用认证的 actor 与 decision，外部 GitHub 操作引用已核对的 observation 而不造 HumanResponse；单次有效解决的约束明确 | 字段定义、结构化答复或外部目标，均按版本 schema 校验 |
| `knowledge_snapshot`、`knowledge_binding_event` | 快照 ID、知识仓/commit、内容 digest 与 ArtifactRef；绑定事件引用 WorkItem/Run、快照和 HumanResponse；相同内容可复用，确认事件不可因去重消失 | 条目索引与来源说明 |
| `artifact`、`artifact_reference` | 内容身份、workspace/subject、生产者 Task 或受控操作二选一、digest、size、media type、发布状态；引用行记录消费者与用途；删除前检查有效引用 | 经约束的元数据，不存大内容或临时签名 URL |
| `verification_evidence` | 证据 ID、被测仓库/commit 或 Artifact、配置版本、执行者 Task/外部 CI 身份、真实/模拟来源、结果与报告引用 | 详细诊断摘要 |
| `external_operation`、`github_observation` | 操作 ID、种类、目标仓/分支/PR、期望版本、Web 发起人/Runner 机器 GitHub 账号、意图/未知/已证实结果及查询水位；观察记录有 PR 创建者稳定 ID、head、Review/merge/check 事实与观察时间 | 平台原始小型响应摘要；宿主 gh 凭证不入业务表 |
| `git_operation_evidence` | Task/Runner、实际个人 gh 身份、仓库、Git 动作、source/target ref、前后 OID、命令/退出码、报告与远端核对水位；记录缺失不能证明操作未发生 | 原生 Agent 命令可能绕过应用内策略，不能把审计行当 GitHub 层硬授权 |
| `audit_event`、`durable_work`、`closure_evidence` | 事件序号、actor、目标/版本、命令和结果；持久待办的唯一业务键与可认领状态；关闭清单关联唯一 WorkItem/Run 和实际采用的全部证据版本 | 经 schema 约束的事件细节与关闭清单 |

候选关键约束：

1. `work_item.current_run_id` 只可指向同一 WorkItem 的运行；切换推进权与旧运行收束由一个事务提交。数据库约束可采用复合外键，不能只靠前端隐藏按钮。
2. Task 接受结果同时比较 `task_id`、派发世代、租约、当前 NodeRun 轮次和输入摘要；重报相同结果返回既有接受事实，同身份异内容冲突。租约过期后的外部副作用继续由 `external_operation` 对账。
3. 命令幂等登记以 `(caller, command_type, target_id, request_key)` 唯一，规范化请求 digest 是比较列；受控 GitHub 写操作另有稳定操作 ID，未知结果未核对前不重复写。
4. Artifact 内容先持久化并校验，再由接受 Task/受控操作的事务将其引用发布给下游。上传后未发布的孤立内容与被有效运行/关闭清单引用的内容采用不同清理规则。
5. 关系字段强制 Workspace 归属和对象版本；JSONB 只承载已冻结 schema 的可变形状。真正的跨 Workspace 访问控制仍由每个读写用例检查，不能把有外键当作授权。
6. Web 发起人的 OAuth 身份与 Runner 的个人 GitHub 执行身份分别持久化；前者不因机器 gh 切换而被改写。MVP commit author/committer 与 PR 作者属于机器账号，须核对其 GitHub 关联邮箱；`git user.name` 文本不构成远端认证证明。宿主 gh 凭证由机器本身保管，其访问范围可能大于 Kowa 的 TaskSpec，不能将数据库策略解释为 GitHub 已硬拒绝。

状态枚举采用已确认的 [交付生命周期设计](../wiki/domain/delivery-lifecycle.md)；本表的列名和存储形状仍为候选。精确 DDL、索引、迁移顺序、数据保留期及隔离级别由正式后端阶段根据批准的共享契约冻结，不把候选列名当成不可变 API。

## 3. 事务边界

1. 接受人工答复、变更人工任务状态、写后续推进意图在一个 PG 事务中。
2. 接受 Task 结果、发布有效产物引用、更新节点当前事实与后续意图在一个事务中；文件上传已在事务前完成，孤立内容另清理。
3. 核验关闭清单、提交交付成功和 WorkItem 关闭必须一致，不能由两个 UI 调用拼接。
4. 修复接替前收束旧任务并核对外部操作；切换当前运行使用并发版本或锁保护。
5. 幂等唯一键绑定命令类型、目标、调用方与请求键；请求内容摘要作为比对值保存，而非加入唯一查找键。同键同内容返回既有结果，同键不同内容明确冲突。

不在数据库事务中等待模型、用户、GitHub 或对象存储。远程副作用采用持久操作意图、执行与查询对账，不承诺端到端 exactly-once。数据库内唯一性不能阻止已离线的 Runner 在外部系统继续写入，因此外部权限与对账仍必要。

工作分支 Git 操作由 Coder 在获准 Task 中使用 Runner 本机个人 `gh`/Git 执行；提交署名是该机器账号，Web 发起人和 Agent 来源另记。独立 `publish_pr` 节点在必需测试/评审后由 Runner 使用同一 gh 创建 PR；控制面负责派发准入、远端版本与门禁事实对账。个人 gh 若拥有超出 TaskSpec 的权限，Server 事后拒收不能撤销已发生的 push/PR；MVP 不为 Coder 提前创建 PR 设计专项检测或清理。网络结果未知时先查证再重试，不能凭模型摘要猜测完成。

Runner 使用本机 gh/Git 把项目/知识仓的指定提交物化到任务目录，保存仓库 ID、精确 OID 和内容摘要；它不使用 A/B Web 用户令牌。首轮 Server 的 GitHubObservationAdapter 可在同机用已配置的个人 gh 做受控只读 API 查询，先核实活动 GitHub 用户 ID，读失败/账号漂移时保持未知，不只信 Runner 报告。该适配器以仓库、PR、OID 和观察水位为稳定端口；将来 Server/Runner 分机需另配 Server 读取凭证，不改业务结论。GitHub 固定提交保持代码真源，本地缓存不因同一文件内容自动取得跨 Workspace 授权；知识仓“只读”是 Kowa 任务意图，若个人 gh 实际可写该仓还需平台规则或接受风险。

## 4. 并发与恢复建议

首期单控制面与单 Runner 不省略乐观版本、租约校验和重复报告处理。可采用 PG 持久待办与后台认领；具体 SQL、认领条件和 Runner wire 待冻结。出站长轮询和 HTTP 报告已批准，见 [执行边界](../wiki/architecture/workflow-execution.md)。重启先恢复未完成意图及待对账操作，不凭内存 map 决定是否已经执行。

MVP 的 Runner 在本机 Mac 发现并调用宿主已安装、已登录且可用的 Claude Code、Codex 等 Provider CLI。Runtime 注册应分别报告可执行文件/版本、认证可用性和实际支持的 Capability；控制面仍按 Workspace 授权与任务要求准入，不能把“Mac 上装了 CLI”当作所有成员和能力均可执行。运行证据记录实际 CLI/Provider 版本、宿主环境与任务发起人。具体探测命令和兼容版本由 Provider 适配验收冻结；不要求模型 CLI 为每个 Task 重新安装到容器。

本机首轮以主动读取 GitHub 的 PR、Review、合并和检查状态收敛观察结果，不要求外部 Webhook 投递到 loopback。未来有可达的接收入口时可补独立 webhook receipt，但观察结果的业务解释仍由同一 GitHub 对账用例负责。第二个同机 Runner 的专项验收不能从彼此共享的执行目录取输入；内容须经 GitHub 或 Artifact Store 的正式引用获取。

只有有效轮次可推进当前 DAG。旧任务结果可以增加历史审计，不能改变当前结果。审批与关闭竞态必须在同一权威处判断，不让前端计算“是否还能点击”。

## 5. 部署边界与非功能缺口

MVP 部署包含控制面、Web、独立 Runner、PG 和 Artifact Store；Langfuse 为评测子系统，其自身依赖单独登记。首轮 Server 与 Runner 运行在同一 Mac、共用一个 macOS 账号；进程分离只承担职责与故障边界，文件/凭证隔离仍须另行实测。开发环境本地 Artifact Store 也必须提供独立于 Runner 目录的获取通道。

| 设施 | 所属职责 | 当前约束 |
| :--- | :--- | :--- |
| PostgreSQL 17.11 | Kowa 状态、事务与持久待办 | 版本已确认；开发/CI 已用 `postgres:17.11-alpine` 容器做隔离集成测试，真实部署实例尚未初始化 |
| Artifact Store | Kowa 非代码产物与证据 | 开发可用服务管理的本地存储；生产 S3 兼容，具体产品待定 |
| Langfuse Web/Worker | 外部评测与观测后端 | 首期接入方向已确认，版本和资源规格未冻结 |
| PostgreSQL、ClickHouse、Redis/Valkey、S3/Blob Store | Langfuse 自托管依赖 | 不等于 Kowa 核心调度依赖；是否共享物理实例及隔离策略待部署评审 |
| Kafka、Temporal | 未选用 | 不以预留分布式为由提前部署 |

Langfuse 依赖依据为 [官方自托管架构](https://langfuse.com/self-hosting)，2026-09-25 核验。评测存储不能接管 Kowa 工作流真源，日志/轨迹异步送达不等于节点业务成功。

目前不编造并发量、延迟、成本、RPO/RTO 或保留期限。上线前需确定容量/资源预算、备份恢复、敏感信息与隔离策略。工程前必须验证本机 Provider CLI 的安装、登录、协议与最小执行能力，真实 CI 接入，以及模型工具和项目脚本各自的权限边界；现有版本声明不能作为兼容性测试结果。

### Provider 最小适配验收设计

| 能力 | 需验证的结果 |
| :--- | :--- |
| 结构化输出 | 合法结果稳定解析；截断、额外说明、缺必需字段明确失败 |
| 输入与上下文 | 使用指定代码/知识版本；独立评审不继承编码隐式会话 |
| 工具执行 | 作用域和凭证受限；模型指令不能扩大允许写范围 |
| 人工续接 | 已接受答复与新 Task 关联；session 丢失仍有明确恢复路径 |
| 取消/超时 | 区分请求取消与进程停止；子进程和外部未知结果有收束证据 |
| 产物 | 完整内容可读取/校验，不能用摘要或本地路径替代 |
| 错误分类 | 权限、配额、网络、协议和业务不通过分别处理，避免全量盲重试 |

以上为准备好的验证清单，不表示任一 Provider 已通过。真实执行需后续阶段提供隔离环境、命令、版本与人工授权边界。身份/隔离模型见 [专项草案](../research/mvp-identity-security-design.md)。

## 6. 实施前出口

共享合同批准并冻结；历史扩展机制完成映射；权限与发布边界明确；验收矩阵转成阶段适用命令。本段为 2026-09-25 的出口状态说明，现已由 S00–S03 阶段及其 record 取代；工程、测试与迁移现状以 `stage/`、`record/` 和 `db/migrations` 为准。
