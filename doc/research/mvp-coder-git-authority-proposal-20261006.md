# MVP Coder 远端 Git 权威变更提案（2026-10-06）

状态：**待用户批准**。批准后按本稿写入 `doc/common.md` 与 `doc/decision.md`，并同步第 5 节列出的下游文档措辞；**未批准前不构成当前权威**。依据为 `doc/research/s04-prerisk-spike-20261002.md`（S04 前置风险验证 spike，PR #15）§5 与 §11 C1 的 2026-10-02 实测。

## 1. 依据（DATA_CONFIRMED）

- Codex 默认 `workspace-write` 沙箱既不能写 `.git`（无法 commit）也不能联网；放宽到可交付的两种配置（`danger-full-access`，或 `writable_roots=[.git]` + `network_access=true`）都会让任务进程可读取宿主 SSH 私钥与 Codex `auth.json`，并经 Keychain 取得 gh 令牌。
- 环境变量原样透传，Provider 不按 `TOKEN/KEY` 命名过滤；"把 Coder 关进沙箱"不能同时满足"最小权限"与"Agent 自行 push"。
- 已冻结的 `kowa.workflow-execution.v1` 已把仓库/ref/操作范围建模为 Runner 侧授权边界（`gitScopes`），并不要求 Agent 自己执行远端操作。

因此"Agent 直接持有宿主 Git/gh 凭证并自行发起远端写"与 `doc/common.md` §4"默认最小权限"存在结构性冲突。

## 2. 所选方案（原 spike 报告选项 ③）

**Coder 只在任务工作区读写文件并运行本地命令；仓库物化与全部远端 Git/gh 操作（clone/fetch/pull/commit/push、`publish_pr`）由 Runner 受控通道在获准 Task 的 `gitScopes` 范围内，以 Runner 本机个人 `gh`/Git 身份执行。**

- 保留 2026-09-27 决策的核心：GitHub 写入身份是 Runner 主机的个人账号，commit author/committer 与 PR 创建者归该账号，Web 发起人另记。
- 改变的是执行主体：由"Agent 原生 git/gh"改为"Runner 受控通道代执行"；本地 commit 也在 Agent 交付工作区后由 Runner 执行，Agent 因此不需要 `.git` 写权限与到 GitHub 的网络。
- 未选：① `danger-full-access`、② 放宽 `workspace-write` 的 `.git` 与网络（两者让 Agent 直接持有宿主凭证，与 §4 冲突）；④ 仅用 Claude 作为 Coder（同样持有宿主凭证，且放弃多 Provider 统一协议目标）。
- 与已冻结 wire 一致：不改 `kowa.workflow-execution.v1` 的任何字段或语义。

## 3. 拟写入 `doc/common.md` 的替换文本

### §2.7 第二段（部分替换）

原：`Runner 在冻结的仓库身份和基线下物化项目与知识仓；Coder 在获准的项目工作分支上以 Runner 本机 GitHub 身份提交、推送并报告远端提交引用。`

新：`Runner 在冻结的仓库身份和基线下物化项目与知识仓；Coder 只在任务工作区读写文件并运行本地命令，其变更由 Runner 受控通道在获准的项目工作分支上以本机 GitHub 身份提交、推送并报告远端提交引用。`

### §3.3 第三段（替换首句）

原：`MVP 的 GitHub 远端代码操作由获准 Task 在 Runner 执行面使用该机器预先登录的个人 \`gh\`/Git 凭证完成；Coding Agent 可以在任务工作区执行本地 Git 和项目仓远端 Git 操作。`

新：`MVP 的 GitHub 远端代码操作由 Runner 受控通道在获准 Task 的仓库/ref/操作范围内，使用该机器预先登录的个人 \`gh\`/Git 凭证完成；Coding Agent 只在任务工作区读写文件并运行本地命令，不直接持有远端 Git/gh 凭证，也不自行发起远端操作。`

§3.3 的其余句子不变。

## 4. 拟写入 `doc/decision.md` 的替换条目

标题不变：`## MVP 直接使用 Runner 本机个人 gh 身份执行 GitHub 操作`

替换正文：

> 选择每台 Runner 预配置可用的 Coding Agent Provider 与个人 `gh`/Git 登录；Coder 只在任务工作区读写文件并运行本地命令，项目仓库的 clone/fetch/pull、commit、工作分支 push 由 Runner 受控通道在获准 Task 的 `gitScopes` 范围内代执行；必需测试与独立评审通过后，由独立 `publish_pr` 执行节点使用同一本机 `gh` 创建 PR。Web 使用 GitHub App 登录，但 Web 发起人不自动成为 GitHub 写入身份。commit author/committer、push 和 PR 创建者归 Runner 主机的个人账号，Kowa 单独记录 WorkItem/Run 发起人及 Agent 来源。备选为：Agent 直接持宿主 git/gh 凭证（2026-09-27 原选择）、Server 用每位 Web 用户令牌发布、专用机器账号。2026-10-02 实测（S04 前置风险 spike §5）表明 Agent 直接执行需要放宽沙箱，任务进程可读取宿主 SSH 私钥并经 Keychain 取得 gh 令牌，与"默认最小权限"冲突，故改为 Runner 受控通道代执行。所选方案下 GitHub 写入仍不能按 A/B Web 用户区分，个人账号访问范围和凭证生命周期不受 Kowa 精细控制；独立发布节点也不能凭自身顺序在 GitHub 层阻止越界写入，MVP 不为该例外提供专项检测/清理。只能在已知可信仓库和明确 GitHub 分支保护、审计与负向验收条件下评价首期能力，不能宣称企业级硬隔离已经实现。

## 5. 批准后需同步的文档（只改措辞，不改业务语义）

- `doc/wiki/architecture/workflow-execution.md`（Coder 权限句）
- `doc/wiki/design/mvp-delivery.md`（流程责任表与 Coder 段）
- `doc/wiki/contracts/mvp-cross-end-semantics.md`（版本与身份不变量）
- `doc/Kowa后端设计/MVP模块与数据设计.md`（§3 事务与写入段）
- `doc/Kowa验收与运行/MVP真实集成验收方案.md`（W1 步骤 5、W6a/W6c）
- `doc/Kowa验收与运行/测试仓库约定.md`（沙箱操作主体措辞）
- `doc/Kowa前端设计/MVP信息架构.md`（PR 区域与 Agent 来源措辞）

## 6. 影响与未决

- 本变更只改权威文本；"Runner 受控 Git 通道"的具体实现（命令面、错误语义、证据采集、与 `gitScopes` 的逐项对应）属 S04 阶段合同，由该阶段按目标红灯冻结。
- 沙箱分支保护（C2）、Server 只读读凭证（C4）、第二个真人账号（C5）仍待用户操作，与本提案独立。
