# GitHub 权限与控制/执行边界审计

状态：设计审计，非当前正式契约。初稿 2026-09-26，结论更新 2026-09-27。Kowa 仍为 pre-S00，无 Server/Runner/Worker 实现或真实 GitHub 运行。第 1—4 节保留**选择前的历史问题快照**，其中 A/B 用户令牌和 Server 独占发布已被新方案取代，不可作为当前 Kowa 设计引用；当前方案以 [common](../common.md)、[decision](../decision.md)、[正式执行边界](../wiki/architecture/workflow-execution.md)及本文件第 5—6 节为准。

本审计追溯旧方案的冲突：旧设计把 GitHub 读取、推分支和建 PR 全放在 Server，Runner/Worker 完全没有 GitHub 能力；但 MVP 需要本机 Claude Code/Codex 在编码过程中操作 Git 仓库。业务授权真源与命令执行位置不能混为一谈。用户已批准替换稿，权威文件已勘误。

## 1. 证据和事实等级

| 证据 | 结论与限制 |
| :--- | :--- |
| [当前共识](../common.md#33-平台与适配器)、[已作决策](../decision.md#github-写操作使用对应成员权限人工评审和合并留在-github) | `CONFIRMED`：Web 用户 A/B 的 GitHub 权限和提交/PR 身份须分别归属本人；控制面持有业务状态和授权决策。它们没有证明 Agent 的远端 Git 能力可用 |
| [执行边界](../wiki/architecture/workflow-execution.md)、[身份草案](mvp-identity-security-design.md)、[固定流程样例](schemas/fixed-workflow.example.json) | `CONFIRMED`（文档现状）：Runner 仅有 Kowa Runtime 凭证；`publish_change`/`publish_pr` 是 Server system 节点，Agent 不能主动执行远端 fetch/push/PR。与用户指出的编码工作流需求不自洽 |
| 当前 Mac 的 `codex --help`/`codex exec --help` | `DATA_CONFIRMED`：本机 Codex CLI 可运行模型生成的 shell 命令，有沙箱和审批选项；这不证明在 Kowa Runner 下网络/GitHub 权限可用。当前 shell 找不到 `claude`，未验证其实际安装/登录 |
| 当前 Mac 的 `gh auth status` | `DATA_CONFIRMED`：当前 macOS 用户有宿主 `gh` 登录态；Server、Runner 共用该 OS 账号时，进程分离本身不能防止 Agent 绕过 Kowa 身份路由 |
| [GitHub App 用户令牌](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)、[Git 访问权限](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app)、[Git 凭证机制](https://git-scm.com/docs/gitcredentials)、[gh 环境](https://cli.github.com/manual/gh_help_environment) | `CONFIRMED`（平台文档）：用户令牌权限是用户与 App 可访问范围的交集；HTTP Git 可用用户令牌；Git 凭证 helper/askpass 与 `GH_TOKEN` 能向命令提供令牌。把令牌交给 Agent shell，就不能声称 Agent 看不到令牌 |
| [历史工作流 YAML](/Users/liufei/workspace/feishu_ai_workflow/internal/infra/workflowdef/builtin/frontend-feature.yaml)、[历史身份文档](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/identity-permission.md)、[历史 MR 门禁](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/mr-review-human-gate.md) | `CODE_CONFIRMED` 仅限现存 YAML 将编码与 MR 创建分成执行侧能力；历史文档称执行机本机 SSH/key/CLI 完成 clone/push/MR，并记录 Web 用户与工具账号串号。相应执行源码缺失，不能当作已验证实现或 Kowa 直接基线 |

## 2. 必须拆开的四种身份

1. **业务发起人**：Web 上启动 WorkflowRun 的 A；Server 冻结稳定 GitHub user ID，并判断 Workspace 与 WorkItem 操作权限。
2. **执行环境**：登记的 Runtime/Runner；其 Kowa 凭证只证明“哪个环境在领取/报告 Task”，不证明是 A，也不是 GitHub 用户令牌。
3. **GitHub 认证操作者**：实际用于 fetch/push/API 的 A 用户令牌，或 GitHub 直接操作的 B Review/merge；必须与已授权的业务动作对应。
4. **Git 提交元数据**：author/committer 字段与 GitHub 认证推送者分开。已批准 A 的运行由 A 作为 author/committer，Kowa 另存 Agent 生成来源；字段文字不能单独证明是谁认证推送。

## 3. 操作逐项审计

控制面与执行面的责任先按业务语义划分；统一机器账号的具体配置与剩余凭证边界待 Q43/Q44 及后续设计冻结：

| 参与者 | 应拥有的决定/能力 | 不应自行拥有的决定 |
| :--- | :--- | :--- |
| Web 用户 A/B | 登录、提交需求、处理授权的 HumanTask；在 GitHub 以本人身份 Review/合并 | 不能仅凭登录绕过 Workspace 和工作流门禁 |
| Server 控制面 | 认证会话、Workspace/仓库/角色策略、运行/节点状态、Git 操作准入、授权人、目标仓/分支/OID、人工门禁、结果接受、审计与外部事实对账；保管或代理 A/B 授权 | 不执行模型生成、项目构建/测试；不能把 Runner 报告直接当成 GitHub 事实 |
| Runtime/Runner | 用 Kowa Runtime 身份领取明确 Task、准备/监管工作区与本机 Provider、执行允许的 Git/工具能力、报告结果及证据；按 Server 发放的任务边界做本地限制 | 不自行给任务增加仓库、用户、ref 或 GitHub 权限，不批准方案/Review/合并，不关闭 WorkItem |
| Agent Worker | 在该节点的冻结上下文中读写本地仓库、运行所授权工具；Coder 可本地 commit，远端操作范围须由 Task 合同定义 | 不因拥有 shell 就继承宿主个人 `gh`/SSH 身份；不能凭提示词扩大仓库/分支/权限 |
| GitHub | 持有仓库访问、ref、commit、PR、Review/合并和保护规则的远端事实 | 不知道 Kowa WorkItem 是否通过业务门禁 |

MVP 节点权限还需按职责而非 Provider 名称分配：Knowledge/Planner/Reviewer/QA 只读冻结代码或知识，允许各自在任务目录写临时输出；Coder 可改项目工作区、本地 commit，并在获准的工作分支上请求远端写操作；PR/Review/merge 分别按已确认的人机门禁处理。即使同一 Codex CLI 被用于多个角色，不能沿用上一角色的 GitHub 授权或工作目录。

| 操作 | GitHub 认证需求 | 当前设计 | MVP 必须明确的权限/执行边界 |
| :--- | :--- | :--- | :--- |
| 登录、Workspace 配置、启动运行 | Web GitHub 用户授权；Kowa 成员授权 | Server 已有候选方案 | Server 决定 A 可访问哪两个仓、可否启动；App 安装不等于成员身份 |
| 克隆/拉取项目与知识仓固定版本 | 私有仓远端读取需 A 与 App 的 Contents read；本地读取无需新 GitHub 调用 | Server 物化快照给 Runner | Agent 若要主动 fetch/pull，需定义执行侧受控读取；知识仓始终只读，目标提交/OID 明确 |
| `git status/diff/add/commit` | 本地操作，不需 GitHub 令牌；需可写工作区和身份元数据 | Coder 产出变更 Artifact，未明确可否保留本地 commit | 允许 Agent 本地 Git 操作；提交 author/committer 为 A，服务端发布前校验 OID、父提交、变更范围和 Agent 来源 |
| `git fetch` | 远端读取需认证 | Agent 无此能力 | 允许读取哪一仓/哪一 ref，是否可使用最新 HEAD，须按 Task 冻结输入判断 |
| `git pull`/rebase | 远端读取后还会改变本地基线 | Agent 无此能力 | 不能静默改变已冻结基线；若获得新基线须重算下游验证/审批适用性 |
| `git push`/更新工作分支 | 远端 Contents write；GitHub 仓库/分支规则仍独立生效 | Server system 节点独占 | 用户要求 Agent 能发起此操作；需限制仓库、工作分支、期望旧 OID、force/删除策略及未知结果对账。不能授予知识仓写入或默认推保护分支 |
| 创建/更新 PR | Pull requests write | Server system 节点自动创建 | 已确认 PR 归属运行发起人 A，不能退回 App/B 身份；Agent 是否可主动发起 `gh`/PR 操作待明确，业务放行仍由 Server 判断 |
| Review/合并 | B 在 GitHub Review，人在 GitHub 合并 | Kowa 只观察并建外部 HumanTask | Agent 不代 Review/merge；Server 核对实际 head、reviewer、合并事实 |
| CI/检查状态 | 读取实际来源所需权限 | Server 主动对账 | 不让 Agent 自报“通过”代替平台/验证证据 |

GitHub 用户令牌不是“只能执行某一条 `git push`”的任务票据。GitHub 可用仓库、成员与 App 权限交集约束它；分支/引用还需要仓库 ruleset/保护规则和 Kowa 自己的操作检查。[GitHub 用户令牌说明](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)、[仓库规则](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets)

## 4. 现设计的问题

1. **功能缺口**：Agent 可以编辑本地文件，但不能按工作流需要主动 fetch/pull/push；把 `publish_change` 放在 Server 只覆盖“编码结束后的一次发布”，不能替代编码过程中的 Git 能力。
2. **权限颗粒度缺口**：候选 TaskSpec 没有仓库角色、可用 Git 操作、目标 ref、期望 OID、授权人、时效、失败/对账语义。Runtime 凭证不能自行变成 A 的 GitHub 权限。
3. **凭证泄漏风险**：若直接把 A 的用户令牌放进 Agent 环境、Git remote URL、credential helper 或 `GH_TOKEN`，模型 shell 能读取或复用；当前同 macOS 账号的个人 `gh` 登录也可能绕过 Kowa 路由。清理 `PATH` 或只写提示词不构成硬隔离。[OpenAI Sandbox security](https://developers.openai.com/api/docs/guides/agents-api/environments/security)
4. **身份/版本风险**：Agent 本地提交若被 Server 重写，commit OID 会变化，旧测试/评审证据不可直接沿用；Agent pull/rebase 或 push 后继续写代码，也会使前序版本适用性发生变化。
5. **文档权威冲突**：`common.md`/`decision.md` 当前写成“模型执行不持 GitHub 写凭证、受控发布组件按用户令牌推送”，这只是一个具体方案，不等于用户新增的“Agent 需要远端 Git 能力”已解决。正式更改前需审核精确新共识与决策文本。
6. **Provider 行为尚未验收**：本机 Codex CLI 有沙箱/审批选项；Claude Code 的工具允许/拒绝与非交互权限模式也会影响 shell 命令。即使 Kowa 提供了 GitHub 能力，仍要在实际 Runner 身份下验证 Provider 能否按合同主动调用，而不能由 CLI 已安装推断 git/gh 网络操作会成功。[Codex 沙箱安全](https://developers.openai.com/api/docs/guides/agents-api/environments/security)、[Claude Code CLI 参考](https://docs.anthropic.com/en/docs/claude-code/cli-usage)

## 5. 方案比较与建议

| 方案 | Agent 能做什么 | 凭证/权限影响 | 评价 |
| :--- | :--- | :--- | :--- |
| 现行 Server 终点发布 | 本地编辑/提交，结束后交变更 Artifact | 用户令牌留 Server | 无法满足 Agent 中途远端 Git 操作；须修正 |
| Agent 直接持 A/B 用户令牌 | 原生 `git`/`gh` 任意调用 | 令牌暴露给模型 shell；可访问用户与 App 共同授权的范围，branch 限制主要靠 GitHub 规则 | 实现最直观；同机多用户与不可信仓库下风险高，不能仅凭短有效期称安全 |
| 每台 Runner 预配置一个持久 `gh` 账号 | Agent 使用宿主本地 `gh` 与相应 Git 凭证 | 同机所有任务默认以该活动 GitHub 账号执行；`gh auth switch` 改共享配置，可能与并发任务竞争 | 单人可信试点可用；不满足已确认的 A/B 成员归属，不推荐作为小团队 MVP 的最终授权模型 |
| **Agent 发起受控 Git 操作** | 本地 `git` 自由；远端 fetch/pull/push、PR 通过任务工具/Runner 受控通道发起 | Server 保管用户令牌并作业务授权；Runner 只得任务级操作权，不拿原始长期令牌；GitHub 侧按 A/B 用户令牌执行 | 推荐进一步设计。功能上让 Agent 主动操作，代价是 Provider 工具接入与受控 Git 通道需验证；不能假称原生 `git push` 已透明可用 |
| 每用户独立 Runner/OS 登录 | 原生 `git`/`gh` 使用各自宿主登录 | 凭证和工作区按 OS 账号分离 | 历史资料有类似思路；与用户已选“一个 macOS 账号”冲突，MVP 不采用 |

受控通道不是一次性 Server 自动发布：Agent 可在允许的 Task 阶段主动提出明确的 Git 操作；Server 按冻结运行/仓库/分支/成员/版本检查，Runner 完成或转发受限执行并返回结构化结果。具体是原生命令代理、Provider 工具还是 Kowa 命令，需要先确定 Agent 对原生 CLI 的要求；绝不通过返回原始用户令牌给模型来假装“受控”。

### 每台 Runner 预配置 Provider + `gh` 的统一机器账号试点

`DATA_CONFIRMED`：当前 Mac 有 `gh 2.100.0`、可用的宿主 `gh` 登录和 Codex CLI；当前 shell 找不到 `claude`。`git config --global --get credential.helper` 未返回配置，因此不能从 `gh` 已登录推断 `git push` 已能使用该凭证。GitHub CLI 提供 `gh auth setup-git` 为 Git 配置 credential helper；尚未执行该写配置命令或任何远端 Git 操作。[gh auth setup-git](https://cli.github.com/manual/gh_auth_setup-git)

该提案在**一个可信用户、一台 Runner、一个 GitHub 身份**的功能冒烟测试中可行；还需实测 Provider 的 shell/网络权限、Git remote 协议、clone/fetch/push、PR 创建及提交署名。对已确认的“小团队多人共用 Mac/Runner，A 的 commit/PR 属于 A，B 属于 B”则不充分：`gh` 对每个 GitHub host 使用活动账号，`gh auth switch` 会变更共享配置。为每个 Task 设置 `GH_TOKEN`、隔离 `GH_CONFIG_DIR` 和 Git credential helper 可以按进程选择 A/B，但此时是**向 Agent 任务提供 A/B 令牌**，不再是单个机器账号；任务 shell 可调用 `gh auth token` 或读取环境/辅助程序结果，同 macOS 账号也无法靠单独工作目录防止查看宿主其他凭证。[gh 环境](https://cli.github.com/manual/gh_help_environment)、[gh auth token](https://cli.github.com/manual/gh_auth_token)、[Git 凭证机制](https://git-scm.com/docs/gitcredentials)

此外，`gh` 登录成功不自动保证 Git 使用同一身份：HTTPS Git 取决于 credential helper/askpass，SSH Git 取决于 SSH key 和 agent。Git commit 的 author/committer 文本与远端 push/PR 的认证身份仍需分开核对。若工作流允许同一 Runner 执行多个成员任务，不能靠切换全局 `gh` 活动账号来保证并发正确性。

用户已选择把统一机器账号作为**MVP 交付身份规则**，而非只作正式 MVP 前探针。执行侧使用本机预配置的 Provider 与 `gh`，Agent 可以直接调用原生 Git/gh；Server 仍负责 Kowa 工作项/任务授权、状态和事实对账，但 GitHub 实际认证操作者是 Runner 主机的活动个人 `gh`/Git 身份（Q43 已选沿用个人账号，不另建 Kowa 机器用户）。Q42 已确认：commit author/committer 与 PR 创建者均为该个人 GitHub 账号；Web 发起人 A 与 Agent 来源由 Kowa 独立记录。此前“A 的运行使用 A 用户令牌、commit/PR 归 A”的决定已由用户批准的新方案取代并完成权威勘误。

沿用个人 `gh` 账号意味着该账号的仓库访问范围、离职/撤权、令牌轮换和 GitHub 审计都与个人绑定；它不是 Kowa 可独立撤销的服务身份。若个人账号可访问其他仓库或能写知识库，Agent 直接运行原生 `gh`/Git 时，Kowa 的 Workspace/仓库角色只是应用内规则，不能从根本上限制该宿主凭证。MVP 可在已知、可信仓库上验证功能，但不能把这一路径评价为企业级最小权限或跨用户隔离已完成。[GitHub 对长期集成与 PAT 的建议](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/deciding-when-to-build-a-github-app)

为避免未来多个 Runner 的个人 gh 账号导致同一交付运行作者漂移，候选不变量是在首次**可远端写的 Task 派发前**冻结 WorkflowRun 的 `git_execution_user_id`；后续 Coder/`publish_pr` 写任务只能派给报告同一 GitHub 用户 ID 的 Runner。执行前后还须复核本机活动 gh 账号。若账号被切换、撤销或新 Runner 身份不匹配，停止新远端写入并对账已发生事实；不能静默改用另一账号继续原 PR。同机第二 Runner 共享账号可验证重派，但不能据此声称不同机器/不同账号都可无条件接力。具体迁移/修复方案可作为后续多机阶段合同，不在 MVP 伪造通过。

统一机器写入身份没有消除控制面“亲自核实 GitHub 事实”的需求。首期 Server/Runner 在同一 Mac、同一 macOS 账号，候选部署选择 Server GitHubObservationAdapter 使用该个人 `gh` 的受控只读 API 查询 PR、Review、merge OID 和 CI，查询前核实活动 GitHub user ID 与 Workspace 登记的 Runner 身份一致；读失败/账号漂移保持外部结果未知，不用 Runner 自报替代。该方式满足首期同机功能，但状态恢复依赖 Server 所在机器的 gh 配置；未来 Server/Runner 分机时，Server 必须有独立可刷新的读取凭证或可信 GitHub 对账端口。GitHubObservationPort 的输入输出保持仓库/PR/OID/观察水位，不把本机 gh 配置写进业务真源。[gh 活动账号](https://cli.github.com/manual/gh_auth_status)、[gh API](https://cli.github.com/manual/gh_api)

## 6. 待决前沿与验收

- Q39/Q41：用户已选择 MVP 由 Agent 直接使用 Runner 本机 `gh`/Git 登录态，统一机器账号取代首期 A/B GitHub 写入归属；未来如果重新要求 A/B 归属，需另定每任务身份或受控 Git 通道。
- Q42：用户已确认 commit author/committer 与 PR 创建者均用 Runner 机器 GitHub 账号，Kowa 分别保存 Web 发起人和 Agent 来源。
- Q43：用户已选沿用当前个人 `gh` 账号；个人权限范围和撤销无法由 Kowa 独立治理，须在 MVP 风险与验收中明示。
- Q44：用户要求有权真人在 GitHub Review，Agent 自动评审不替代；Reviewer 不能是 PR 创建账号。没有额外要求其必须不同于 Web 发起人。
- Q45：用户已选独立 `publish_pr` 节点，在必需验证和独立评审后由 Runner 使用本机 `gh` 创建 PR。Coder 在代码节点可用同一原始 `gh` 凭证，因此该节点顺序本身不能硬阻止提前创建 PR。
- Q46：用户认为 Coder 一般不会提前建 PR，MVP 不为该例外设计专项检测、自动阻断或清理。正常流程仍只在独立 `publish_pr` 节点创建 PR；不能由此声称 GitHub 层已硬禁止 Coder 提前创建。
- 控制面首期以同机个人 gh 只读查询的候选方案已成稿；实际 GitHubObservationPort 与将来分机后的读凭证仍需合同与真实验收。每个 Runner 换机后机器身份变化也须明确处理。
- 独立 Runner `publish_pr` 已确定；Coder 的正常可写目标是项目仓工作分支，fetch/pull 后须按新 OID 重判下游证据。同 macOS 账号的凭证与任务目录隔离仍需真实负向验收。GitHub 人工 Review/合并门禁保持有效。
- 真实验收至少覆盖 A/B 发起但机器 GitHub 写入身份相同、知识仓写入尝试、`git push` 到非本任务分支/受保护分支、force/删分支尝试、机器 gh 失效或切换、旧 Task 报告拒收、push 超时先对账、Agent 访问 Server 凭证或其他任务目录的尝试，以及提交/PR 作者与实际认证操作者对账。外部 GitHub 若接受越界操作，应报告部署限制，不谎称 Kowa 已硬拒绝。

本文件是设计审计，不代表任何方案已实现、安全验证通过或正式阶段已开始。
