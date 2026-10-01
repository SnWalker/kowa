# Kowa MVP 真实集成验收方案

性质：设计期的可复跑方案；尚无服务、Runner 或真实运行，所有场景均未执行。制定：2026-09-25。

目标行为以 [MVP 流程](../wiki/design/mvp-delivery.md)和[验收矩阵](MVP验收矩阵.md)为准。本方案负责环境、顺序、人工动作与证据水位，不维护阶段状态或新业务真源。首轮开发与真实验收均在本机 Mac，Web 只供同机浏览器使用；一个 Server 与一个独立 Runner 共用一个 macOS 账号完成首条闭环，Runner 可监管多个 Worker/Agent 进程。模型 Provider 使用本机已安装、已登录的 Claude Code/Codex 等 CLI；项目构建/测试脚本的隔离方式与本机 CLI 权限边界分别核验。同账号不等于凭证隔离已通过。

## 1. 准入前置

| 前置 | 必须准备与核对的事实 |
| :--- | :--- |
| 工程 | Kowa 控制面、Web、独立 Runner、真实能力和迁移均已按正式阶段实现；版本与 HEAD 可定位 |
| 流程 | 一条获准的固定 WorkflowDefinition、Capability/Prompt/结果 schema 版本及冻结摘要 |
| 基础设施 | PostgreSQL、Artifact Store、Runner 运行目录、评测后端及其必要依赖可用；备份/恢复口径明确 |
| 本机 Provider/Git | Runner 实际发现 Coding Agent CLI、个人 gh 活动账号、Git remote 协议、Git 凭证路径与目标项目仓 clone/fetch/push、PR 能力；不能由 `gh auth status` 推断 git push 已可用 |
| 本机边界 | Mac 上的模型工具预期能使用该机器个人 gh 身份；OAuth client secret、其他任务目录与非目标仓库的可见/可写范围须实测并如实记录。单 macOS 账号、TaskSpec 或提示词不构成硬隔离已通过 |
| 身份 | GitHub App 用于 A/B Web 登录；Runner 使用本机个人 gh 账号写项目仓，commit author/committer 与 PR 作者为该机器账号；Web 发起人另记。有权真人在 GitHub Review，不能是 PR 作者 |
| 浏览器 | 精确 GitHub OAuth 回调地址、回环监听、受信任本地 HTTPS 证书、独立会话和 CSRF 验证可用 |
| GitHub | 工作分支/PR 授权、Review 与保护规则、人工合并权限、CI/集成验证来源可核对 |
| 任务 | 有权限的小型功能或缺陷修复，包含明确验收标准和已知的仓库验证命令；不得使用历史任务伪造新运行 |

截至 2026-10-01，仓库已有 `go.mod`、Go 控制面的部分模块与迁移（`db/migrations` 000001–000003）、Web 工程（`package.json`、`src`）和 CI 工作流；Runner 目前只有进程骨架，尚无可用的 Provider 执行、固定 WorkflowDefinition 或真实 WorkflowRun，也没有部署配置。以下环境观察为 2026-09-26 的观察，非当前验证：当时在交互 shell 中可找到 `codex-cli 0.154.0`、`gh 2.100.0` 且 `gh` 已登录，`claude` 不在 PATH；当时没有验证真实 Agent/Git remote 调用，也未配置或验证 Git credential helper。Docker Desktop 当时已安装但服务不可用；它不是调用宿主模型 CLI 的前置条件。个人 gh 登录可作为所选机器 GitHub 执行身份，但不能替代 Web GitHub App 登录授权。上述前置条件目前**不满足**，不得进入真实 WorkflowRun 操作。

## 2. 操作前冻结水位

每次运行前记录：Kowa HEAD/构建版本、数据库迁移版本、WorkflowDefinition 名称/版本/digest、Capability/Provider/Prompt 版本、Workspace 配置版本、Web 发起人 ID、Runner 个人 gh 身份与 Git remote 协议、项目/知识仓库 ID 与提交、Runner ID/运行环境及沙箱配置、Artifact Store 命名空间、CI 规则、预期产物种类和数量。

任何既有测试数据先标明来源，不得使用旧的 WorkflowRun、Task、Artifact、PR 或结果作为新代码的通过证据。敏感令牌、私钥、会话 cookie 不写入方案、日志、截图或 record。

## 3. 正常闭环 W1

