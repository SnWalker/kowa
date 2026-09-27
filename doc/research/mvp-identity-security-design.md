# MVP 身份、权限与执行信任边界

状态：设计期方案；GitHub App 登录、小团队多人使用、本机 Provider 和 Runner 个人 `gh` 执行身份已确认。Q44/Q46 已收口，真实隔离尚待验证；旧 A/B 用户令牌写入方案已按[专项审计](github-permission-audit.md)及用户批准完成权威替换。更新：2026-09-27。

本文将 [common](../common.md) 的最小权限、人工授权、显式上下文要求转成可评审边界。MVP 登录选择 GitHub App 的 OAuth Web 流程；它不等于 App installation 授权。GitHub 平台事实引用 [交付设计](github-delivery-design.md)，执行身份语义引用 [执行尝试合同](../wiki/contracts/execution-attempts.md)。

## 1. 三种身份不可互相冒充

| 身份 | 证明什么 | 不能证明什么 |
| :--- | :--- | :--- |
| 登录用户 | 谁在提出命令/处理人工任务 | 不能仅凭登录就访问任意 Workspace |
| GitHub App installation | 应用能访问哪些仓库及平台权限 | 不等于当前用户身份，也不自动授予 Kowa 管理权限 |
| Runtime/Runner | 哪个已登记执行环境在报告 | 不等于用户批准，不允许任意改业务状态 |

用户的 GitHub 关联保存稳定外部用户身份，不以可改用户名作为唯一标识。注册 Runner、安装 App、创建 Workspace 与分配成员权限是不同的授权动作，不能用一个成功回调自动完成全部授权。

首个管理员不能由“第一个访问网页的人”自动取得。设计为由部署操作者事先登记获准的 GitHub 用户 ID，完成本人 OAuth 登录后才能建立首个 Workspace 管理身份；登记动作需审计且不可被 GitHub App 安装回调自动触发。首期不建设本地密码系统。

## 2. GitHub 登录与服务端会话

