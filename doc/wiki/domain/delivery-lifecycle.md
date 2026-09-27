# 交付生命周期设计

状态：active（已确认的 MVP 领域状态与转换设计基线；未实现）。最近核验：2026-09-26。

本文集中定义 MVP 跨端领域身份、状态和转换语义；已确认的产品边界引用 [MVP 流程](../design/mvp-delivery.md)，Task 尝试身份引用 [执行尝试合同](../contracts/execution-attempts.md)。用户已采纳本文件的状态与轮次模型；它不是数据库 DDL、wire schema 或阶段合同，也不声称历史代码完整存在。

## 1. 事实与设计依据

- `CONFIRMED`：Kowa common 区分 WorkItem、WorkflowRun、NodeRun、Task、HumanTask，并规定中心状态、冻结输入和显式证据。
- `CODE_CONFIRMED`（历史示例）：demo.form 首次返回 awaiting_input；有输入时返回 completed。它只演示能力侧行为，不证明控制面持久化闭环。
- `STRONG_INFERENCE`：历史实体文档以 NodeID/Iteration 区分定义节点和运行轮次，以 CurrentWorkflowRunID 指向当前运行；结合 Kowa 不变量，可作为本设计起点。
- 证据路径与限制见 [设计证据目录](../../research/mvp-design-review.md)，不从缺失代码推断实现已通过。

## 2. 权威分工

| 对象 | 拥有的决定 | 不拥有的决定 |
| :--- | :--- | :--- |
| Workspace | 可使用的仓库、知识与资源边界 | 具体节点是否业务通过 |
| WorkItem | 目标、交付关联与关闭 | Runner 租约或节点执行细节 |
| WorkflowRun | 一次流程的推进与收口 | 直接执行模型或工具 |
| NodeRun | 某个定义节点的一轮状态、输入输出及业务判定依据 | 私自改变外部平台事实 |
| Task | 一次可租约执行尝试及报告 | 自行批准方案、关闭工作项 |
| HumanTask | 请求、答复及其适用对象 | 绕开控制面推进流程 |
| Artifact | 已发布内容与引用 | 决定所有消费者是否仍可使用旧结果 |

执行状态、业务结论、推进决定分别记录。Reviewer 正常执行但返回 needs_revision 时，不能以执行完成代替评审通过。进度只用于观测。

## 3. 生命周期状态

| 对象 | 设计状态 | 语义 |
| :--- | :--- | :--- |
| WorkItem | OPEN / CLOSED / CANCELED | 未完成、已满足交付合同、目标被终止；页面阶段由运行投影 |
| WorkflowRun | ACTIVE / PAUSED / CANCELING / SUCCEEDED / FAILED / CANCELED | 推进、显式暂停、取消收束及结果；普通节点等人不自动暂停全部分支 |
| NodeRun | PENDING / READY / RUNNING / WAITING_HUMAN / DECIDED / EXECUTION_FAILED / SKIPPED / CANCELED | 一轮节点是否有结果、在等人还是执行失败；业务 verdict 另存 |
| Task | QUEUED / LEASED / RUNNING / AWAITING_INPUT / COMPLETED / FAILED / EXPIRED / CANCELED | 一次执行尝试；终止后不复用身份重新 Execute |
| HumanTask | OPEN / RESOLVED / EXPIRED / CANCELED | 人工请求的处理状态；批准或驳回是答复内容 |

NodeRun 的 `DECIDED` 表示本轮输出已被接受。评审返回 `needs_revision` 时，本轮仍为 `DECIDED`，但放行条件未满足；编排器据明确 verdict 建立返修轮次。Task 的 `COMPLETED` 只说明能力调用结束。无法把两者直接投影成 WorkItem `CLOSED`。

Task `AWAITING_INPUT` 是本次执行的终止报告，NodeRun 进入 `WAITING_HUMAN`；人工答复后新 Task 可在该节点轮次继续。`EXPIRED` 隔离旧写回，不能推断模型进程或外部副作用已停止。

已确认：GitHub PR Review 和人为合并是外部事实，Kowa 观察并核对，不生成第二份需要用户在 Kowa 点击的合并批准。等待 Review 或合并时建立 `HumanTask` 作为外部操作提示；它由已核对的 GitHub 事实自动收束，不接收浏览器提交的“我已批准/合并”作为放行证据。知识确认、方案批准和人工恢复仍由用户在 Kowa 提交结构化答复。

已确认：`FAILED` 的 WorkflowRun 是已经结束的运行，不能在原记录上复活。可修复的节点失败使运行进入 `PAUSED`，由明确命令 Retry/Rerun 后回到 `ACTIVE`；历史错误和轮次仍保留。WorkItem `CLOSED` 不得通过 Rerun 重新执行。合并后验证失败若需要改代码，在同一未关闭 WorkItem 下建立关联修复运行与新 PR，旧运行终结后让出推进权。

## 4. 轮次与输入

定义节点有稳定身份；业务返修/Rerun 为受影响节点生成新 NodeRun 轮次，旧轮次保留。执行错误 Retry 仍处在原 NodeRun 轮次，但由新的 Task 执行相同有效输入。Task 的续接和重复投递语义以 [执行尝试合同](../contracts/execution-attempts.md)为唯一权威。

这与历史 demo 复用 Task ID 续接不同，相关取舍已进入 decision。派发字段、租约时间和 wire 仍需跨端契约冻结，不据此提前编写运行器。

