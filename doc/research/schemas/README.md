# MVP 候选 schema 与示例

本目录保存设计期的机器可读候选资料，不是已批准的 Kowa wire 或运行时实现。

- [mvp-v1.schema.json](mvp-v1.schema.json)：候选消息形状和类型；标注 Draft 2020-12，但尚未完成完整标准校验、消费侧验收或协议发布。HumanTask 已区分 Kowa 内答复型与 GitHub 外部操作型；字段和跨对象验证仍待消费者审阅。
- [fixed-workflow.example.json](fixed-workflow.example.json)：单条工作流的合成示意。Coder 在 Runner 上使用本机 Git/gh 提交并推送工作分支，`verify_change` 核对远端提交；必需测试/独立评审后由独立 Runner `publish_pr` 能力使用本机 gh 建 PR，GitHub Review/合并仍由人完成并由 Server 对账。节点类型、能力 ID、输出字段和图编译仍须与后续正式契约核对；节点顺序不等于对宿主 gh 凭证的硬限制。

本机共享 Provider CLI 的选择/实际版本证据还未进入候选 `TaskSpec` 的机器字段；[最小契约草案](../mvp-contracts-draft.md)先记录语义，待唯一 owner 与 Runner 消费者一起冻结。不得把缺失字段解释为允许从宿主环境随意选用任意账号。

[GitHub 权限审计](../github-permission-audit.md)发现 Agent 主动 fetch/pull/push 的授权消息尚未进入候选 JSON Schema；本样例只表达“直接本机 gh 的统一身份”和发布顺序，不证明仓库/ref 权限被硬隔离。`agent` 节点类别暂表示 Runner 执行 Capability，包含确定性的 `github.pr_publish`；最终 NodeType 命名仍需合同评审。

原先设计期生成的批量合成正反例及离线校验脚本已移除；它们不能证明业务行为或真实集成。正式契约必须说明 owner、producer、consumer、版本、错误语义，并由前后端和 Runner 的真实消费者评审后进入 `doc/wiki/contracts/`。
