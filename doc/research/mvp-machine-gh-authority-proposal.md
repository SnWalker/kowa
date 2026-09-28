# MVP 统一个人 gh 身份的权威勘误拟稿

状态：2026-09-27 已获用户批准并写入 `doc/common.md` / `doc/decision.md` 的审阅记录；以下“拟写入”保留批准时原文，不作为另一份当前权威。依据为[GitHub 权限审计](github-permission-audit.md)与用户 Q39—Q46 的选择；真实隔离尚未验收。

## 需要替换的旧权威

- [common §3.3](../common.md#33-平台与适配器)现写 A/B 分别以本人用户令牌推分支/建 PR、commit/PR 归本人、模型执行不持 GitHub 写凭证；[§2.7](../common.md#27-repositorybranchworktree-与变更集)还写受控服务接收 Coder 变更后完成推送。统一个人 `gh`/Coder 直接推送方案下均需替换。
- [decision 的 GitHub 写操作条目](../decision.md#github-写操作使用对应成员权限人工评审和合并留在-github)选择了 A/B 用户令牌和 Server 受控发布、反对共用 Runner CLI；新方案正好改选该备选，须替换所选/备选/理由/影响，不可追加互相矛盾的第二条当前决定。
- [执行边界](../wiki/architecture/workflow-execution.md)已移除“Runner 一律不能推分支”；[候选工作流样例](schemas/fixed-workflow.example.json)已把旧 `publish_change` 更名为远端 OID 核对节点 `verify_change`，`publish_pr` 是测试/评审后的独立 Runner 能力。仍需在权威批准后同步正式流程与验收。

## 拟写入 common 的有效共识

建议将 §2.7 第二段“已发布代码供工作流下游…”替换为：

> 已发布代码供工作流下游跨机器消费时，以 GitHub 的固定提交为正式引用。Runner 在冻结的仓库身份和基线下物化项目与知识仓；Coder 在获准的项目工作分支上以 Runner 本机 GitHub 身份提交、推送并报告远端提交引用。QA、评审和后续节点按该引用重建输入，不依赖 Coder 本地目录或可变的分支最新值。

建议替换 §3.3 中“首期 Web 使用 GitHub App…”整段为：

> 首期 Web 通过 GitHub App 用户授权登录，Kowa 建立服务端会话并独立判断 Workspace、WorkItem 与人工任务权限。MVP 的 GitHub 远端代码操作由获准 Task 在 Runner 执行面使用该机器预先登录的个人 `gh`/Git 凭证完成；Coding Agent 可以在任务工作区执行本地 Git 和项目仓远端 Git 操作。必需测试及独立评审通过后，独立 `publish_pr` 节点使用同一机器身份创建 PR。GitHub 记录的 push 操作者、commit author/committer 与 PR 创建者是 Runner 机器的个人 GitHub 账号，Web 发起人和 Agent 生成来源由 Kowa 分别记录，不冒称 GitHub 写入属于 Web 发起人。PR 由有权真人且非 PR 作者提交有效 Review，合并仍由人在 GitHub 执行；Kowa 核对当前提交、Review、合并事实及合并后验证。

上位不变量仍是：Server 拥有业务授权、Task/仓库/ref 准入、状态和门禁结论；Runner 只执行获准 Task。`common.md` 的“最小权限”不能解释为宿主个人 `gh` 账号已按 Workspace 或分支硬隔离。若这个物理约束无法通过真实负向验收，需要在 MVP 验收与对外安全表述中明确限制。

## 拟替换 decision 的真实取舍

建议将“GitHub 写操作使用对应成员权限，人工评审和合并留在 GitHub”替换为：

> **MVP 直接使用 Runner 本机个人 gh 身份执行 GitHub 操作。** 选择每台 Runner 预配置可用的 Coding Agent Provider 与个人 `gh`/Git 登录，由获准的执行任务在本机完成项目仓库的 clone/fetch/pull、本地 commit 和工作分支 push；必需测试与独立评审通过后，由独立 `publish_pr` 执行节点使用同一本机 `gh` 创建 PR。Web 使用 GitHub App 登录，但 Web 发起人不自动成为 GitHub 写入身份。commit author/committer、push 和 PR 创建者归 Runner 主机的个人账号，Kowa 单独记录 WorkItem/Run 发起人及 Agent 来源。备选为 Server 用每位 Web 用户的 GitHub App 用户令牌执行发布、每任务委派成员令牌给 Agent、专用机器账号或受控 Git 通道。所选方案能较快验证历史型 Agent 自主 Git 工作流并复用本机工具状态，代价是 GitHub 写入不能按 A/B Web 用户区分，个人账号访问范围和凭证生命周期不受 Kowa 精细控制，同一 macOS 账号下的 Agent 可能访问宿主更多仓库和凭证；独立发布节点也不能凭自身顺序在 GitHub 层阻止 Coder 提前建 PR，MVP 不为此例外提供专项检测/清理。只能在已知可信仓库和明确 GitHub 分支保护、审计与负向验收条件下评价首期能力，不能宣称企业级硬隔离已经实现。

保留已批准的“人到 GitHub 合并、Kowa 以当前 head 的有效 Review 和实际合并/集成验证作门禁”。Review 必须由有权真人在 GitHub 提交，不能是 PR 创建账号；不额外要求 Reviewer 与 Web 发起人不同。正常流程只由独立 `publish_pr` 节点建 PR，Coder 提前创建的可能性不作为 MVP 硬防护已完成来报告。

## 需同步的详细设计与验收

1. Server：Web GitHub 身份、Workspace 授权、Task Git 操作范围、业务状态/证据与 GitHub 事实对账。首期 Server/Runner 同机时，GitHubObservationAdapter 候选使用同机个人 gh 执行受控只读查询并核实活动账号；分机部署须另配 Server 读凭证，不能只信 Runner 自报。
2. Runner：登记机器 `gh` 活动账号、Git remote 协议、Provider 健康与获准仓库/分支；在任务前后记录实际认证身份、基线/结果 OID、命令和退出码。不能把 `gh auth status` 成功当成 `git push` 成功。
3. Agent/Worker：允许本地 Git；仅 Coder 对项目仓库获准工作分支有远端写需求，知识库和验证/评审节点只读。个人账号若实际有更大 GitHub 权限，Kowa 的提示词或 TaskSpec 不构成 GitHub 层硬限制。
4. 验收：用真实新运行区分 Web 发起人、机器 GitHub 账号、Agent 来源与 GitHub Reviewer；验证 pull/rebase 后版本变化、非工作分支 push、知识库写入尝试、force/delete、凭证泄漏、PR 作者和合并事实。若机器个人账号能越过 Kowa 目标边界，应如实记录限制，不把脚本自测当隔离证明。

批准后已将对应正文写入 common/decision，并同步正式 wiki。本文只保存此次提案和批准范围；后续有效设计以权威文件为准。
