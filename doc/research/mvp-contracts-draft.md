# MVP 最小契约草案

状态：字段与 wire 候选；正式跨端行为语义见[最小语义契约](../wiki/contracts/mvp-cross-end-semantics.md)。统一机器个人 `gh` 的 MVP 身份路线已选。更新：2026-09-27。

本文件承载尚未批准的候选字段，避免提前进入 wiki/contracts。产品条件见 [MVP 流程](../wiki/design/mvp-delivery.md)，身份与状态见 [领域设计](../wiki/domain/delivery-lifecycle.md)。下表字段为语义名称，不代表数据库列、HTTP 路由或已冻结 JSON Schema。

## 1. 契约清单与责任

| 合同 | Producer | Consumer | 版本/失败要求 |
| :--- | :--- | :--- | :--- |
| KnowledgeSnapshot | 知识绑定用例，经人工确认 | 方案、编码、测试、评审和 Web | schema + 不可变快照身份；缺内容失败 |
| EffectiveInput | 控制面输入组装 | 指定执行尝试 | 固定上游版本及摘要；类型/引用错误不派发 |
| TaskSpec/TaskResult | 控制面/Worker | Worker/控制面 | 尝试身份遵守正式行为合同；wire、能力 schema 与内容版本仍待冻结 |
| HumanTaskRequest/Response | 控制面/授权用户 | Web/控制面 | 请求版本与对象版本均校验；过期答复不推进 |
| ArtifactRef | 产物服务接受发布 | 下游 Worker、Web、评测 | 内容摘要、类型、大小与归属一致 |
| VerificationEvidence | 实际验证者 | 门禁、关闭与 Web | 绑定被测版本及验证配置；缺证据不可冒充通过 |
| WorkflowDefinition/CompiledPlan | 定义作者/编译器 | 运行准入与编排器 | 定义身份、编译规则、内容摘要分开；无效定义不可发布 |
| CapabilityDescriptor | 经审阅的能力发布目录 | 编译器、调度和 Runner 注册校验 | 静态契约与在线可用性分开；自注册不等于业务契约获准 |
| WorkspaceConfig/RepositoryBinding | Workspace 管理用例 | 创建、知识绑定、执行准入和 Web | 配置版本、GitHub 仓库与用途明确；不携带密钥 |
| WorkItemIntake/RunView | 创建工作项用例/控制面运行投影 | 编排器/Web | 固定目标仓库、运行发起人和定义；展示状态不另持有业务真源 |
| GitHubDeliveryObservation | GitHub 只读对账用例 | PR 门禁、集成验证和 Web | PR 实际创建者、Review/head/合并结果各有观察水位；未知不能写成功 |
| GitOperationEvidence | Coder/Runner 的 Git 命令记录与控制面对账 | 编排器、审计与下游 | 明确机器 GitHub 身份、仓库、操作、ref、前后 OID 和外部结果；原生 gh 可绕过 TaskSpec，记录不等于硬权限控制 |
| ChangePublication | Coder Task 与远端事实核对用例 | 测试、独立评审、PR 创建与恢复 | 绑定项目仓、工作分支、机器账号与远端 OID，不把本地路径当正式代码交接 |
| RepositoryExecutionSnapshot | Runner 本机 git 物化与控制面版本核对 | Worker 与知识绑定 | 指定仓库 ID/提交、内容 digest 和用途；机器 gh 实际权限可能大于 Kowa 仓库角色 |
| PullRequestCreation | 独立 Runner `publish_pr` Capability | PR 门禁、Web 与审计 | 验证/评审后使用本机 gh 创建，实际作者为机器个人账号；Coder 提前 PR 不设专项硬防护 |
| ClosureEvidence | 关闭用例 | WorkItem、历史页和审计 | 保存本次实际采用的全部证据与政策版本 |

这些 owner 是领域责任，不是开发阶段编号。首个实施阶段建立时才指定冻结契约的阶段 owner，消费者通过显式依赖使用。

## 2. 尚未进入机器 schema 的业务消息

上一轮生成的 [候选 JSON Schema](schemas/README.md)覆盖执行、知识及人工任务等 18 类消息，不覆盖完整产品读写链路。本节补齐其业务字段，机器形状仍须由唯一 owner 冻结，不能把示例文件当成完整前后端协议。