1. **用户操作**：安装并授权 GitHub App、选择测试仓库、完成必要分支规则与 Review 配置。Kowa 恢复后只读核对安装、仓库与权限。
2. **用户操作**：两个团队成员在同机浏览器分别登录，管理员建立 Workspace 与权限，登记可写项目仓和只读知识仓。分别验证无权成员无法改变配置或读取另一 Workspace 资源。
3. **用户 A 操作**：创建新的 WorkItem，从唯一固定 DAG 起点输入需求并触发真实 WorkflowRun、真实 Runner 派发。此类触发遵守 AGENTS 的人工边界；需返回实际工作项和运行 ID，并固定运行发起人的 GitHub 用户 ID 为 A。
4. 核对知识推荐、读取、用户增删与确认记录；冻结知识仓提交与条目摘要。确认之前下游不能消费，知识失败不能显示为“无相关知识”。
5. 核对方案、独立评审、必要返修与人工批准的版本链。Coder 在 Runner 任务工作区使用本机 Git/gh clone/fetch/pull、commit 并推送项目仓工作分支；提交 author/committer 与实际 push 身份为机器个人账号，Web 发起人/Agent 来源另记。`verify_change` 核对远端仓库/分支/OID，测试、独立代码/安全评审从该 OID 在各自目录读取代码。pull/rebase 或新 push 使被测版本变化时重判下游证据适用性。
6. 让上游 Runner 执行目录不再作为下游读取来源；在新的隔离执行目录/进程消费已发布产物，核对 digest、知识快照和 GitHub 可获取提交。具体停机或目录移除动作只在隔离测试数据上、按阶段合同执行。
7. 必需测试和独立评审通过后，独立 Runner `publish_pr` 能力使用本机 gh 创建 PR。核对 PR 作者、远端 commit author/committer 与 Runner 机器个人账号一致，Web 发起人 A 和 Agent 执行另记。由有权真人在 GitHub 提交针对当前 head 的有效 Review，且 Reviewer 不是 PR 创建账号；Kowa 不要求第二次合并批准点击。
8. **用户操作**：在 GitHub 合并 PR 并返回 PR URL/number。Kowa 主动只读对账 Review、实际合并方式和 merge OID；不得相信页面按钮或用户口头反馈就是合并事实。
9. 对实际合并结果运行集成验证，保存验证配置、任务和报告。控制面核验必需证据并关闭 WorkItem；知识建议或离线评测失败单独显示和重试，不反向改变交付状态。

Web 只显示控制面核定的动作和状态。GitHub 首轮通过主动轮询/对账更新；网络失败时显示上次成功观察时间，不以未接到 Webhook 解释为未发生合并。

## 4. 受控异常场景

异常用新的隔离 WorkItem/WorkflowRun 执行，不在 W1 里修改数据制造通过。

| 运行 | 触发 | 必需结论 |
| :--- | :--- | :--- |
| W2 | 知识条目缺失或来源拒绝访问 | 明确阻断，保留失败与恢复入口；不能伪装成空知识 |
| W3 | Reviewer 给出 needs_revision，连续触及三次自动修订上限 | 第四次不能自动通过，转有效 HumanTask；失败历史保留 |
| W4 | Runner 重启、租约过期、重复报告/迟到报告 | 新旧 Task/输入关联正确，不重复执行或覆盖当前结果；未知外部副作用待对账 |
| W5 | 产物仅留上游本地或 digest 不符 | 下游拒收，NodeRun 不假成功；修复后使用新产物 |
| W6 | PR head 更新后旧 Review 留存；或 GitHub 已合并但无有效 Review | Kowa 不沿用旧批准、不关闭；如实保留已发生的 GitHub 事实 |
| W6a | Runner 机器 gh 登录失效、Git remote 无凭证或个人账号无项目仓/PR 权限 | 对应 fetch/push/PR 能力失败并保留原因；不改用 Web A/B 或 App 身份制造通过；已有远端事实与未知副作用先对账 |
| W6b | A/B 依次发起各自运行，在同一 Mac/Runner 上共用模型 CLI/gh | GitHub commit/push/PR 均显示同一机器个人账号，Kowa 准确区分 A/B Web 发起人与 Agent 来源，不宣称 GitHub 写入按 Web 用户分离 |
| W6c | Agent 在任务内尝试知识仓 push、非本任务分支、保护分支或 force push | 实测 GitHub 平台规则和个人账号实际权限；若远端已发生越界写入，如实记为 MVP 权限限制或失败，不能用 Kowa TaskSpec/提示词声称硬拦截；pull 改变基线后旧测试/评审证据失效 |
| W7 | PR 合并后集成验证失败 | WorkItem 保持 OPEN；若需改代码，经人工确认关联修复运行和新 PR |
| W8 | 取消与发布/合并并发、同键不同请求 | 保留外部事实，拒绝幂等冲突，不以取消表示已回滚 |
| W9 | 在 W1 完成后于同机启动第二个 Runner，用新的运行触发重新派发 | 不读第一个 Runner 的工作目录；通过 Artifact Store/GitHub 引用获取输入，旧租约不能写回，证据单独记录 |

权限与隔离另需跨用户/跨 Workspace 的负向执行：测试 Agent 是否能读取宿主个人 gh 凭证、其他任务目录、OAuth client secret、会话存储或调用非目标仓库的远端写操作。个人 gh 凭证供 Agent 使用是已选 MVP 设计，不能把“能读取该凭证”本身记为意外；应记录实际可访问范围和外传风险。假凭证和目标仓库必须是测试隔离数据，不能用真实敏感令牌做越界试验。

## 5. 验收记录与判定

每个 W1—W9 保存新证据 ID、预期/实际行为、命令与退出码、测试数量、被测提交及知识版本、Task/NodeRun/Artifact/PR 身份、数据库/日志查询水位、用户真实操作及操作后只读核对。各端阶段 record 记录实际执行；本方案不写假结果。W9 是跨 Runner 专项，不能代替 W1 的业务闭环，也不能把同机试验宣称为跨机器故障恢复已通过。

- `PASS`：实际新运行满足对应 [验收矩阵](MVP验收矩阵.md)目标，身份、版本、内容摘要和外部事实可对账。
- `BLOCKING_FAIL`：Kowa producer/transition/consumer 违反权威合同，或必需门禁缺证据。
- `OBSERVATION_ONLY`：确定性合同与证据合法，模型或外部质量波动不违反本次交付要求；不得借此放过规定的同步质量、安全和集成验证失败。

真实操作之前，AI 完成允许的自动检查、记录前水位、给出当次精确清单；用户操作后 AI 只读验收。没有真实运行时只能评价设计与准备度，不得把本方案或候选 JSON 样例记为集成通过。
