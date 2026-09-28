# MVP 首批阶段的跨端依赖地图

性质：阶段建立前的设计输入，不是 `stage/Sxx-*.md`、阶段状态表或 record。整理：2026-09-27。[开发前准入清单](mvp-predevelopment-checklist.md)的设计项及[权威勘误](mvp-machine-gh-authority-proposal.md)已完成；下列顺序不自动改变两端 `currentStage=null`。正式创建阶段时仍须做当次入口审计。

| 依赖顺序 | 责任端/模块 | 应冻结的输出 | 下游消费者与约束 |
| :--- | :--- | :--- | :--- |
| 0 | 跨端设计（阶段前） | 有效 common/decision、固定流程产品边界、机器 gh 身份/人工门禁/风险接受、真实验收目标 | 后端和前端阶段都以相同版本为准；不得把待审 research 当正式 wire |
| 1 | 后端 Identity/Workspace | Web 会话、Workspace 成员、双仓绑定、机器 Git 执行身份与允许动作语义 | 前端登录/配置；Workflow 准入；Runner 注册；安装 GitHub App 不自动给管理员权限 |
| 2 | 后端 Workflow/Execution（共享合同阶段 owner 待编号） | 依[最小语义契约](../wiki/contracts/mvp-cross-end-semantics.md)冻结定义编译、WorkflowRun/NodeRun、TaskSpec/Result、租约、Git 操作证据、ArtifactRef、错误分类与行为版本 | Runner/Worker 和前端 DAG/历史消费冻结合同；消费者不得自改业务状态解释 |
| 3 | 后端 Runner/Provider、Knowledge/Artifact | 本机 Provider/gh 可用性、任务目录与冻结仓库版本物化、知识确认、产物发布和重复报告处理 | Coder/Test/Reviewer 与前端知识/产物页面；远端 OID 而非本机目录作正式代码交接 |
| 4 | 后端 GitHubObservation/HumanTask/Evaluation | `verify_change`、独立 Runner `publish_pr` 的输入输出、PR/head/Review/merge 观察、人工任务、必需验证与关闭证据 | 前端人工待办、PR/质量/关闭视图；GitHub 事实要重新核对，不能仅信 Agent 自报 |
| 5 | 前端 Web（依赖 1—4 的相应合同） | Workspace 配置、WorkItem 创建/详情、DAG/状态、HumanTask、PR/证据、历史/恢复交互 | 不复制后端状态机；明确 Web 发起人、Runner 个人 gh 作者、Agent 来源及外部事实未知 |
| 6 | 跨端真实验收（实施后） | 新 WorkItem/WorkflowRun、知识/代码提交、Task/Artifact、PR、真人 Review/合并与集成验证证据 | 回写各端实际阶段 record；不得用设计期样例或旧运行证明通过 |

建议第 2 行由**后端某一个明确阶段**承担跨端共享合同的首个冻结版本；若后端拆分多个合同 owner，必须按合同逐项指定唯一阶段并写明版本依赖。前端可以先完成信息架构草案，但实现阶段只能消费已冻结的服务端动作与渲染合同。Runner 的 GitHub 外部写入与 Server GitHub 事实观察有不同 producer，最终业务放行仍在控制面。

准确 HTTP 路由、PostgreSQL DDL、Provider 兼容性试验、真实 GitHub 授权/分支规则和 W1—W9 执行证据可以由后续相应阶段产出或验收；在写首批阶段计划前，只需明确它们的 owner、目标行为、前置依赖和退出判据。任何阶段的 record 只能记阶段开始后真实发生的命令、结果和证据。