| 消息 | 必需字段及类型 | 规则 |
| :--- | :--- | :--- |
| WorkspaceConfig | workspace_id:ID；version:正整数；成员授权集合；项目/知识 RepositoryBinding；启用定义引用 | App 安装不是 Workspace 管理权；变更使用预期版本 |
| RepositoryBinding | github_repository_id:十进制字符串；host:受支持平台；role:`project`/`knowledge`；installation_id:十进制字符串；读/写用途 | 首期只允许一个可写项目仓，知识仓只读；不信任用户填写的任意 clone URL |
| WorkItemIntake | workspace_id、项目仓身份、需求 ArtifactRef、验收标准版本、创建者身份、定义引用 | 创建者来自会话，不由请求体自报；第二可写仓拒绝 |
| WorkflowDefinitionRef | 名称、正整数版本、内容 digest | 名称+版本唯一；相同身份内容不变，旧运行不漂移 |
| RunView | work_item_id、workflow_run_id、首次可写 Task 派发前冻结的 `git_execution_user_id`、`started_by_github_user_id`、定义引用、状态、当前节点轮次摘要、更新时间 | Web 发起人与机器 GitHub 执行者分别展示；后续写任务只匹配同一机器 GitHub 身份，前端不推断准入和关闭 |
| NodeView | node_id、node_run_id、iteration、执行状态、业务 verdict、有效输出引用、阻塞原因 | Task 完成与业务通过分开；不暴露宿主绝对路径 |
| GitHubDeliveryObservation | 项目仓 ID、PR number、GitHub PR `user.id`、head OID、head commit author/committer、有效 Review ID/actor/commit、合并布尔值与实际合并 OID、观察时间 | PR 创建者与提交署名匹配 Runner 机器账号；Web 发起人另记。Server 查询 GitHub 真源；Reviewer 必须是有权真人且非 PR 作者 |
| ChangePublication | 项目仓 ID、冻结基线 OID、Coder Task、工作分支、结果 OID、机器 GitHub 账号、commit author/committer、GitOperationEvidence 引用 | Coder 在 Runner 本机 commit/push，`verify_change` 核对远端 OID；测试/评审只消费该远端版本，不共用 Coder 目录 |
| GitOperationEvidence | workflow_run_id、task_id、机器 GitHub 账号、repository_id/role、操作（clone/fetch/pull/commit/push/PR）、source/target ref、前后 OID、命令/退出码、观察时间及外部结果 | Runner 尽量记录本机原生 Git/gh 操作，Server 对关键远端结果复核；同账号原始凭证可绕过 Kowa 记录，不能以“未记录”证明未发生 |
| RepositoryExecutionSnapshot | Workspace/运行身份、项目或知识仓 ID、精确 commit OID、内容 digest、Runner 本地物化引用、生产时间与用途 | Runner 使用本机 gh/Git 物化；GitHub 固定提交是真源，本地缓存不跨任务自动提升为授权 |
| PullRequestCreation | WorkflowRun ID、项目仓 ID、工作分支与 head OID、目标分支、发布 Task、机器 GitHub 账号、操作 ID | 独立 `publish_pr` 节点在门禁后用本机 gh 创建；返回 PR number/实际 `user.id`，不匹配机器账号则阻断；Coder 提前建 PR 不设专项检测或清理 |
| ClosureEvidence | WorkItem/Run 身份、政策版本、知识与方案确认引用、代码评审/测试/安全证据、GitHub Review 与合并结果、集成验证引用、关闭时间 | 缺任一必需项不关闭；知识建议和离线评测不冒充同步门禁 |

消息与生产者映射还需覆盖两种合法来源：ArtifactRef 由有效 Task 或受控系统操作生产，必须恰有一个来源；VerificationEvidence 由实际 Task 或可核对的外部 CI check 生产，不能用模拟结果自称真实。HumanTask 的类型和指派目标必须显式来自控制面；浏览器提交的 HumanResponse 不自报 actor，由会话认证补全。这些结构已体现在 [候选机器 schema](schemas/mvp-v1.schema.json)，依赖身份及对象状态的跨字段校验仍由领域合同承担。

