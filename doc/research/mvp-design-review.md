# MVP 设计审阅与证据目录

整理日期：2026-09-25。本文只管理设计材料与未决问题，不维护开发阶段状态。

## 1. 当前阅读顺序

1. [common](../common.md)：已批准的产品与架构约束。
2. [decision](../decision.md)：已作出的真实取舍。
3. [MVP 流程](../wiki/design/mvp-delivery.md)：已确认的详细业务边界。
4. [固定流程与执行边界](../wiki/architecture/workflow-execution.md)、[执行尝试合同](../wiki/contracts/execution-attempts.md)、[交付生命周期设计](../wiki/domain/delivery-lifecycle.md)：已确认的语义基线。
5. [三篇历史指南核对](historical-guides-assessment.md)、[GitHub 权限与执行边界审计](github-permission-audit.md)、[Workflow/Capability 扩展设计](workflow-capability-design.md)、[契约草案](mvp-contracts-draft.md)、[GitHub 交付草案](github-delivery-design.md)、[身份与信任边界](mvp-identity-security-design.md)。
6. [后端模块与数据设计](../Kowa后端设计/MVP模块与数据设计.md)、[前端信息架构](../Kowa前端设计/MVP信息架构.md)。
7. [验收矩阵](../Kowa验收与运行/MVP验收矩阵.md)。

active 表示已确认设计文档有效，不表示软件已实现。draft/待审材料不作为已冻结接口。首次落盘由用户在确认原有建议后明确解除只读限制授权；本次不创建 S00，不更新执行前沿，不自动提交。

## 2. 设计完成范围

| 主题 | 本次产物成熟度 | 剩余工作 |
| :--- | :--- | :--- |
| MVP 范围、双仓职责、主交付流程 | 已确认并落盘，三篇参考文档已核对 | 维持首期边界，不因历史功能清单扩大范围 |
| 返修、知识建议、关闭与修复政策 | 已确认并落盘 | 映射为确定的节点与验收命令 |
| 状态与轮次身份 | Task 身份及 WorkItem/Run/NodeRun/HumanTask 状态和转换均已确认 | 映射到持久化和 wire，验证跨对象不变量 |
| 知识/HumanTask/Artifact 契约 | 有候选字段与错误语义 | 批准 schema 后进入正式 contracts 索引 |
| GitHub 交付 | 新 MVP 已选 Runner 个人 gh 原生 Git/PR 写入、机器 commit/PR 身份、独立 PR 节点；真人 Review、人在 GitHub 合并；权威文本已勘误 | 阶段内核验个人账号/非目标仓的实际权限风险、Server 对账读身份及真实验证条件 |
| Workflow/Capability 扩展 | 单条固定流程、后续扩展和出站长轮询已批准 | 审阅作者格式、目录准入、编译与 wire 参数 |
| 身份与安全 | 小团队使用、GitHub App Web 登录、服务端会话、首轮同机浏览器、共用本机 Provider CLI 和同一 macOS 账号已确认 | 成员配置细则、模型工具与项目脚本的同账号隔离边界、真实负向验收 |
| 后端与前端设计 | 有各端草案；首条一个 Server＋一个独立 Runner，随后同机第二 Runner 专项已确认 | API/wire、隔离与运行环境的实测 |
| 验收 | 48 项目标断言（A28 + B12 + C8） | 实现后建立红灯与新运行证据；目前未执行 |

## 3. 历史依据与限制

历史项目根为 `/Users/liufei/workspace/feishu_ai_workflow`，只作参考；未修改历史文件，也未全仓递归扫描。以下路径已定向读取。

| 依据 | 可以证明 | 不能证明 |
| :--- | :--- | :--- |
| [实体模型](/Users/liufei/workspace/feishu_ai_workflow/docs/wiki/domain/repo-entities.md) | 文档描述 CurrentWorkflowRunID、NodeID/Iteration 与 Task 分层 | 当前完整实现存在 |
| [demo 表单能力](/Users/liufei/workspace/feishu_ai_workflow/cmd/demo-agent/cap_demo_form.go) | CODE_CONFIRMED：返回 awaiting_input/完成结果，branch 存在性被用作答复判断 | Kowa 人工确认安全、完整控制面流程 |
| [demo 测试](/Users/liufei/workspace/feishu_ai_workflow/cmd/demo-agent/cap_demo_form_test.go) | CODE_CONFIRMED：两条测试源码描述首跑与续跑，同 Task ID | 测试已在当前快照执行通过 |
| [前端流程 YAML](/Users/liufei/workspace/feishu_ai_workflow/internal/infra/workflowdef/builtin/frontend-feature.yaml) | CODE_CONFIRMED：知识绑定、评审回环、人审及弱化门禁配置 | 真实端到端通过；loop_max_iter 的边界次数解释 |
| [产物协议](/Users/liufei/workspace/feishu_ai_workflow/docs/wiki/contracts/artifact-and-progress.md) | 文档区分 progress、output 与 ContentStore | 完整存储源码与运行保证 |
| [数据物化](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/data-materialization.md) | 文档记录跨机失败、版本物化及产物上传模式 | 文档不同演进段落都代表同一时刻现状 |
| [知识版本](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/knowledge-versioning.md) | 文档明确展示记录与实际消费分离的历史迁移取舍 | Kowa 应继续双轨维护 |
| [人审门](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/mr-review-human-gate.md) | 文档有 expectedVersion、返修、PR 复用及 amend 策略 | 该 Git 写入策略适合 Kowa 全部场景 |
| [交互同步](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/web-feishu-interaction-sync.md) | 文档有统一 fields/actions、版本、Web renderer 约束 | Kowa 需要恢复飞书专用回调入口 |
| [Replay 快照](/Users/liufei/workspace/feishu_ai_workflow/docs/design/as-built/workflow-replay.md) | 文档记录不可变引用及 JSONB 摘要注意事项 | 首期必须交付 Replay 操作 |
| [用户提供的总览与两篇扩展指南核对](historical-guides-assessment.md) | 802 行材料完整读取；Makefile、demo、YAML 可交叉核对 | 指南列出的缺失实现已经验证；示例 ID/代码可直接复制 |

