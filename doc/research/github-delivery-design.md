# GitHub 交付与事件对账设计

状态：设计期候选；MVP 身份与交付边界已由 [common](../common.md)、[decision](../decision.md) 和[正式交付流程](../wiki/design/mvp-delivery.md)确认，接口及真实权限尚未实现或验收。更新：2026-09-27。

## 身份与操作位置

Web 用户通过 GitHub App 登录 Kowa；Server 判断 Workspace、WorkItem、HumanTask、Task 与仓库/ref 的业务授权。Runner 使用本机预配置的个人 `gh`/Git 身份执行 GitHub 操作。GitHub 的实际写入 actor、commit author/committer 和 PR 创建者是该机器个人账号，不是 Web 发起人；Kowa 分别保存发起人、机器身份、Agent 来源与实际外部对象。GitHub App 安装与 Web 登录不自动赋予 Kowa Workspace 权限，也不替代 Runner 的 Git 凭证。

| 操作 | MVP 执行者 | Kowa 接受条件 | 外部硬边界 |
| :--- | :--- | :--- | :--- |
| 项目与知识仓 clone/fetch/pull | Runner/获准 Task | 仓库 ID、冻结 OID、知识仓只读意图 | 机器账号实际仓库权限 |
| 项目仓本地 commit、工作分支 push | Coder Task | 工作分支、基线、远端 OID 与证据匹配；`verify_change` 独立核对 | GitHub 仓库/分支规则；TaskSpec 不缩减个人账号权限 |
| PR 创建 | 独立 Runner `publish_pr` 能力 | 必需测试与独立评审有效，源/目标仓和 head 版本一致 | 机器账号的 PR 权限及 GitHub 规则 |
| PR Review、合并 | 有权真人在 GitHub | Review 适用于当前 head 且 Reviewer 不是 PR 作者；人工合并事实已观察 | GitHub 自身授权与仓库保护 |
| PR/Review/CI/merge 读取 | Server GitHubObservationAdapter | 对稳定对象 ID、版本和观察水位对账；失败保持未知 | 首轮同机个人 `gh` 只读查询候选；分机后另配 Server 读凭证 |

Server 与 Runner 首轮同一 Mac、同一 macOS 账号。Server 借用同机个人 `gh` 做只读平台事实对账是部署候选，不是跨机凭证设计；必须核验当前活动账号，并把读失败或身份漂移表示为 `EXTERNAL_UNKNOWN`。不能接受 Runner/Agent 自报的 Review、PR 作者或合并结论作为平台事实。未来分机时，GitHubObservationPort 的输入输出保持不变，Server 读取凭证另行配置。

机器个人账号可能实际拥有非目标仓、知识仓写入或其他分支权限。Kowa 对 Task 的准入和拒收只能保护自身状态，不能撤销已经发生的外部 push/PR。MVP 只在可信仓库和明确分支规则下评价；负向测试应记录真实 GitHub 接受或拒绝的结果，并如实披露未实现硬隔离。Coder 提前自行调用 `gh pr create` 不设专项检测或清理；正常流程仍只在 `publish_pr` 节点创建。

## 版本、对账与失败

项目仓与知识仓以稳定 repository ID 和精确提交 OID 标识。Coder 推送后，`verify_change` 检查远端仓库、ref、head OID 和预期基线；QA、代码评审和安全评审从该 OID 物化各自工作区。新 push、pull/rebase 或变更验证配置后，受影响的测试、评审与 GitHub Review 均需按版本适用性重判，不能沿用旧的“通过”。知识仓运行期只读，知识更新建议只形成 Artifact。

PR 创建、push 或查询超时为结果未知，先按仓库、源/目标 ref、head OID、PR ID 与操作记录对账再决定是否重试；不能盲目重复产生第二个有效外部对象。取消或租约失效阻止 Kowa 继续接受结果，但不能撤销已经发生的 GitHub 写入。重要外部操作保存请求身份、期望版本、观察时间、实际返回与证据 ID；日志不得暴露本机令牌。

首轮仅同机浏览器访问，GitHub Webhook 不作为必需入口。Server 主动查询 PR、Review、合并和 CI，持久化观察水位；读失败、限流或权限消失时不把旧观察冒充最新事实。若 GitHub 已合并而没有适用于当时 head 的有效 Review，保留合并事实但不自动关闭 WorkItem。集成验证固定实际合并结果，而非目标分支可变 HEAD。合并后验证失败时按正式交付流程保留原 WorkItem，必要时由人确认关联修复运行和新 PR。

## 待实施阶段验证

- `gh auth status` 的活动账号、Git remote 协议、Git credential helper/SSH、真实 fetch/push/PR 权限须分别核验；`gh` 已登录不证明 Git push 可用。
- 真实 Provider 与项目脚本可读的宿主文件、环境、网络和其他任务目录须做负向验证；不能从进程分离推断安全隔离。
- PR Review 的版本、权限与撤回规则、CI 来源、分支保护、force/delete 拒绝、签名要求、机器账号失效和切换后的处置须按测试仓配置验证。
- 精确 REST/CLI 调用、轮询退避、错误码、Webhook 后续方案由后端阶段冻结；本文件不宣称接口或真实验收已经通过。