这里的 ID 与 OID 是语义类型。Workspace 和前端所需消息只有在字段、访问范围、分页/错误、消费者行为全部核对后，才移入正式 `wiki/contracts`。

## 3. 知识快照

候选内容分为两组，不把确认事件混入不可变内容：

- 内容快照：snapshot_id、schema_version、workspace_id、知识仓库身份和 commit、条目相对路径/内容摘要、适用范围和必读标记。
- 绑定确认事件：绑定对象、snapshot_id、human_response_id、此前绑定引用、操作者/时间与变更说明。它引用内容，不被反向写入内容摘要。

内容摘要只覆盖约定的规范化内容与格式版本，不包含自身 ID/digest、确认时间或可变的人读说明，避免循环引用和重复确认造成假版本变化。相同内容可复用快照，但每次有效确认保留独立事件，不能因去重丢失审计。快照的读取权限与是否被当前任务批准使用分别判断。

规则：

1. 展示和执行引用同一快照。本地索引、Markdown 说明和执行目录只是投影。
2. 人读推荐理由不独自导致内容版本变化；绑定身份包含来源版本与有效选择，不能只按文件名去重。
3. 无相关知识是成功读取后的明确结果；仓库不可达、条目缺失、摘要不符是失败。
4. 知识提交变化不自动影响既有尝试；重绑显式产生快照并重新计算下游影响。
5. 目录路径必须归一化且限制在指定仓库内；符号链接不能逃出允许范围。

## 4. 有效输入

候选内容：input_id、schema_version、需求版本、项目基线、knowledge_snapshot_id、上游 node_run_id/产物引用、human_response_id、执行配置与策略版本、内容摘要。

组装顺序由声明式绑定决定，不依赖无序 map 的覆盖顺序。服务端固定身份、资源与策略字段不可被人工输入或 Provider 输出覆盖。重复字段冲突明确报错。缺失必需上游、类型不匹配、失效引用均不默认为空。

每次尝试消费不可变输入。Retry 相同输入；续接在既有冻结上下文上增加已验证的人答复，不重新读取最新知识或最新代码。

## 5. Task 与报告

TaskSpec 的候选信封：task_id、node_run_id、subject、capability_id/version、获准 Provider 选择规则及版本、effective_input_ref、Git 操作预期/策略引用、Runner 机器 GitHub 账号身份、lease/fencing 上下文、取消/超时约束、可选 resume_ref、前一次尝试关联。Provider 选择规则不能包含某台 Mac 的 CLI 绝对路径或个人主目录；Runner 在派发/报告时另记录实际 CLI、版本、认证可用性和环境身份。指定 Provider 不可用不能静默换成另一个账号。Git 操作范围须按仓库/角色/ref/动作登记并核对，但 Agent 直接持宿主 `gh` 时，TaskSpec 不是 GitHub 层硬权限边界。

TaskResult 分开承载：执行结果、结构化业务 output、人读 summary、output_refs、可选 input_request/session_ref、结构化 error。

- summary 只解释，不驱动机器分支；禁止解析自由文本猜测通过。
- progress 是尽力观测，携带 task/派发世代/单调序号，不作为完成证据。
- 结果接受先校验任务归属、有效租约、当前轮次与 schema，再发布输出。
- 同请求同内容重复返回既有结果；同身份不同内容冲突，不采用最后写入覆盖。
- 已发生的外部副作用即使随旧报告晚到也要审计/对账，但不得推进当前轮次。
- 尝试身份以已批准的 [执行尝试合同](../wiki/contracts/execution-attempts.md) 为准，不在本文维护另一套解释。出站长轮询基线见 [执行边界](../wiki/architecture/workflow-execution.md)，路由与字段仍待冻结。

## 6. 人工任务与答复

请求内容：human_task_id、所属运行/节点、任务类型、完成方式（Kowa 内答复或外部事实）、审阅/操作对象及版本、处理权限、字段约束或外部操作目标、允许动作、并发版本。

答复内容：response_id、human_task_id、expected_version、decision、结构化输入、理由、幂等键。操作者和提交时间由服务端认证与时钟补充，不信任浏览器自报身份。