1. 用户选择 GitHub App 登录，服务端生成单次 `state` 与 PKCE S256 参数，并把流程绑定到短期服务端记录；回调须匹配预登记的固定 URL 和 `state`。失败、超时或重复 code 不建立会话。[GitHub App Web flow](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
2. 服务端交换 code，读取 GitHub 稳定用户 ID。用户名只用于显示，浏览器不持有用户访问令牌。Web 登录只建立 Kowa 发起人身份和会话，不把 A/B 的用户令牌交给 Runner 作远端 Git 写入；MVP Git 写入由 Runner 预配置的个人 `gh` 账号执行。Server 是否需要保留用户令牌用于独立读取 GitHub 事实，仍待对账凭证方案确定。
3. 服务端签发随机的不透明会话 ID，持久化会话摘要、GitHub 用户 ID、建立/到期时间和撤销状态。浏览器仅持有 `__Host-` 前缀、`Secure`、`HttpOnly`、`Path=/` 的 cookie；跨站 OAuth 返回所需的短期 state cookie 与业务会话分别管理。[OWASP 会话建议](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
4. 使用 cookie 会话的写请求校验 CSRF 令牌、来源和会话。`SameSite` 是额外保护，不能代替 CSRF 校验。[OWASP CSRF 建议](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
5. 登出、管理员撤销或权限变更使会话/授权缓存失效；高风险命令再次读取当前 Workspace 权限与对象版本，不能只信登录瞬间的角色快照。

运行发起人 A 在启动时固定为认证的 GitHub 用户 ID，Server 仍须核实 A 的 Kowa Workspace/WorkItem 操作权限；这不等于 A 拥有目标仓库的 GitHub 写权限。Runner 领取任务时登记本机 `gh` 活动账号和目标仓库可用性；远端 Git/PR 实际使用该个人账号，A/B 只是 Kowa 内的发起人/人工处理人。若 `gh` 登录失效、项目仓不可达或目标分支被 GitHub 规则拒绝，应保留失败并停止，不能切换另一台 Runner 或另一个账号制造通过。当前一个 macOS 账号下的凭证保管和真实隔离尚未验收。[gh 活动账号](https://cli.github.com/manual/gh_auth_status)、[GitHub App Web 登录](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)

会话的空闲/绝对时限、OAuth 单次 code/state 清理、如需对账而保留用户令牌时的期限及密钥轮换参数在部署合同中固定。首轮验收只允许在承载 Kowa 的同一台 Mac 上打开浏览器；本机回环 HTTPS 回调、浏览器信任的本地证书和 GitHub App 中登记的精确回调地址是环境前置，当前尚未配置。不同团队成员须以各自 GitHub 身份分别登录，不借用同一浏览器会话。

## 3. 小团队 Workspace 授权

候选角色只提供授权组合，不取代逐资源判断：

| 能力 | Workspace 管理者 | 研发操作者 | 只读成员 | 额外条件 |
| :--- | :--- | :--- | :--- | :--- |
| 配置成员/仓库/Runner/策略 | 可 | 不可 | 不可 | 不能扩大 GitHub 实际安装权限 |
| 创建工作项、发起运行 | 可 | 可 | 不可 | 仓库与执行资源在范围内 |
| 查看状态和普通产物 | 可 | 可 | 可 | 产物归属和敏感等级允许 |
| 处理人工任务 | 按任务授权 | 按任务授权 | 不可 | 必须匹配处理人/角色、请求和对象版本 |
| Retry/Rerun/取消 | 按对象授权 | 按对象授权 | 不可 | 当前运行与副作用处置允许 |
| 请求业务工作分支/PR 交付 | 按操作授权 | 按操作授权 | 不可 | Server 授权派发；Coder/独立发布节点使用 Runner 本机个人 `gh` 账号，Web 发起人另记；外部过宽权限不可假称已被 TaskSpec 硬限制 |
| 执行 PR 合并 | 不由 Kowa 代执行 | 不由 Kowa 代执行 | 不可 | 人在 GitHub 操作，Kowa 核实有效 Review 与合并事实 |

不自动要求不同的人完成所有 Kowa 审批；如需职责分离，应作为明确策略配置。GitHub 当前提交上的有效 Review 是代码合并批准证据，不在 Kowa 再点击一次。Agent 的独立评审要求始终存在，不能因为同一人操作 MVP 就取消上下文和证据隔离。

所有命令检查：登录/执行身份 → Workspace 成员或执行授权 → 对象属于该范围 → 操作权限 → 当前版本/状态 → 高风险授权。不能因为能猜到 artifact_id、human_task_id 或知道 URL 就可读取或修改。

业务输入和门禁策略的冻结不冻结永久权限。命令接受、任务派发及写凭证签发时检查当前授权；撤销权限不篡改已发生的历史事实。当前权限只能收紧或阻止后续操作，不能静默降低冻结的业务门禁。

## 4. Runner 与凭证

登记绑定明确环境身份与授权范围，凭证轮换/撤销后不得仅凭 runtime_id 恢复访问。Runner 需要自己的 Kowa Runtime 凭证，用于出站注册/领取、心跳、获取已分配 Task 的冻结输入、上传该 Task 的产物和报告；Server 按 Runtime、派发世代、租约、Task 与 Workspace 归属逐项校验。该凭证本身不授予 GitHub 仓库权限、Web 用户身份或业务门禁决定权，也不传给 Worker/Provider。若 Task 需要远端 Git 操作，必须有另外的成员/仓库/ref/操作授权路径；具体委派协议与凭证边界待审。

MVP 的 Runner 运行在本机 Mac，探测并调用这台机器已经安装、可用且已登录的 Claude Code、Codex 等 Provider CLI；多个 Kowa 成员共用该本机执行能力。Runner 登记需要区分“命令存在”“版本兼容”“当前认证可用”“具体 Capability 可执行”，不可由一个进程存活标志替代。Kowa 记录每次任务的 Workspace、WorkItem、发起人、实际 Provider/版本与费用归属；Provider 平台侧若显示同一个本机账号，不能把它误称为各成员自己的模型身份。MVP 的 Server/Runner 共用一个 macOS 账号；CLI 登录态的保管、任务工具可见范围和撤销办法仍需环境设计与实测。

MVP 运行服务不配置 GitHub App 私钥或 installation 写令牌；GitHub App OAuth client secret 留在 Server。Runner 预配置个人 `gh`/Git 登录并供 Agent 的任务环境直接使用，因此模型 shell 可能调用 `gh auth token` 或访问宿主 Git 凭证；这些权限不能再被描述为“Agent 没有 GitHub 写凭证”。Kowa 可在 TaskSpec 中声明目标仓/分支与禁止动作，并在接受结果时核对，但个人账号本身若有更广权限，单一 macOS 账号下的提示词/进程分离不构成硬拒绝。Q46 已确认 MVP 不为 Coder 提前建 PR 增设专项检测、自动阻断或清理；正常流程仍只在独立 `publish_pr` 节点建 PR，且不得把该节点顺序描述成 GitHub 层硬权限。实际隔离仍须有真实负向证据。

知识正文、仓库文件和模型输出都是内容输入，不能授权工具、扩大仓库范围或撤销门禁。模型要求“为修复问题推送 main”不构成授权。

冻结业务输入可以引用资源身份与访问需求，但不嵌入长效 token。短效访问凭证的更新不改变业务输入；授予的资源或操作范围变化必须重新授权。

租约/fencing 保护控制面结果接受，不等于 GitHub 凭证立即失效或子进程已经停止。存在未决副作用时先对账，不能靠创建新 Task 避开风险。

## 5. Web 与审计

浏览器展示服务端允许动作；写请求必须重新鉴权和校验版本，隐藏按钮不是安全边界。token/秘密不放在 URL、`localStorage` 或普通浏览器可读配置中。OAuth 临时代码不得进入持久业务日志，服务端会话与授权动作分开审计。

审计保存操作者、授权依据/版本、命令目标、期望版本、有效结果与关联证据；认证身份由服务端产生，不相信请求体里的 actor。日志缺失不能被补成“已批准”，审计写入失败时不能先确认高风险操作已受理。

跨 Workspace 访问、已撤销权限、过期人工答复、来源不明 Runner、模拟证据冒充真实和内容指令越权，均需独立负向验证。

## 6. 本机 Mac 的身份边界

Runner 作为独立进程出站连接控制面。历史项目也采用进程分离，但其本机 Git/CLI 登录态与宿主身份共用，并未证明仓库测试可安全访问多人数据。独立进程与任务沙箱是两项要求，不能互相替代。[历史身份问题](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/identity-permission.md)

这里的“任务执行子进程”具体包括：Worker 调用本机 Claude Code、Codex 等 Coding Agent/模型工具的进程，以及针对目标项目仓库执行构建、测试和脚本的进程。后者是项目代码的验证，不是 Kowa 自身的单元测试；它可能执行仓库提供的任意脚本，因此与模型工具一样需要明确的文件、凭证和网络边界。Runner 负责监管这些执行，独立 Runner 进程本身不提供上述边界。“每 Task 容器化”需要在容器内另行提供 CLI 与认证，不能由 Mac 宿主已经安装和登录推导出来；不作为当前 MVP 的模型 Provider 运行前提。

小团队共享这台 Mac 时，Web 发起人、Runner Runtime、共享模型账号与 Runner 个人 GitHub `gh` 账号在 Kowa 审计中分别记录。OAuth client secret 仍应留在 Server；Agent 使用同一 macOS 账号运行原生 git/gh，实际上可接触宿主 GitHub 登录态。它可能因此访问 Kowa 目标仓以外的仓库，项目脚本也可能读取凭证、会话存储或其他任务目录。工作目录与 Provider 工具规则只能缩小正常路径，不能在未实测沙箱的情况下宣称硬隔离。项目构建/测试脚本可另行评估受限容器，但该选择不改变本机 Provider 前提。现有 Docker Desktop 安装或 `sandbox-exec` 文件存在都不是隔离已通过的证据。

`DATA_CONFIRMED`（2026-09-26 当前交互环境）：Mac 用户 `liufei` 的 `gh auth status` 成功。用户选择 MVP 的 Server、Runner 与本机 Provider 共用一个 macOS 账号，并让 Agent 直接使用该个人 `gh` 登录。它能支持单一 GitHub 写入身份，却不能实现 A/B Web 用户分别在 GitHub 写入，也不能让 Server 的 TaskSpec 硬限制该个人账号原本拥有的远端权限。MVP 需用真实任务和假凭证验证同账号进程的文件、工具与网络可见范围；在隔离证据形成前，不宣称多人执行具有硬权限边界。

本机实测需要用两个团队 GitHub 账号、两个 Workspace、受保护的宿主测试文件与假凭证，以及一个会尝试越界读取/写入的仓库任务，分别验证文件、网络、进程和发布凭证边界。任何一项未证实，不能宣称多人共享执行安全。

## 7. 批准与验证出口

当前用户选择：GitHub App OAuth Web 登录、服务端会话和小团队 Workspace 成员仍用于 Kowa 业务身份；MVP 的远端 Git 写入由 Runner 上的 Agent/独立 PR Capability 使用本机个人 `gh` 账号执行，commit author/committer 与 PR 作者为该个人账号，Web 发起人另记。首轮仅同机浏览器，Server/Runner 共用一个 macOS 账号。人到 GitHub Review/合并、Kowa 核对事实的门禁保留；Q44 已确认 Reviewer 必须是有权真人且不能是 PR 创建账号，Q46 已确认不为 Coder 提前建 PR 增设专项处理。上述选择已按获批替换稿写入 `common.md`/`decision.md`；具体同账号隔离、Server 事实读取身份及真实负向证据仍须在实施阶段验证。本草案不代表部署已安全可用。

验收应同时使用允许与拒绝样例，记录对象归属、身份与策略版本。相关目标进入 [验收矩阵](../Kowa验收与运行/MVP验收矩阵.md)，不得仅用管理员账户正常路径证明权限正确。
