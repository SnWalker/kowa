# 固定流程与 Web 应用入口 v1 契约

状态：active。版本：`kowa.fixed-workflow.v1`、`kowa.web-application.v1`。Owner：`backend:S06`。最近核验：2026-10-15。

本页拥有首期**唯一固定内置工作流**（被冻结的定义与运行准入）与**固定流程的 Web 应用入口**（浏览器实际调用的操作）的共享跨端语义；精确机器字段、正反样例与消费说明见 [`api/fixed-workflow/v1`](../../../api/fixed-workflow/v1/README.md) 与 [`api/web-application/v1`](../../../api/web-application/v1/README.md)。节点责任以 [MVP 流程](../design/mvp-delivery.md) 为准，运行/节点/Task 身份以 [Workflow/Execution v1 契约](workflow-execution-v1.md) 为准，知识内容与 Artifact 以 [Knowledge/Artifact v1 契约](knowledge-artifact-v1.md) 为准，人工任务与交付关闭以 [WorkItem/Delivery 治理 v1 契约](workitem-delivery-v1.md) 为准；本页不复制这些门禁。

## Producer 与 Consumer

| 合同 | Producer | Consumer |
| :--- | :--- | :--- |
| WorkflowDefinition / CompiledPlan（固定流程） | 内置定义作者与编译器（`internal/fixedflow`，随二进制冻结） | 运行准入、Execution、Web 定义视图 |
| 运行准入与推进（`intake.start-knowledge`、`run.recovery`） | 控制面编排（`internal/orchestration`，owner backend:S06） | Web、审计、修复谱系 |
| `task → 冻结输入引用` 绑定与冻结输入 manifest | 运行准入（owner backend:S06）；经 Runtime transport 交给 Runner | Runner/Worker、审计 |
| 组合运行查询（`run.detail-history`、`journey`、`delivery.artifact-fixrun`、`workspace.list`） | 控制面组合投影 | Web WorkItem/DAG/交付页面、审计 |
| `fixed.workflow` | 内置定义视图 | Web DAG 渲染、审计 |

浏览器不生产运行、任务或冻结输入；Runner 不执行 Web 业务命令；控制面不执行模型任务。

## 冻结规则

- **一条流程**：MVP 只发布 `kowa-fixed-workflow@1`。定义名、正整数版本与内容摘要共同冻结；同名同版本同内容幂等、不同内容冲突；已有运行不重新读取最新定义或能力目录。扩展方式仍是"新增流程 / 新定义版本 / 新能力"，不改写既有运行。
- **准入唯一性**：一个 WorkItem 同时只有一条推进运行；准入创建运行并把推进权交给 S05 的当前运行关联端口，客户端不能自报 actor、版本或裁决。
- **冻结基线**：准入把项目仓与知识仓的默认交付分支 head **一次性**解析为 40 位 commit OID 并冻结；此后每个任务的**读** scope 只用这些 OID，`refs/heads/<branch>` 只用于写操作授权（工作分支）。分支最新 tip 永不替代冻结版本；远端分支前进不改变已冻结输入。
- **冻结输入**：每个被派发的 Task 先持久化其冻结输入（S03 EffectiveInput 的 canonical 字节摘要即 `task.inputDigest` + 引用制品 + 项目/知识 OID + 能力/Provider/scope/deadline），随后才存在 Task；绑定不可漂移，重启与重试复用同一身份、摘要与截止时间；缺失或不可解析一律 fail closed，Runner 在取到并校验冻结输入之前不执行 Worker。
- **知识未确认不派发**：确认由 S05 的版本化答复接受，随后由本阶段调用 S03 确认端口并组装有效输入；未确认时知识绑定下游不存在任何 Task。
- **门禁不可绕过**：必需上游未决定或 verdict 未通过时下游不物化；两类自动返修区域（方案、代码）各有"初次 + 最多三轮"预算，耗尽转人工处置而非放行；必需验证失败保持失败事实。
- **关闭唯一权威**：运行成功与 WorkItem 关闭只由 S05 的关闭事务提交；本阶段不提供第二条成功路径。
- **Web 只读投影**：组合查询只报告服务端判定的事实（状态、verdict、轮次、引用、水位）；声明为数组的字段恒为数组；`Task COMPLETED` 不等于 verdict 通过。

## 失败语义

沿用 [MVP 跨端最小语义契约](mvp-cross-end-semantics.md) 的类别：`VALIDATION_ERROR`、`UNAUTHORIZED`、`FORBIDDEN`、`VERSION_CONFLICT`、`RESOURCE_UNAVAILABLE`、`EXTERNAL_UNKNOWN`、`POLICY_BLOCKED`。典型映射：非成员/跨 Workspace 为 `FORBIDDEN`；`expectedVersion` 漂移或同请求键异内容为 `VERSION_CONFLICT`；仓库/知识源不可物化、无满足全部能力的 Runtime、读取未绑定输入为 `RESOURCE_UNAVAILABLE`；运行非 ACTIVE、返修预算耗尽、未确认知识、未建立合并基线的修复运行、越界恢复命令为 `POLICY_BLOCKED`。

## 消费者义务

1. 逐操作 pin 对应 `delivery.json` 的文件 SHA-256 与 `schemaSha256`；不得把 schema 摘要当作 manifest 文件摘要。
2. 浏览器只消费服务端给出的动作与状态，不自行推断放行、关闭或权限。
3. 运行/节点/Task 三层身份与 `verdict`/`state` 分离展示；历史轮次不得混入当前轮次。
4. 冻结输入与项目/知识 OID 由服务端绑定，前端不参与计算，也不把分支名当作冻结版本。

## 复跑

```bash
bash scripts/check-fixed-workflow-contract.sh
bash scripts/check-web-application-contract.sh
bash scripts/test-fixed-workflow-integration.sh
```
