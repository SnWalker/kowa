# WorkItem/Delivery 治理 v1 契约

状态：active。版本：`kowa.workitem.v1`、`kowa.delivery-governance.v1`。Owner：`backend:S05`。最近核验：2026-10-15。

本页拥有 WorkItem 基础、HumanTask、GitHub 交付对账与关闭的共享跨端语义；精确机器字段、正反样例与消费说明见 [`api/workitem/v1`](../../../api/workitem/v1/README.md) 与 [`api/delivery-governance/v1`](../../../api/delivery-governance/v1/README.md)。领域状态以[交付生命周期](../domain/delivery-lifecycle.md)为准，人工批准边界以 [MVP 流程](../design/mvp-delivery.md) 为准，不在此复制第二套门禁。

## Producer 与 Consumer

| 合同 | Producer | Consumer |
| :--- | :--- | :--- |
| WorkItem（intake/list/detail/cancel） | 控制面 WorkItem 服务（会话与成员授权来自 S01） | Web WorkItem 页面（frontend:S02、DAG/历史见 frontend:S04）、审计 |
| 当前运行关联（associateRun） | 运行准入（backend:S06） | WorkItem 服务、修复运行谱系 |
| HumanTaskRequest/Response | 控制面 HumanTask 服务；外部事实由已核对 GitHub 观察收束 | Web 人工任务页面（frontend:S05）、固定流程续接（backend:S06） |
| GitHubDeliveryObservation / ExternalOperation | 控制面只读对账；写入由 Runner 的受控通道与独立 `publish_pr` 能力实际发生 | 门禁、Web 交付证据页（frontend:S06）、关闭用例 |
| VerificationEvidence / ClosureEvidence | 真实验证者、外部 CI 与关闭用例 | 门禁、历史、Web、审计 |
| GitRemoteStatus | 控制面只读真实观测（gh 身份 + `git ls-remote` + GitHub 权限事实） | frontend:S01 runtime 页“Git remote 可用性与权限缺口” |

一个合同只有一个 owner。浏览器不生产 Runtime 事实，Runner 报告不构成 GitHub 平台事实，控制面不代替 Runner 执行写入。

## 冻结规则

- WorkItem 创建只登记目标与需求/验收版本，**不启动流程**；`OPEN/CLOSED/CANCELED` 之后不回退，变更创建关联新项。
- 一个 WorkItem 同时只有一条交付运行拥有推进权；修复接替要求来源运行已结束，且不继承旧批准。
- HumanTask 两种 completion mode 不可互换：`kowa_answer` 由被指派成员在 Kowa 内提交版本化答复；`external_fact` 只由已核对事实收束，不产生 HumanResponse，也没有浏览器批准入口。答复绑定请求版本与对象版本，对象版本漂移或 head 前进使旧待办失效，迟到答复被拒绝。
- 接受答复、解决任务与写续接意图在同一事务；本阶段不替固定流程发起新任务，续接意图由 backend:S06 消费。
- 外部交付操作只有准入与事实接受在控制面：`verify_change` 核对仓库/ref/OID，`publish_pr` 要求必需测试与独立评审有效并核对 PR 作者为冻结的机器身份、Review 来自非作者且适用于当前 head、checks 状态分明、合并提交 OID 为实际合并结果。未知结果先对账再决定重试，重复观察不重复推进，缺少有效 Review 的已合并交付不满足关闭。
- 关闭清单逐项绑定精确事实（需求摘要、定义、策略、运行、必需测试、独立评审、有效 Review、实际合并、合并后验证）；关闭与交付运行 `SUCCEEDED` 在同一权威事务提交；辅助知识建议与离线评测失败不阻塞关闭；合并后验证失败保持 WorkItem `OPEN` 并提供关联修复运行，不自动回滚。
- Git remote 投影只报告真实观测：区分 Web actor、登记机器身份、实际查询身份、真实 remote 读取与 GitHub 权限事实，以及未知/未探测；`writeObservation` 在缺少真实写入观测时恒为 `none`，权限事实不等于实测可写。

## 与前序版本的边界

`kowa.workitem.v1` / `kowa.delivery-governance.v1` 只新增本阶段合同族；`kowa.runtime-web.v1`（backend:S04）、`kowa.workflow-execution.v1`（backend:S02）与 `kowa.knowledge-artifact.v1`（backend:S03）的形状与语义不变。运行准入与 `task → 冻结输入引用` 绑定仍属 backend:S06；本阶段只提供 WorkItem 事实与已接受的人答复。

消费者准入须 pin 对应 `delivery.json` 的**文件** SHA-256 并同时核对 `schemaSha256`；不得把 schema 摘要当作 manifest 文件摘要，也不得从候选字段推断能力。
