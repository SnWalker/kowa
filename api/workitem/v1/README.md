# kowa.workitem.v1

唯一 owner：`backend:S05`。机器合同 [schema.json](schema.json)；操作与交付层次 [delivery.json](delivery.json)。行为权威：`doc/wiki/contracts/workitem-delivery-v1.md`。

Producer：控制面 WorkItem 服务（真实 Server 组合根 + PostgreSQL/内存适配器），会话与成员授权来自 backend:S01。Consumer：Web WorkItem 列表/创建/详情（frontend:S02/S03）、运行准入（backend:S06，消费 WorkItem 事实与当前运行指针）。

| Operation | 层次 | 方法/路径 | 证明 | 成功 |
| :--- | :--- | :--- | :--- | :--- |
| workitem.intake | http | `POST /api/v1/workspaces/{workspaceID}/work-items` | 可信 Origin + 会话 cookie + 当前成员且角色为 developer/admin + CSRF | 201 `workItem`（`OPEN`，v1，目标仓库来自服务端项目绑定） |
| workitem.list | http | `GET .../work-items` | 会话 + 当前成员 | 200 `workItemList` |
| workitem.detail | http | `GET .../work-items/{workItemID}` | 会话 + 当前成员；跨 Workspace 与不存在一律 404 | 200 `workItem` |
| workitem.cancel | http | `POST .../work-items/{workItemID}/cancel` | 会话 + developer/admin + CSRF + `expectedVersion` | 200 `workItem`（`CANCELED`） |
| workitem.associateRun | application | 当前运行关联端口 | 唯一 producer = 运行准入（backend:S06）；要求前一运行已结束、目标未关闭 | 返回更新后的 `workItem` |

所有响应设置 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`；请求体严格解码，未知字段一律 400，actor 只来自会话。

## 语义

- 创建只登记目标：`OPEN`、标题、需求版本与验收版本（服务端按内容计算 `sha256:` 摘要），**不启动任何工作流**，也不绑定运行。
- 状态只有 `OPEN / CLOSED / CANCELED`，关闭或取消后不回到 `OPEN`；后续变更创建关联的新 WorkItem（A25）。
- 一个 WorkItem 同时只有一条交付运行拥有推进权（`currentRunId`）。修复运行必须指出它接替的当前运行，且该运行已经结束（`SUCCEEDED/FAILED/CANCELED`）；接替后旧运行不再拥有推进权，也不继承旧批准（A23）。
- 取消要求该 WorkItem 的**所有外部操作都已对账**（`SUCCEEDED/FAILED`）：只要还有 `PENDING/ATTEMPTED/UNKNOWN` 的外部操作，取消以 `EXTERNAL_UNKNOWN` 拒绝。取消不声称已回滚任何已经发生的 GitHub 副作用（A24）。
- 目标仓库恒为 Workspace 项目绑定，客户端不能指定；第二条可写仓库在契约层不可表达。

## 错误与幂等

`VALIDATION_ERROR=400`、`UNAUTHORIZED=401`、`FORBIDDEN=403`、`VERSION_CONFLICT=409`、`RESOURCE_UNAVAILABLE=404/503`、`EXTERNAL_UNKNOWN=409`、`POLICY_BLOCKED=422`。

命令以 `(caller, command, target, requestKey)` 幂等：同键同内容返回原结果，同键不同内容返回 `VERSION_CONFLICT`，旧内容不会被静默接受。版本冲突（`expectedVersion` 漂移）一律拒绝，拒绝后事实保持不变且随后的合法请求仍成功。

## 样例与门禁

`examples/` 提供正例（创建/列表/详情/取消请求）与 schema 反例（未知字段、非法摘要、非法状态、请求体走私 actor）。JSON Schema 不验证授权、版本竞态或数据库状态；这些由 Go 测试与隔离 PostgreSQL 覆盖。

`bash scripts/check-workitem-contract.sh` 使用固定 jv v0.7.0 校验样例、摘要一致性与“仅核心端口不得登记为 http”，并运行 `TestWorkItemSurfaceMatchesTheFrozenSchema`（真实 handler 输出过冻结 schema）。
