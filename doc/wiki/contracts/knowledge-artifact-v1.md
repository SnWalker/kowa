# Knowledge/Artifact v1 契约

状态：active。唯一 owner backend:S03，版本 `kowa.knowledge-artifact.v1`。精确字段、规范化字节、正反样例和消费者核对见 [机器合同](../../../api/knowledge-artifact/v1/README.md)。本页拥有跨端交接语义，不维护阶段状态。

知识快照固定仓库身份、提交与条目原始字节；成功读取后空结果仍需显式确认。内容身份与人工绑定事件分离；重复确认不丢审计。可读候选与当前任务批准分别判断，既有有效输入不随知识更新或重绑而改变。人工确认由服务端已认证且获授权的应用调用提交；本合同不替代后续 HumanTask 答复接受合同。

Artifact 先登记 STAGING，再上传校验为 UPLOADED，最后发布 PUBLISHED。Task 发布必须匹配 S02 已接受结果的 output.artifactRefs；operation 仅供已认证的受控系统用例。发布时原子添加已接受 Task 所属 run 保留引用；发布后 Ref 与正文不可覆写。AWAITING_INPUT 可以发布供审阅候选，但 requiresApproval=true 的候选在显式批准前不能建立执行输入/run 引用。正文读与输入消费均检查完整引用、归属、大小与摘要；storageRef 只通过稳定端口解析。

输入组装检查冻结定义的必需上游、字段类型与冲突，重查当前绑定、上游发布和批准。canonical 与保留引用原子持久化，恢复直接消费存储字节。Retry 可读取原输入，无需重读源仓；Retry/Rerun 调度本身属于后续阶段。

Workspace 锁串行成员权限、发布、引用与清理。有效引用禁止清理；孤立内容宽限后再检查引用，先提交 DELETING，再幂等移除，最后 DELETED。失败可重试；Orphans 只列候选，不授权删除。v1 无删除有效引用 API，开发期间永久保留核心证据；生产保留、容量、备份和 S3 部署后续确认。本地 adapter 只用于开发，根目录独立于 Task，create-only/fsync/os.Root 防覆盖与逃逸；不承诺生产 S3 就绪。

## 验收矩阵映射

| 条目 | 测试 / 证据 | 层次 |
| :--- | :--- | :--- |
| A03 | TestKnowledgeContract/A03_unreachable_missing_and_digest | 知识源失败不可冒充空 |
| A04 | TestKnowledgeContract/A04_explicit_empty_requires_successful_read；PG A04_empty_snapshot_still_requires_confirmation | 成功空结果/确认事件 |
| A05 | TestKnowledgeContract/A05_frozen_commit_C01_repeat_content；PG A05_A16_frozen_input_recovery_and_protection | 固定 commit/内容与恢复 |
| A06 | TestKnowledgeContract/A06_C02_prefilled_is_not_confirmation | 预填不构成确认 |
| A16 | PG A05_A16_frozen_input_recovery_and_protection + TestArtifactReadProcess | 删除 Task 目录，独立子进程/连接读取 |
| A17 | TestArtifactContract/A17_digest_mismatch、TestUploadAndCleanupRecovery | 摘要错/上传失败不发布 |
| A27 | TestArtifactContract/A27_reference_cleanup_race；PG A27_database_publication_reference_cleanup_race | 引用/发布/清理竞争 |
| C01 | PG C01_confirmation_idempotency_and_audit | 同内容复用、两事件、幂等冲突 |
| C02 | PG C02_candidate_publication_scope_and_integrity、C02_readable_candidate_requires_approval；TestCandidateRequiresExplicitApproval | 可读候选未批准不可执行 |

附加 consumer 检查：TestMissingRequiredUpstream、TestRequiredUpstreamDeclarations、TestEffectiveInputBindings、TestContractExamples、TestKnowledgeArtifactTaskPublication（错误 lease 后正确接受；未报告上传仍不可发布）。这都是新隔离数据验收，不表示真实 WorkflowRun 或生产迁移已经运行。
