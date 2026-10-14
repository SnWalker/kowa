# kowa.delivery-governance.v1

唯一 owner：`backend:S05`。机器合同 [schema.json](schema.json)；操作与交付层次 [delivery.json](delivery.json)。行为权威：`doc/wiki/contracts/workitem-delivery-v1.md`。

Producer：控制面 HumanTask / GitHub 交付对账 / 验证与关闭服务（真实 Server 组合根 + PostgreSQL/内存适配器），GitHub 事实经 backend:S04 的只读观测 seam 读取，身份与成员授权来自 backend:S01。Consumer：Web HumanTask/交付证据页（frontend:S02/S04/S05）、固定流程编排与关闭用例（backend:S06）、frontend:S01 的 runtime 页 Git remote 投影。

| Operation | 层次 | 方法/路径 | 证明 | 成功 |
| :--- | :--- | :--- | :--- | :--- |
| human.list | http | `GET .../human-tasks[?state=open]` | 会话 + 当前成员 | 200 `humanTaskList` |
| human.detail | http | `GET .../human-tasks/{humanTaskID}` | 会话 + 当前成员 | 200 `humanTask`（含服务端判定的 `availableActions`） |
| human.response | http | `POST .../human-tasks/{humanTaskID}/responses` | 会话 + 当前成员且被指派 + CSRF + `requestVersion`/对象版本一致 | 200 `humanTask`（`RESOLVED`，含答复与续接意图） |
| delivery.query | http | `GET .../work-items/{workItemID}/delivery` | 会话 + 当前成员 | 200 `deliveryView` |
| gitRemote.status | http | `GET .../git-remote-status` | 会话 + 当前成员 | 200 `gitRemoteStatus`（真实只读探测） |
| github.verifyChange | application | 受控交付核验 | 当前运行 + 项目仓 + 受保护分支拒绝 | 结算 `SUCCEEDED/FAILED/UNKNOWN` |
| github.publishPr | application | 发布准入与结果对账 | 必需测试 + 独立评审 + 已结算 `verify_change` | 只有核对到真实 PR 且作者为冻结机器身份才 `SUCCEEDED` |
| github.observe | application | PR/Review/checks/merge 只读对账 | 每次读取先核对活动身份 | 追加观察并结算/过期相关待办 |
| verification.record | application | 验证事实生产端口 | 真实/模拟来源、被测版本、生产者身份逐项记录 | 返回 `verificationEvidence` |
| delivery.close | application | 关闭用例 | 关闭清单逐项重读 + 当前运行 + 版本一致 | WorkItem `CLOSED` 与运行 `SUCCEEDED` 同一事务提交 |

## 两种 completion mode

- 关闭清单的绑定在生产者侧逐项重读：知识绑定事件必须存在于本 Workspace、指向所绑快照且 `subject_id` 为该 WorkItem（S03 权威行）、交付对象必须是当前运行/同一仓库/同一交付提交上已结算 `publish_pr` 操作实际产出的 PR、必需测试以关系表 `closure_required_test` 绑定到验证证据行。

- `kowa_answer`：Kowa 内版本化结构化答复。服务端逐项校验 `requestVersion`、对象绑定（kind/id/version/digest）与 actor（候选人或被指派 Workspace 角色）；答复、任务解决与续接意图（`continuation`，`PENDING`）在同一事务提交。客户端不能提交冻结字段以外的字段，预填值不构成批准。
- `external_fact`：只由已核对的 GitHub 事实收束，**永远不产生 HumanResponse**，浏览器没有“我已批准/合并”按钮。对象只能是某个 head OID 上的 `pull_request_review` 或 `pull_request_merge`；head 前进时旧待办 `EXPIRED`，迟到答复被拒绝（A10/A12）。

## 交付对账

