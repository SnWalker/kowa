# kowa.web-application.v1

Owner: `backend:S06`. Version: `kowa.web-application.v1`.

本目录是**固定流程的 Web 应用入口**：把零散的 owner 端口组装成浏览器实际调用的操作，并冻结其请求/响应形状、正反样例与失败语义。它**只消费**其它 owner 的事实，不重新解释权限、状态、内容或门禁。

## 操作

| operation | 层次 | 方法/路径 | 授权与语义 |
| :--- | :--- | :--- | :--- |
| `workspace.list` | http | `GET /api/v1/workspaces` | session；只列出当前会话身份**仍是成员**的工作区（S01 成员表是权威；服务端逐条重新判定，绝不本地拼装） |
| `intake.start-knowledge` | http | `POST /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/runs` | session+member(developer/admin)+CSRF+`requestKey`+`expectedVersion`；运行准入：冻结定义、项目/知识基线 OID、Provider 与机器身份，创建 WorkflowRun 与首个节点轮次，经 S05 `associateRun` 取得推进权，随后产出知识推荐与确认人工任务 |
| `run.detail-history` | http | `GET /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/runs/{runID}` | session+member；运行、冻结定义、**全部已物化节点轮次（含历史 iteration）**、每轮的 verdict 与冻结输入摘要、**每轮的 Task 尝试**（尝试序号/种类/状态/冻结输入摘要/结果摘要/Runtime/租约/消费的 Artifact 引用）、修复谱系、知识确认状态、**辅助知识建议分支摘要与逐次尝试历史**（状态/冻结输入摘要/Artifact/脱敏失败原因）与观察水位 |
| `run.recovery` | http | `POST /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/runs/{runID}/recovery` | session+member(developer/admin)+CSRF+`requestKey`+`expectedVersion`；`kind ∈ retry｜rerun｜cancel｜repair｜retry-knowledge-suggestion`，服务端按当前推进权、版本、返修预算与外部副作用收束；`retry-knowledge-suggestion` 只重试已关闭工作项上、由本运行关闭的辅助知识建议分支（不创建/重开业务运行，不改关闭事实，不写知识仓） |
| `delivery.artifact-fixrun` | http | `GET /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/artifacts` | session+member；已发布交付物清单（冻结引用字段来自 artifact 模块自身行）与修复运行谱系 |
| `journey` | http | `GET /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/journey` | session+member；跨阶段组合只读视图：冻结定义、当前运行与已决节点数、待办人工任务、交付物数量与关闭记录 |

## 冻结语义（跨操作）

- **运行准入唯一性**：一个 WorkItem 同时只有一条推进运行；准入只登记并启动固定流程，不绕过 S05 的当前运行约束。
- **冻结基线**：准入把项目仓与知识仓的**默认分支 head 解析为 40 位 commit OID** 并冻结；此后所有读 scope 只用该 OID，`refs/heads/<branch>` 仅用于写操作授权。分支最新 tip 永不替代冻结版本。
- **冻结输入绑定**：每个被派发的 Task 在存在之前先持久化其冻结输入（S03 EffectiveInput canonical + 引用制品 + 项目/知识 OID + 能力/Provider/scope/deadline）；绑定不可漂移，重启复用同一身份。缺失或不可解析一律 fail closed。
- **知识未确认不派发**：确认答复经 S05 接受后，由 S06 调用 S03 确认端口；未确认时 `plan` 及其下游不存在任何 Task。
- **门禁不可绕过**：必需上游未 `DECIDED` 或 verdict 未通过时下游不物化；返修耗尽转人工处置，不自动放行。
- **关闭唯一权威**：运行 `SUCCEEDED` 与 WorkItem `CLOSED` 只由 S05 关闭事务提交；本族不提供第二条成功路径。

## 失败语义

| 类别 | 触发 |
| :--- | :--- |
| `VALIDATION_ERROR` | 形状/枚举/摘要不合法、未知字段 |
| `UNAUTHORIZED` / `FORBIDDEN` | 无会话、CSRF 不符、非成员、跨 Workspace 对象、越权恢复命令 |
| `VERSION_CONFLICT` | `expectedVersion` 漂移、同请求键异内容、同一尝试身份异内容 |
| `RESOURCE_UNAVAILABLE` | 仓库/知识源不可物化、无满足全部能力的 Runtime、未绑定的输入读取 |
| `POLICY_BLOCKED` | 运行非 ACTIVE、返修预算耗尽、未确认知识、未建立合并基线的修复运行 |
| `EXTERNAL_UNKNOWN` | 外部事实尚未核对 |
| `INTERNAL_ERROR` | 控制面内部失败（不暴露细节；服务端日志记录原因） |

## 正反样例

- 正：`examples/positive-*.json`（均通过冻结 schema 与真实生产者 schema 测试）。
- 负：`examples/negative-*.json`（未知字段、写成分支而非 OID、未知运行状态、未知角色）。

## 消费者义务

1. 逐操作 pin `delivery.json` 的文件 SHA-256 与 `schemaSha256`。
2. 身份与授权一律来自服务端判定；浏览器不缓存权威、不推断放行。
3. 声明的数组字段永远是数组：生产者把空集合序列化为 `[]`（`runBindings`、`nodes`、`artifacts`、`openHumanTasks`）。
4. 组合读带 `watermark`：`observedAt` 是服务端生成本次投影的时间，`runUpdatedAt` 是当前运行最后一次状态变更时间；浏览器据此区分"没有新内容"与"数据陈旧"，不自行拼装水位。
4. 运行/节点/Task 三层身份不得合并展示；`COMPLETED` 不等于 verdict 通过。

## 复跑

```bash
bash scripts/check-web-application-contract.sh
bash scripts/test-fixed-workflow-integration.sh
```