接受条件：当前请求未失效、用户有权限、审阅对象仍适用、字段类型/必填/选项/只读约束合法。UI 不认识必需字段时明确阻断，不能隐藏字段后提交。答复接受、请求状态和后续推进意图在同一数据库事务中提交。

已确认的外部 GitHub Review/合并待办只提供到 GitHub 的目标与当前 PR/head，不提供 Kowa 代批准按钮。它由 GitHub 对账用例在核实外部事实后以证据引用关闭；无需也不得伪造 `HumanResponse`。同一 PR 提交版本的重复观察幂等，head 变化使旧待办失效并重新判断 Review。待办解决和业务门禁通过是两项判断：GitHub 已合并但事前有效 Review 缺失时必须记录合并事实并阻断自动关闭。候选机器 schema 用 `completion_mode` 区分这两种 HumanTask，但跨对象权限、版本和证据校验仍需正式合同。

幂等键的查找作用域由调用方、命令类型、目标和请求键确定，内容摘要是用于比对的值，不放进唯一查找键。同键同内容返回原结果，同键不同内容冲突；否则把摘要放进键会让冲突请求成为另一条合法记录。

历史 demo 以 branch 字段存在判断已经答复；Kowa 不能继承该示例判断。预填 branch 或 Provider session 存在均不等于人工批准。

## 7. 产物与证据

ArtifactRef 候选内容：artifact_id、workspace/subject、kind、schema_version、明确的 Task 或受控操作生产者、digest、size、media_type、可解析的存储引用。短期签名 URL 不是持久身份，producer 本地绝对路径不是获取地址。

先持久上传与内容校验，再由控制面接受有效报告并发布引用。上传成功但报告未接受的内容属于未发布/孤立内容，不能被下游自动消费。发布后内容不可原地覆写。

合法的 awaiting_input 报告也可以发布供人工审阅的候选快照/方案；“内容已发布可读取”不等于“已批准供下游执行”。该区分避免先要求人工确认、却不给人查看候选内容的死锁。

原始内容的摘要针对约定字节计算；结构化快照保存摘要对应的规范化内容及其格式版本，不能对 JSONB 回读后的随意序列化重新计算身份。代码引用必须远端可达并有保留机制，仅保存 SHA 字符串不保证未来可获取。

VerificationEvidence 候选内容：被测提交/产物、验证配置版本、执行者、开始结束时间、结果与明细、完整报告引用、来源分类（真实/模拟）。模拟证据不得满足真实交付门禁。

Worker 的 `retryable` 提示不是调度许可；自动 Retry 仍以控制面的冻结策略、剩余额度和外部副作用对账为准。无法核实的 CI 状态属于未知，而不是通过或未通过。

## 8. 证据生命周期

- 核心交付证据、诊断日志、孤立内容分开管理。
- 有效运行或关闭清单仍引用的内容不得按临时数据清理；引用建立、发布与清理需并发保护。
- 孤立内容清理必须经过宽限和引用复查，宽限参数由运行设计给出。
- 开发验收期间保留核心证据；面向实际用户运行前确定保留期限、容量预算、备份与删除规则，本文不编造数字。
- 敏感信息在采集/入库/导出边界处理，不能只在页面隐藏。

## 9. 冻结前检查

字段类型、必填、schema 演进、大小限制、幂等作用域、错误码与 HTTP/wire 信封尚未冻结。冻结时必须用知识绑定、独立评审、任务续接、产物交接四个消费者样例核对，并与 [验收矩阵](../Kowa验收与运行/MVP验收矩阵.md) 建立对应。

Workflow/Capability 的加载、编译、目录和新增流程由 [扩展设计](workflow-capability-design.md)统一描述，本文件不维护另一套语义。InputKeys/OutputKeys 只能作为导航，冻结合同需要字段类型、必需性和条件分支可用性。

首期只有一条固定流程与一个 Runner，控制面和 Runner 可协调升级。候选 schema 对未知字段采取严格拒绝；新增字段也要有明确版本/兼容性审查，不将“字段可选”当作旧消费者必然兼容。所有错误必须能区分输入不合法、权限不足、对象过期、执行暂不可用与外部事实未核对。

