# kowa.fixed-workflow.v1

Owner: `backend:S06`. Version: `kowa.fixed-workflow.v1`.

本目录是首期**唯一固定内置工作流**的机器合同：被冻结的授权定义文档、编译后的定义身份，以及 Web 消费的固定流程视图。它是固定流程准入、编译与 golden 的唯一真源；`internal/fixedflow` 直接加载同一份文档。

## 冻结内容

| 文件 | 职责 |
| :--- | :--- |
| `examples/positive-definition.json` | 授权定义文档本体（`kowa.workflow-execution.v1` 形状，由 S02 冻结 schema 校验），与 `internal/fixedflow/definition.json` **逐字节相同** |
| `schema.json` | `kowa.fixed-workflow.v1`：编译后视图与冻结错误包络 |
| `examples/positive-fixed-workflow-view.json` | `fixed.workflow` 的**真实生产者输出**（由 `internal/fixedflow.BuildView` 生成，测试断言两者逐字节相同） |
| `examples/negative-*.json` | 必须被冻结 schema 拒绝的负样例 |
| `delivery.json` | 操作登记：`contract`/`owner`/`schemaSha256`/`operations` |

## 身份与摘要

- 定义名 `kowa-fixed-workflow`，版本 `1`。名字遵循 S02 编译器的标识符语法（`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`）；**点号只出现在合同版本号里，不进入定义身份**。
- 内容摘要 = 编译器对定义的 canonical JSON 编码取 SHA-256（`workflow.Compile` 的同一规则）。同名同版本同内容幂等，不同内容冲突。
- 当前冻结摘要见 `examples/positive-fixed-workflow-view.json` 的 `definitionDigest`；`internal/fixedflow/testdata/compiled-plan.golden.json` 保存编译计划的 golden。

## 节点责任（17 节点）

`knowledge_recommend → knowledge_confirm → knowledge_bind → plan → plan_review → plan_approval → {code, design_tests} → verify_change → {test, security_review} → code_review → publish_pr → observe_review → observe_merge → integration → close`

- 两类有界自动返修区域：`plan_review → plan`、`code_review → code`，各为「初次 + 最多三轮自动修订」（`maxAutoRevisions: 3`）。
- 三处汇合：`test`（`verify_change` + `design_tests`）、`code_review`（`test` + `verify_change`）、`publish_pr`（`code_review` + `security_review` + `verify_change`）。
- 控制面操作：`knowledge.recommend`、`knowledge.bind`、`delivery.verify_change`、`github.observe_review`、`github.observe_merge`、`delivery.close`。
- 人工任务：`knowledge_confirmation`、`plan_approval`（外部事实型待办由 S05 合同提供，不在定义里声明种类）。
- 能力目录（Runner 执行）：`plan.create`、`plan.review`、`testcases.design`、`code.implement`、`test.execute`、`code.review`、`security.review`、`delivery.publish_pr`、`delivery.integration_verify`，均为版本 1。

节点拓扑、类型、必需性、返修预算、操作与人工任务种类的改变必须同时更新定义文档与 golden；`TestFrozenTopology` 与 `TestCompilerRejectsBrokenVariants` 会拒绝静默改动。

## 操作

| operation | 层次 | 服务端行为 |
| :--- | :--- | :--- |
| `fixed.workflow` | http | `GET /api/v1/workspaces/{workspaceID}/fixed-workflow`：session+member，返回冻结定义名/版本/摘要、拓扑序与全部节点责任，只读 |

## 失败语义

- 定义文档不合法、能力不在目录内、绑定非祖先、普通依赖环、返修目标非祖先、预算漂移、重复节点：**不发布**（`VALIDATION_ERROR`）。
- 同名同版本不同内容：`VERSION_CONFLICT`。
- 未认证/非成员：`UNAUTHORIZED` / `FORBIDDEN`。

## 消费者义务

1. 浏览器只渲染服务端返回的节点责任，不推断放行、不本地改图。
2. 前端消费时 pin `delivery.json` 的文件 SHA-256 与 `schemaSha256`；不得把 schema 摘要当作 manifest 文件摘要。
3. 视图只描述定义结构；本轮/历史轮次与 verdict 由 `kowa.web-application.v1` 的运行读投影提供。

## 复跑

```bash
bash scripts/check-fixed-workflow-contract.sh
```
