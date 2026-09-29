# Workflow/Execution v1 契约

状态：active。版本：`kowa.workflow-execution.v1`。Owner：`backend:S02`。最近核验：2026-09-29。

本页拥有 Workflow/Execution 首个跨端行为版本的共享语义；精确机器字段、正反样例和 Runner/Web 消费说明见 [`api/workflow-execution/v1`](../../../api/workflow-execution/v1/README.md)。领域状态仍以[交付生命周期](../domain/delivery-lifecycle.md)为准，Task 身份仍以[执行尝试合同](execution-attempts.md)为准。

## Producer 与 Consumer

| 合同 | Producer | Consumer |
| :--- | :--- | :--- |
| WorkflowDefinition / CompiledPlan | 内置定义作者、Workflow 编译器 | 运行准入、Execution、Web 定义摘要 |
| RuntimeRegistration | 经 Kowa Runtime 身份认证的 Runner | Execution 派发与审计 |
| TaskSpec / TaskLease | Execution | Runner/Worker |
| ProgressEvent / TaskResult | Runner/Worker | Execution 条件接受、Workflow、Web 观测 |
| RunView / NodeView | 控制面投影 | Web、审计、HumanTask 与后续门禁消费者 |

浏览器不能生产 Runtime、Task 或报告；Runner 身份不能执行 Web 业务命令。Provider、Runtime、机器 GitHub 账号和 Web 发起人是不同身份。

## 冻结规则

- 定义以名称、正整数版本和内容摘要冻结。同名同版本同内容幂等，不同内容冲突；已有运行保存编译结果，不重新读取最新定义或能力目录。
- 编译拒绝未知字段/节点类型、重复节点、缺失依赖、普通 DAG 环、非祖先输入、输入输出类型不匹配、非法条件、未知精确能力版本，以及会让必需输入依赖可跳过上游的定义。
- 普通依赖图保持无环；返修是有界控制转换。固定自动返修预算为初始轮次加最多三次自动修订，耗尽后返回 `POLICY_BLOCKED` 等待人工处置。
- Runtime 注册同时固定 epoch、能力精确版本、Provider 身份/版本/认证水位和机器 GitHub 用户。重复同 epoch 的不同内容冲突；新 epoch 替代旧派发世代。
- TaskSpec 固定 NodeRun 轮次、尝试种类、输入摘要、Provider 选择、机器 GitHub 用户及 Git 仓库/ref/动作范围。范围是 Kowa 的准入和结果接受边界，不是宿主个人凭证的 GitHub 硬隔离声明。

## 状态、尝试和条件接受

- 同一次交付复用 Task；执行错误 Retry 创建新 Task、保持业务输入；人工续接创建新 Task、关联已接受答复并可选关联 Provider session；Rerun/返修创建新 NodeRun 轮次。
- progress 只更新观测序号和阶段，不改变 NodeRun 的决定状态或 WorkflowRun 放行结果。
- 接受结果必须同时匹配 Task、lease、fencing token、Runtime epoch、未过期时间、当前 NodeRun 轮次和输入。旧租约或旧轮次不得推进。
- 相同 Task 报告相同结果摘要返回既有接受事实；同身份不同摘要为 `VERSION_CONFLICT`。外部副作用未知仍需审计/对账，不能借幂等写回声称 exactly-once。
- Task `COMPLETED` 与 NodeRun `DECIDED`、业务 verdict、WorkflowRun 成功分别持久化。`needs_revision` 是已决定但未放行，不是执行失败，也不是质量通过。
- 取消先停止新派发；仍有在飞租约时运行处于 `CANCELING`，收束后才是 `CANCELED`。租约过期只隔离控制面写回。

## 持久化与恢复

PostgreSQL 保存已发布定义、冻结编译计划、WorkflowRun、NodeRun、Task、Runtime、租约、fencing 和报告摘要。派发用事务与 `FOR UPDATE SKIP LOCKED` 原子认领；结果接受锁定 Task 和当前 NodeRun 后在同一事务写入。进程重启从数据库恢复，不依赖内存连接或 Runner 本地目录。

`000002_workflow_execution` 是该版本的迁移水位。隔离数据库测试覆盖迁移升降、定义发布、派发、断开重连、重复/冲突报告、过期租约和恢复读取；它不调用真实 Provider、GitHub 写入或生产数据库。

## 失败语义与矩阵映射

定义/报告形状错误为 `VALIDATION_ERROR`；定义身份或同报告身份异内容为 `VERSION_CONFLICT`；Runner/Provider/能力不匹配为 `RESOURCE_UNAVAILABLE`；旧租约/轮次为 `STALE_RESULT`；返修耗尽或门禁不足为 `POLICY_BLOCKED`；外部写结果未核实为 `EXTERNAL_UNKNOWN`。

本版本覆盖 A08—A15、B01—B12、C03 和 C08 在 Workflow/Execution owner 范围内的确定性合同。HumanTask 权限与 GitHub 外部事实的完整实现分别仍属于后续 owner 阶段；S02 只冻结续接所需 Task 关联和消费边界。