## 10. 候选命令与查询面

本节仅固定要评审的业务操作集合，不代表路由、HTTP 方法或浏览器可以直接调用 Runner 接口。每个写命令均由服务端会话或登记的 Runner 身份建立操作者，不接收请求体自报的 actor；对业务对象携带期望版本与请求幂等键。

| 操作族 | 输入与权威处理者 | 可观察结果与拒绝条件 |
| :--- | :--- | :--- |
| Workspace 配置 | 管理者提交成员、双仓绑定、执行策略的预期版本；Workspace 用例核实 App 授权范围 | 返回新的配置版本；仓库未授权、第二可写仓、权限不足或版本冲突明确拒绝 |
| Web GitHub 登录与机器 Git 身份 | 登录成员通过 GitHub App OAuth 建立 Kowa 身份；Runner 登记本机 `gh` 活动账号与 Git remote 可用性 | Web 发起人与机器 GitHub 执行者分开返回；commit/PR 署名归机器账号，不把 Web 登录令牌当作该任务的 Git 写入凭证 |
| WorkItem 创建与启动 | 成员提交需求/验收范围；控制面固定创建者、定义与仓库基线，启动时冻结运行输入 | 返回 WorkItem/Run 身份及版本；目标缺失、知识仓不可读或定义无效不能派发 |
| Agent 远端 Git 与独立 PR 发布 | Coder Task 在 Runner 本机执行原生 git/gh，记录项目仓、工作分支与前后 OID；验证/评审后独立 `publish_pr` Task 使用同一本机 gh | 远端操作归 Runner 个人账号；Server 按冻结目标和 GitHub 实际事实判断结果，不能把 TaskSpec 说成硬阻止越界命令；提前 PR 不设专项处理 |
| 运行查询 | 授权成员按 Workspace/WorkItem/Run 获取投影、节点轮次、人工任务、证据与对账水位 | 仅显示服务端已接受事实；分页和访问范围由服务端强制，未知外部状态有独立表示 |
| HumanTask 答复 | 被授权处理人提交请求版本、审阅对象版本、结构化答复与幂等键 | 返回已接受答复与新的对象版本；过期、越权、字段非法或同键异内容拒绝 |
| Retry、Rerun、取消与修复 | 有权成员提交目标运行/节点、期望版本、理由及适用策略 | 返回命令接受结果和新轮次/运行引用；不能用命令覆盖未决外部副作用或已关闭工作项 |
| GitHub 手动刷新 | 有权成员请求对指定 PR/提交执行受限只读对账；控制面以可用读取身份查询平台 | 返回观察任务或新水位；请求本身不能声明 Review、检查或合并已通过；Server 分机后的读凭证尚需冻结 |
| Runner 领取、心跳、报告 | 已登记 Runner 用独立 Kowa Runtime 凭证出站领取；控制面派发冻结 TaskSpec 并验证 Runtime/派发世代、租约和结果 | 凭证只准访问已分配 Task 的输入、产物与报告面，不授予成员 GitHub 权限或业务裁决；重复请求返回同一有效分配或既有接受结果 |
| Artifact 发布与读取 | 授权 Task/受控操作写入临时内容；产物用例核验摘要、归属及报告接受后发布 | 返回持久 ArtifactRef；未发布内容不进入下游，只读消费者不得读取其他 Workspace 内容 |

浏览器查询与 Runner 报告属于不同认证面；受控 GitHub 发布也不是浏览器可以直接提供任意仓库 URL 的通用写接口。具体路由、分页游标、上传协议、租约时长和操作结果信封仍需由后续唯一 owner 与真实消费者核对。

## 11. 候选错误与演进规则

所有命令结果至少区分以下语义类别，并附可关联的请求/对象身份、当前版本或观察水位（仅在调用方有权知道时返回）：

