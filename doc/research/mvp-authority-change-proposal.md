# MVP 新共识与决策拟写稿

状态：2026-09-26 曾获批准并应用，**其中 A/B 用户令牌、Server 发布与本人 GitHub 提交/PR 署名的段落已在 2026-09-27 被新选择取代**，见[新获批替换稿](mvp-machine-gh-authority-proposal.md)。本文件只保留历史提案与审核痕迹，任何旧身份段落均不得作为当前共识或实现依据；以 `doc/common.md`、`doc/decision.md` 为准。整理：2026-09-25。

本稿只收录本轮 grilling 中已经回答、且值得提升到权威层的选择。具体执行步骤留在 [MVP 身份设计](mvp-identity-security-design.md)、[GitHub 交付设计](github-delivery-design.md)和[真实验收方案](../Kowa验收与运行/MVP真实集成验收方案.md)。本机共享 Provider CLI 的方向已明确；其 OS 账号与凭证边界、项目脚本隔离仍在审阅，不在这里提前写为定论。

## 拟增补 `doc/common.md`

建议在“首期 MVP 范围”补入：

> 首期面向一个小团队的多人使用。成员以各自 GitHub 用户身份登录，Kowa 的 Workspace 成员资格和处理人工任务的权限由服务端授权，不因安装 GitHub App 或登录成功自动取得管理员权限。

建议在“平台与适配器”补入：

> 首期 Web 使用 GitHub App 的用户授权流程登录，Kowa 建立服务端会话。运行发起人 A 授权 Kowa 以其 GitHub 用户权限受控推送工作分支并自动创建 PR；B 发起的运行使用 B 的权限。提交的 author/committer 与 PR 创建者均为对应运行发起人，Kowa 另存 Agent 生成来源；模型执行与项目测试不持有 GitHub 写凭证。代码合并由人在 GitHub 执行，Kowa 核对适用于当前 PR 提交的有效 GitHub Review、合并事实及合并后验证结果，不要求用户在 Kowa 重复批准同一代码合并。

建议在“首期 MVP 范围”或“平台与适配器”补入：

> 首期 Runner 在本机 Mac 调用该机器已安装、可用的 Claude Code、Codex 等 Provider CLI；团队成员共用这些本机执行能力。Runner 登记实际可用能力与版本，Kowa 为每次任务分别记录发起人和执行证据。后续跨机器运行不能依赖某台 Mac 的个人主目录作为任务输入或交付真源。

建议对“Repository、Branch、Worktree 与变更集”中跨机器代码交接的表述作精确补充，供审核：

> 已发布代码供工作流下游跨机器消费时，以 GitHub 的固定提交为正式引用。受控服务按该引用临时物化执行输入，或接收 Coder 待发布的变更内容以完成受控推送；这类短期传输不取得代码真源地位，也不能成为下游长期依赖的本地目录或未发布 Artifact。

上面只记录当前有效共识，不在 `common.md` 重述候选方案、原因、Mac 端口、证书或实现路径。

## 拟增补 `doc/decision.md`

### MVP 使用 GitHub App 用户授权登录并由 Kowa 管理会话

> 选择 GitHub App 的 OAuth Web 流程识别用户，由 Kowa 服务端管理会话和 Workspace 授权。备选为首期单用户无登录、本地密码账户或把 GitHub App installation 当作用户身份。选择此方案是因为 MVP 面向小团队多人协作，必须区分个人、仓库安装和执行环境的授权；代价是需要回调、会话、CSRF、成员初始化与撤销能力。首条真实验收仅在本机 Mac 的同机浏览器执行，具体回环 HTTPS 地址和证书由环境准备确定。

### GitHub 写操作使用对应成员权限，人工评审和合并留在 GitHub

> 选择由 Kowa 受控发布组件使用当前 WorkflowRun 发起人的 GitHub App 用户令牌推送工作分支并自动创建 PR，提交 author/committer 与 PR 创建者归属该成员；人工 Review 和合并仍由有权用户在 GitHub 执行，Kowa 核对外部事实，不增加第二次 Kowa 合并批准。MVP 运行服务不配置 App 私钥，不签发 installation 写令牌。备选是 App installation 以 bot 身份发布、A 每次手动创建 PR，或使用共享 Runner 的个人 GitHub CLI 登录态。所选方案让 A/B 的写操作按各自 GitHub 权限和审计身份区分，避免 App 令牌绕开某位成员的仓库权限；代价是服务端需要安全保管、刷新并按操作核验每位成员的用户令牌，严格隔离模型/项目脚本，且提交元数据、实际推送身份、PR 创建者和 Agent 来源必须分别对账。

### 可恢复执行失败暂停当前运行，终结失败才结束运行

> 选择可恢复的节点执行失败使当前 WorkflowRun 暂停，由明确 Retry/Rerun 命令继续；`FAILED` 只表示本次运行已经终结。备选是每次节点失败都终结运行并新建 WorkflowRun。所选方案保留一条交付运行的推进权与失败历史，允许相同输入的 Retry 和受控 Rerun；代价是需要明确暂停原因、预算、授权及旧结果隔离。合并后集成验证失败如需改代码，仍在未关闭 WorkItem 下建立关联修复运行与新 PR，旧运行终结后让出推进权。

### GitHub 人工操作使用外部事实收束的 HumanTask

> 选择在等待 GitHub Review 或人工合并时建立外部操作型 HumanTask，向用户提示目标 PR 和适用提交；由 Kowa 核对 GitHub 事实后自动收束，不接受第二次 Kowa 批准点击。备选是在运行投影中仅显示系统观察状态、不建立人工待办。所选方案使真实外部人工操作进入统一待办和审计入口；代价是必须区分待办解决与业务门禁通过，并在 PR head 变化时使旧待办失效，不能让提醒状态替代 GitHub Review 或合并事实。

上述四条已在用户批准后进入 `decision.md`。一个 Server 与一个独立 Runner、再用同机第二 Runner 验证交接的方案已有单 Runner 决策基线，详细验收顺序留在验收文档，不重复新增决策条目。

## 仍需专项设计与验证

1. MVP 已选择 Server、Runner 和共享本机 Provider 使用同一个 macOS 账号。模型工具与项目构建/测试脚本在该账号下可访问哪些文件、网络、个人 `gh` 登录态及其他任务目录，必须实测；不能把“同机已登录”当作已隔离。
2. 用户令牌/OAuth client secret 与共享模型 CLI 的隔离、密钥托管和失效处置仍需实现方案与真实负向验收。MVP 服务不配置 App 私钥也须核验。
3. 已确认的[交付生命周期设计](../wiki/domain/delivery-lifecycle.md)仍需映射为正式持久化和 wire；代码物化/发布前短期传输按 `common.md` 新增的 GitHub 真源边界实施。
