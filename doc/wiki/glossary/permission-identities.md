# 权限与 Git 身份术语

状态：active（术语边界；不定义凭证传递方案）。最近核验：2026-09-26。

| 术语 | 短定义 | 不可混同 |
| :--- | :--- | :--- |
| 业务发起人 | 在 Kowa Web 创建或启动某次 WorkflowRun 的已认证成员；运行记录保存其稳定 GitHub 用户 ID | 不等于当前处理 Task 的 Runner 或模型账号 |
| Workspace 授权 | Kowa 控制面决定某成员能否对指定 Workspace、WorkItem、仓库和人工任务执行某项业务操作 | GitHub 登录成功、App 安装和知道对象 ID 都不自动授予它 |
| Runtime 身份 | Kowa 登记的 Runner 执行环境及其领取/报告 Task 的身份 | 不等于 Web 用户或 GitHub 认证操作者，不能自行批准业务门禁 |
| GitHub 认证操作者 | GitHub 在一次远端 fetch/push/API 请求中识别的令牌或 SSH 身份所属者 | 不等于发起该请求的本机进程，也不能从 commit 的姓名文本反推 |
| Git 提交 author / committer | Git commit 对象中记录的作者和提交者元数据 | 不等于远端 push 的认证操作者；只有姓名/邮箱文本不构成签名或授权证明 |
| Provider 账号 | 本机 Claude Code、Codex 等模型服务的登录/计费身份 | 不等于 Kowa 业务发起人，也不授予 GitHub 仓库权限 |

当前具体职责见 [固定流程与执行边界](../architecture/workflow-execution.md)；Agent 远端 Git 凭证与操作界面仍在[权限审计](../../research/github-permission-audit.md)中待决。此处只统一语言，不建立第二套业务规则。