| 类别 | 典型场景 | 客户端和编排器行为 |
| :--- | :--- | :--- |
| INVALID_INPUT | 字段缺失、类型不符、定义编译失败、无效仓库角色 | 修正输入；不自动重试同一内容 |
| UNAUTHENTICATED / FORBIDDEN | 会话失效、非成员、Runner 授权或资源越界 | 停止操作，重新认证或由有权者调整授权；不能偷偷换另一台 Runner/另一个 gh 账号制造成功 |
| VERSION_CONFLICT / STALE_RESULT | 人工请求、对象版本、Task 世代、PR head 与预期不符 | 重新读取权威状态；旧结果不能推进，不能最后写入覆盖 |
| RESOURCE_UNAVAILABLE | 登记的 Runner、Provider、Artifact Store 暂不可用 | 保留待办/运行上下文，按冻结策略退避或显式等待 |
| EXTERNAL_UNKNOWN | GitHub 发布、PR 或检查请求超时且结果未证实 | 先对账外部对象和操作意图，再决定重试；不报告为已成功或已失败 |
| POLICY_BLOCKED | 必需门禁未满足、授权已撤销、返修额度耗尽 | 返回明确阻断原因及允许的人工作用，不用空结果或默认值放行 |

传输错误码与上述业务类别是一对多映射，不能只用 HTTP 状态码推断 Retry/Rerun 权限。命令幂等查找键由调用主体、操作类型、目标和请求键构成；相同键但规范化内容不同必须冲突。受控外部写操作另有持久操作 ID 和对账水位，不能借浏览器请求键声称 GitHub 端 exactly-once。

契约升级必须同时检查服务端、Web、Runner、已冻结的 WorkflowRun 与能力目录消费者：同名同版本内容不可变；旧运行继续读其冻结版本；新增字段或枚举值若旧消费者不能理解，须发布新合同版本或明确拒绝，不能依靠忽略未知字段制造隐式兼容。正式冻结时要为每个消息补 schema、正反样例、生产者/消费者评审记录以及验收矩阵条目。

## 12. 首批阶段的合同归属建议

下表确定唯一的**端/模块 owner**，便于编写首批阶段依赖；具体后端 Sxx 编号只在阶段建立时分配，不能从候选材料反推 `currentStage`。

| 合同族 | 唯一 owner（后端） | 主要 producer | 消费者与冻结条件 |
| :--- | :--- | :--- | :--- |
| Web 登录/Workspace/RepositoryBinding | Identity/Workspace | Server OAuth 与配置用例 | Web、Workflow 准入；GitHub App 安装、Kowa 成员与 Runner 个人 gh 身份分开 |
| WorkflowDefinition/CompiledPlan/RunView/NodeView | Workflow | 定义编译器、控制面状态投影 | Web、Execution；固定 DAG、NodeRun 轮次和门禁版本一致 |
| TaskSpec/TaskResult/RuntimeRegistration | Execution | Server 派发、Runner 报告 | Runner/Worker、Workflow、Web 观测；Task、租约、派发世代和冻结输入一致 |
| GitOperationEvidence/ChangePublication/PullRequestCreation/GitHubDeliveryObservation | GitHub Delivery（控制面事实接收） | Runner/Coder/独立 PR 能力上报，Server 从 GitHub 核对 | Workflow 门禁、Web、审计；机器 gh 身份、远端 OID、PR/head/Review/merge 事实与来源明确，不能由 Agent 自报单独放行 |
| HumanTaskRequest/Response | HumanTask | Server 创建、授权人答复或 GitHub 外部事实 | Web、Workflow；请求版本、完成方式与审阅对象一致 |
| ArtifactRef | Artifact | 有效 Task 或受控操作 | Runner、Web、Workflow；不可变引用、内容摘要和访问范围明确 |
| KnowledgeSnapshot | Knowledge | 知识绑定用例与有效人确认 | 方案、编码、评审和 Web；内容版本与确认事件分开 |
| VerificationEvidence | Evaluation | 真实验证 Task 或外部 CI | Workflow 门禁、Web；被测 OID、配置与真实性明确 |

首个共享合同阶段应由后端 lane 负责冻结跨端行为版本和最小正反样例，前端阶段通过显式依赖消费；前端不得从展示需要重新定义 GitHub 作者、HumanTask 或关闭规则。后端可以把这些合同拆为数个不重叠的 owner 阶段，但每个合同最终只能有一个 owner。准确 wire 路由、字段和 DDL 可成为相应阶段的工程目标，不必伪装成设计期已经通过的接口。