已核对该快照根无 go.mod，internal 下没有 domain/app，相关 agent SDK 和服务实现缺失；没有安装依赖或尝试伪造完整构建。历史 Go 测试只读源码，未执行。

## 4. 明确的适配与候选裁决

- 已确认：GitHub 替代内部平台；单业务仓库交付、只读配套知识库；有界自动返修；不自动知识写回或回滚。
- 用户在 Q16—Q18 明确：MVP 只有一条固定流程，后续可复用能力新增流程或以新版本增加 Agent 节点；批准新执行尝试新 Task、重复投递复用原 Task，以及出站 HTTP 长轮询/心跳/报告。已进入 common、decision 和对应正式基线。
- 技术建议：知识内容快照与确认事件分开、显式人答复、单一交付推进权及具体并发/授权/schema。它们由不变量与审阅高度支持（STRONG_INFERENCE），但不等于 wire 已冻结；候选字段留在 research。
- 不继承历史兼容别名、旧 claim/push 双路径、关闭的 QA/Owner 门禁、默认 amend/force 或示例中默认生产环境。

## 5. 设计输入与剩余审阅

| ID | 输入/事项 | 当前处理与准入边界 |
| :--- | :--- | :--- |
| R01 | 用户提供的 3 篇历史文档 | 已收到、完整读取并交叉核对，结果见指南核对页；不再列为等待资料 |
| R02 | Workflow DSL、编译产物与 Runner 出站 wire | 固定流程/扩展、尝试身份和长轮询基线已批准；继续冻结作者格式、目录 schema、消息字段、超时与恢复参数 |
| R03 | 成员角色、审批权限与 GitHub 身份映射 | GitHub App Web 登录与服务端会话已选；成员和授权细则须在真实多人访问前验证 |
| R04 | 发布凭证隔离、分支规则与 CI 来源 | 人在 GitHub 合并，当前 head 需有效真人 Review。新 MVP 由 Runner 个人 gh 写入，Web 发起人另记；账号可能有超出项目/知识仓任务范围的物理权限，Kowa 逻辑限制不等于 GitHub 硬限制。Server 同机 gh 只读对账候选已成稿，真实隔离/CI 来源仍未验收。详见[权限审计](github-permission-audit.md) |
| R05 | 本机 Provider CLI 最小协议与目标环境兼容性 | Runner 应发现并调用 Mac 上已安装、已登录的 Claude Code/Codex 等；后端设计已有结构化输出/上下文/工具/续接/取消/产物/错误验证清单，真实模型验收前须以 Runner 实际身份执行 |
| R06 | 评测阈值、保留期限、资源预算、备份恢复 | 对应质量门禁/实际用户运行前明确；不填假数字 |
| R07 | GitHub 外部人工操作的待办身份 | 用户已确认外部操作型 HumanTask：只作待办提示，由 GitHub 事实自动收束，不要求 Kowa 重复批准。领域状态已生效；候选 schema 的版本失效与证据关系仍须消费者评审 |
| R08 | 代码物化/发布前传输与 GitHub 真源措辞 | 已确认下游交接以 GitHub 固定提交为真源；`common.md` 已按[获批替换稿](mvp-machine-gh-authority-proposal.md)改为 Coder 本机 commit/push。Runner 物化与 OID 验证仍需实施阶段消费者验证 |
| R09 | Agent 远端 Git 操作权限 | [权限审计](github-permission-audit.md)已区分本地 commit 与远端 fetch/pull/push/PR。MVP 选 Runner 本机 Provider+个人 `gh`、Agent 原生 Git/gh，commit/PR 由该账号署名，Web 发起人另记。正常流程由独立发布节点建 PR；有权真人 Review。Coder 提前建 PR 未做专项硬限制；Server 事实读取仍须真实验证 |

本表是设计待办，不是阶段 BLOCKED/HUMAN_ACTION_REQUIRED 状态。业务状态枚举也不等于 AGENTS 定义的开发阶段状态机。

## 6. 本轮一致性修正

- 知识内容摘要不包含确认事件或自身 digest，避免循环引用；同内容确认仍保留独立审计。
- 幂等请求唯一键不包含内容摘要，否则同键不同内容会绕过冲突；定义名/版本与内容摘要作同样区分。
- awaiting_input 的合法报告可以发布供人审阅的候选内容，发布不等于批准，避免确认流程死锁。
- 单模板产品范围与定义/能力扩展能力分开，前端不提前显示多模板入口。
- CODE_CONFIRMED：旧治理门禁将缺失键当 null、空字符串当未开始；已通过 8 个目标负向用例复现并修复。本轮覆盖 currentStage、activeReopen/suspendedReopen 槽位及跨端指针的显式存在性；非 null 前沿只能为 Sxx，不代表全部嵌套历史字段或状态迁移已被机械证明。
- 门禁仍是结构检查，不证明证据真实性、完整状态转换历史或业务实现；48 项业务断言未执行，不能与脚本自测试数量混淆。