- `publish_pr` 准入要求必需测试通过、独立评审通过（两者都必须是 `real` 来源、绑定同一 commit 与仓库）以及同一 commit 上已结算的 `verify_change`；PR 作者必须是运行冻结的机器 GitHub 身份。
- 结果未知时先查询对账：`ATTEMPTED/UNKNOWN` 状态禁止为同一 head 再次准入；查到真实对象后复用（`reconciled=true`），绝不盲目重复创建（A19/A20）。只有在一次成功读取确认不存在对象后，才允许显式 `AbandonOperation` 结算为 `FAILED` 并开始新的尝试；读取仍不可读时拒绝放弃（A21）。
- `verify_change` 只接受远端 ref 头等于交付 commit 的观测；不一致判 `FAILED`。
- 观察是追加式的：失败的读取只记录一次 unknown 尝试并保留最后成功事实，绝不把旧观察冒充最新（A20）。
- checks 区分 `none / pending / failed / passed`；`none` 不读作通过。Review 必须来自非 PR 作者、账号类型为 `User`、且 GitHub 报告其对本仓库有 `admin/write` 权限（`reviewerPermission`，按 `repos/{owner}/{repo}/collaborators/{login}/permission` 读取并核对账号 id），并绑定当前 head；已合并但缺少有效 Review 不满足关闭（A22）。该规则同时用于外部事实结算与关闭清单，`authorAssociation` 仅为信息字段。
- "人工在 GitHub 完成合并"是硬条件：`mergedById` 必须是有名账号，且不能是该运行的冻结机器身份；`mergedByGitHubUserId` 进入关闭记录与 checklist digest。

## 验证与关闭

- `required_tests / independent_review / security_review / post_merge_integration` 是同步门禁；`knowledge_suggestion / offline_evaluation` 是辅助事实，失败**不阻塞**关闭（A26）。
- 关闭清单逐项绑定：需求摘要、定义名/版本/摘要、策略版本、运行、必需测试、独立评审、有效 Review（观察 ID + head + 同仓库同 PR）、实际合并（观察 ID + 合并提交 OID + 合并账号，不是目标分支最新 HEAD）、合并后验证（针对合并提交）；版本失配、旧运行、缺证据、竞态一律拒绝。知识快照/绑定事件是**登记引用**（形状 + 摘要入清单），其存在性与确认权威属 backend:S03。
- 关闭与运行 `SUCCEEDED` 在同一权威事务提交：不存在“运行成功但未关闭”或“关闭但运行未成功”的可见中间态；重复提交同一命令幂等，竞争的关闭只有一个胜者。
- 合并后验证失败时 WorkItem 保持 `OPEN`，修复入口是关联的修复运行（`workitem.associateRun`），不自动回滚。

## Git remote 投影（frontend:S01 缺口）

`gitRemote.status` 是 runtime 页“Git remote 可用性与权限缺口”的唯一 owner 交付（不修改 `kowa.runtime-web.v1`，不重解释 Runtime 健康）。它区分：

- Web actor（`requestedByGitHubUserId`，来自会话）；
- 登记机器身份（`machineIdentity.runtimeIds` + `registeredGitHubUserIds`，来自 S04 运行时注册/执行配置）；
- 实际 GitHub 查询身份（`observedGitHubUserId`，真实 `gh api user` 数字 ID）；
- 实际 remote 读取事实（真实 `git ls-remote`，记录实际生效的 `transport`：https 或 ssh）；
- GitHub 报告的 push 权限事实（`pushPermission`，来自 GitHub API 的 `permissions`）；
- `writeObservation` 恒为 `none`：本阶段没有任何真实写入观测，权限事实**不等于**实测可写。

未知/未探测一律 `unknown` 且带原因；只有真实观测才能给出 `readable`/`granted`，登录、注册或“CLI 存在”都不构成可用证据。真实读观测证据：`doc/Kowa后端设计/record/evidence/S05-git-remote-real-read-only-probe.json`（无写入）。

## 样例与门禁

`examples/` 提供正例（两种 mode 的待办、已答复待办、交付视图、git remote 状态、答复请求）与 schema 反例（未知字段、缺 `availableActions`、伪造“浏览器批准”动作、声称已写入的 `writeObservation`、非法 transport、非法 checks 状态）。

`bash scripts/check-delivery-governance-contract.sh` 使用固定 jv v0.7.0 校验样例、摘要一致性、http/application 层次登记，并运行 `TestDeliverySurfaceMatchesTheFrozenSchema`（真实服务输出过冻结 schema）。