旧结果保留，变化的是其对当前消费和审批的适用性。Rerun 先冻结影响范围并停止相关在飞执行，再生成新轮次；未受影响分支可以保留有效结果。旧报告只能补历史，不得恢复旧轮次的推进权。

## 5. 当前运行与修复

一个 WorkItem 同时只有一条交付运行拥有推进权，运行内部可以并行。修复运行保存来源运行、原因、已合并基线和新交付对象；它不会继承已失效的批准。

接替前必须收束旧任务并对账未决外部操作；切换当前运行后，旧运行不得接受普通恢复命令。辅助知识建议和离线评测不竞争交付推进权。

已批准的关闭及修复边界以 [MVP 流程 §4](../design/mvp-delivery.md#4-关闭与辅助工作) 为准，不在此维护第二套条件。

当前运行指针不是“最近创建记录”的同义词：接替操作必须验证旧执行已收束，并以一致的并发版本切换推进权。旧运行的历史结果保留，新运行不能把来源链接解释成自动通过原门禁。

## 6. 命令转换约束

| 命令/事件 | 前置与效果 | 禁止行为 |
| :--- | :--- | :--- |
| 启动运行 | 目标未关闭、资源有效、冻结定义和初始输入 | 静默选用漂移配置 |
| 接受任务结果 | 校验尝试、租约、输入轮次和产物；按结果契约推进 | progress 推进 DAG、摘要代替结果 |
| 接受人工答复 | 身份、权限、请求版本和对象版本有效 | 默认值当确认、旧批准套新版本 |
| Retry | 允许的执行错误且预算未耗尽；保持输入 | 修改输入后仍称 Retry |
| 返修/Rerun | 保留反馈并创建新结果轮次；使受影响旧证据不再用于放行 | 擦除失败、重置预算 |
| 取消 | 停新派发、通知执行侧并核对外部操作 | 将租约失效等同进程已停止或合并已回滚 |
| 关闭 | 控制面核验完整交付证据 | UI 分别拼出“成功”和“关闭” |

取消请求与关闭发生竞态时，以控制面串行接受的有效命令和外部已发生事实对账；不得出现既已正常交付关闭又仍允许取消推进的矛盾结果。具体并发版本由后端事务设计落实。

## 7. 允许的关键转换

| 主体 | 转换 | 前置与结果 |
| :--- | :--- | :--- |
| WorkItem | 创建 → OPEN | 登记目标、仓库和验收范围；还不表示运行已开始 |
| WorkItem | OPEN → CLOSED | 有效运行与关闭清单在同一权威提交；无未决必需门禁 |
| WorkItem | OPEN → CANCELED | 用户明确终止目标，且外部副作用已核对；不将已合并代码说成已回滚 |
| WorkflowRun | 创建 → ACTIVE | 冻结定义与初始输入，取得工作项交付推进权 |
| WorkflowRun | ACTIVE → PAUSED → ACTIVE | 有可恢复的阻断及明确恢复决定；历史轮次不擦除 |
| WorkflowRun | ACTIVE/PAUSED → CANCELING → CANCELED | 停新派发，收束在飞任务，核对未知外部操作 |
| WorkflowRun | ACTIVE → SUCCEEDED | 与对应 WorkItem 关闭同一次业务提交 |
| WorkflowRun | ACTIVE/PAUSED → FAILED | 明确结束本次运行，WorkItem 可保持 OPEN；后续需新运行 |
| NodeRun | PENDING → READY → RUNNING | 冻结依赖与有效输入；只有匹配能力、权限和资源才派发 |
| NodeRun | RUNNING → WAITING_HUMAN → RUNNING | 合法输入请求、人答复与新 Task；不长期占用 Worker |
| NodeRun | RUNNING → DECIDED | 接受结构化结果及产物；业务 verdict 决定放行或返修 |
| NodeRun | RUNNING → EXECUTION_FAILED → READY | 失败记录保留，明确 Retry 才重新就绪 |
| HumanTask | OPEN → RESOLVED | Kowa 内答复型接受授权主体的版本化答复；外部操作型接受经核对的 GitHub 事实 |
| HumanTask | OPEN → EXPIRED/CANCELED | 关联版本失效或目标被取消；迟到答复不得推进 |

外部操作型 HumanTask 的 `OPEN → RESOLVED` 由控制面接收 GitHub 对账事实触发，保存 PR、head、Review/merge 证据和实际外部操作者；它没有 Kowa `HumanResponse`。PR head 或目标对象变化时，旧提示 `EXPIRED`，新版本另建提示。提示被解决只说明对应外部动作已发生；是否满足当前合并门禁仍按有效 Review、仓库规则与合并结果分别判断。`observe_review`/`observe_merge` 是事实观察责任，不因为有 HumanTask 而重复拥有批准权。

条件跳过的节点必须记录原因；只对定义允许的分支有效，必需验证和质量门不能因 `SKIPPED`、`all_done` 或执行器报告 `COMPLETED` 而被当作通过。业务返修进入新轮次，循环预算属于流程政策，不由 NodeRun 状态自行归零。

真实 GitHub 合并后集成验证失败时，WorkItem 仍为 `OPEN`，当前运行 `PAUSED` 等待处置。人选择重试同一合并版本验证，可在当前运行继续；需要改代码时建立关联的新修复运行，前一运行结束并让出推进权。已关闭 WorkItem 的后续变更创建新 WorkItem，不从 `CLOSED` 转回 `OPEN`。
