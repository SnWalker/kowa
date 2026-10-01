# MVP 跨端最小语义契约

状态：active（设计语义基线；Identity/Workspace 与 Workflow/Execution 合同已冻结，其余精确 wire 仍由各 owner 阶段冻结）。最近核验：2026-09-29。上位产品与身份边界见 [common](../../common.md)、[decision](../../decision.md) 和 [MVP 流程](../design/mvp-delivery.md)；具体候选字段见[最小契约草案](../../research/mvp-contracts-draft.md)。本文拥有跨端消息的责任、版本与拒绝语义，不拥有后端阶段状态或前端展示布局。

## 唯一 owner 与消费者

所有合同由后端控制面定义业务语义及版本，Runner、Web 与外部适配器分别生产或消费事实，不能自行解释门禁。下表的 owner 是模块责任；真正的 Sxx owner 与版本号在首批阶段建立时指定。

| 合同族 | 唯一 owner | Producer | Consumer | 最小失败语义 |
| :--- | :--- | :--- | :--- | :--- |
| WebIdentity/WorkspaceConfig/RepositoryBinding | Identity/Workspace；owner `backend:S01`，版本 `kowa.identity-workspace.v1` | GitHub App 登录与 Workspace 管理用例 | Web、Workflow 准入、Runner 任务准备 | 未认证、未授权、仓库不可见、版本冲突分别拒绝 |
| WorkflowDefinition/CompiledPlan/RunView/NodeView | Workflow | 定义编译器与运行投影 | Web、Execution | 无效定义不发布；运行继续使用冻结版本 |
| TaskSpec/TaskResult/RuntimeRegistration | Execution | Server 派发、Runner 报告 | Runner/Worker、Workflow、Web | 任务身份、派发世代、租约、输入或能力不匹配时不得推进 |
| KnowledgeSnapshot/EffectiveInput | Knowledge/Workflow 输入组装；owner backend:S03，版本 kowa.knowledge-artifact.v1 | 知识确认用例与控制面 | 方案、Coder、QA、评审、Web | 必需引用缺失、内容不符或未确认不派发 |
| ArtifactRef | Artifact；owner backend:S03，版本 kowa.knowledge-artifact.v1 | 有效 Task 或受控系统操作 | 下游、Web、评测 | 未发布、摘要错误或跨 Workspace 越权不可消费 |
| GitOperationEvidence/ChangePublication/PullRequestCreation/GitHubDeliveryObservation | GitHub Delivery | Runner/Coder/PR 能力上报；Server 对 GitHub 读事实 | Workflow 门禁、Web、审计 | 远端版本/机器身份不符拒收；外部结果未知先对账 |
| HumanTaskRequest/Response | HumanTask | Server 创建、被授权人答复或 GitHub 事实收束 | Web、Workflow | 处理人、请求/对象版本或答复非法则拒绝推进 |
| VerificationEvidence/ClosureEvidence | Evaluation/Workflow 关闭用例 | 真实验证者、外部 CI 与关闭用例 | 门禁、历史、Web | 缺证据、模拟证据或版本失配不满足关闭 |

一个合同只有一个模块 owner；跨端契约阶段由后端 lane 冻结首个行为版本，前端阶段显式依赖该版本。即便多个后端阶段分别实施不同合同族，也不能出现两个阶段同时拥有同一合同。Browser 不调用 Runner 报告接口，Runner 凭证不能代替 Web 会话或业务授权。

`kowa.identity-workspace.v1` 的机器 schema、HTTP seam、正反样例、失败类别和前端消费说明位于 [`api/identity-workspace/v1/`](../../../api/identity-workspace/v1/README.md)。该版本只以服务端会话建立 Web actor，不包含 Runtime 或机器 GitHub 身份；知道 Workspace ID、GitHub 登录成功或 App installation 可见都不代表拥有 Workspace 权限。

`kowa.workflow-execution.v1` 的机器 schema、正反样例、Provider/机器 Git 身份、租约与条件接受说明位于 [`api/workflow-execution/v1/`](../../../api/workflow-execution/v1/README.md)，共享行为摘要见 [Workflow/Execution v1 契约](workflow-execution-v1.md)。

## 版本与身份不变量

- Workspace 绑定项目仓与配套知识仓的稳定 GitHub repository ID；首期一项 WorkItem 只写一个项目仓。知识绑定确认后冻结仓库提交与所选条目的内容摘要；重新绑定形成新有效输入，不改写既有 Task。
- Workflow 定义名称、版本和内容摘要共同固定；编译产物、输入、能力版本、政策版本和上游 ArtifactRef 进入 WorkflowRun/Task 的有效输入。相同定义身份的内容不可变，已有运行不跟随新定义。
- WorkItem、WorkflowRun、NodeRun、Task、HumanTask、Artifact/Evidence 维持独立身份。真实重新执行产生新 Task，重复投递同一次尝试复用原 Task；已接受报告同身份同内容幂等、同身份不同内容冲突。Retry 保持有效输入，Rerun 重建受影响节点与下游版本。
- Web 发起人的 GitHub ID 来自服务端会话；机器 GitHub 执行账号在首次可远端写的 Task 派发前冻结到 WorkflowRun。Coder 和 `publish_pr` 只派给报告同一账号的 Runner；账号失效或切换时停止新写入并对账。Web 发起人、机器账号和 Agent 来源分别存证与展示。
- Coder 以机器个人 `gh`/Git 身份在获准项目工作分支 commit/push；`verify_change` 独立核对 GitHub 仓库、ref、远端 OID。QA/评审按 OID 物化，不消费 Coder 本地目录。必需测试和独立评审有效后，独立 Runner `publish_pr` 节点创建 PR。
- PR 作者是机器账号；GitHub Review 由有权真人且非 PR 作者针对当前 head 提交。人到 GitHub 合并，Server 只读核对 PR、Review、CI 和实际合并结果。外部操作 HumanTask 是提醒，由外部事实收束，不提供第二次 Kowa 批准按钮。
- 实际 GitHub 写入可能超出 TaskSpec 的逻辑许可。Server 拒收越界报告不能撤销外部副作用；未知的 push/PR/merge 先按仓库、ref、OID、PR 与操作记录对账。正常流程只在 `publish_pr` 建 PR，但不承诺 GitHub 层硬阻止 Coder 提前建 PR，也不设专项清理。

## 命令、查询与错误类别

Web 命令包括 Workspace 配置、WorkItem 创建/启动、HumanTask 答复、Retry/Rerun/取消及受限的 GitHub 事实刷新；查询包括 Workspace、Run/Node/Task、人工任务、产物、PR/Review/合并水位和历史。Runner 只通过独立 Runtime 身份领取、心跳、报告和提交产物。所有可变业务对象的命令携带期望版本和幂等键，actor 从会话或 Runtime 认证得出，不信任请求体。

业务错误至少区分 `VALIDATION_ERROR`、`UNAUTHORIZED`、`FORBIDDEN`、`VERSION_CONFLICT`、`RESOURCE_UNAVAILABLE`、`EXTERNAL_UNKNOWN` 和 `POLICY_BLOCKED`。外部事实未核实时保持未知，不把过期观察转为通过或失败。传输状态码不能单独决定 Retry/Rerun。精确 HTTP 路由、字段类型、分页、DDL、Provider 能力 schema、轮询时限和存储保留参数由对应后端 owner 阶段冻结，前端消费者审阅正反样例。

## 首批阶段的验收输入

合同 owner 阶段须为每族提交机器 schema/版本、正反样例、Producer/Consumer 核对、权限与并发拒绝、失败恢复及验收矩阵映射。尤其要覆盖：Web 登录与机器写入身份分离；知识确认版本变化；Task 旧租约/重复报告；远端 OID 改变后测试/评审/Review 失效；PR 结果未知对账；GitHub 已合并但有效 Review 缺失；合并后验证失败的修复运行。设计期候选 JSON 通过语法检查不等于这些行为通过。

`kowa.knowledge-artifact.v1` 精确机器合同和消费者核对见 [`api/knowledge-artifact/v1/`](../../../api/knowledge-artifact/v1/README.md)，交接、批准与引用保护见 [Knowledge/Artifact v1 契约](knowledge-artifact-v1.md)。

## 实施交付责任补充（2026-10-01）

当前冻结核心合同不等于 Web 入口已交付。S01 拥有会话与 Workspace，S02 保持执行核心，S03 保持知识/Artifact；后续待冻结交付由 S04 拥有 Runtime/执行配置 transport，S05 拥有 WorkItem 基础与 HumanTask/Delivery，S06 拥有固定流程准入、跨模块恢复与组合 Web 查询。组合层只消费原 owner 的事实，不重新解释权限、状态、内容或门禁。精确新增操作、版本与摘要在 owner 退出前冻结；各消费者 stage 的需求块不是正式 API。

owner 发布 delivery.json 只登记 contract、owner、schema 相对路径/schemaSha256 和 operations（操作名→实际交付层次）。消费者 pin 该文件的 SHA-256 与正反证据；字段与业务语义仍以对应 schema/README 为准。核心应用端口和 http-seam 不能登记成已装配 http，登记文件本身也不证明运行验收通过。
