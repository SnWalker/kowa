# Kowa MVP 开发前设计任务清单

性质：设计期任务导航，不是后端或前端的开发阶段、状态表或施工合同。建立：2026-09-25。权威产品边界见 [common](../common.md)；已批准方案见 [decision](../decision.md)。任务是否可以收口，以本表所引的正文和证据为准，不从勾选状态反推 `currentStage`。

## 目标与顺序

首期交付一条固定工作流：Workspace 配置项目仓库与只读知识库，创建 WorkItem，在 DAG 起点输入需求，完成知识绑定、方案和评审、人工门禁、编码、验证、PR、人工合并及合并后集成验证，最终关闭。首期只写一个业务项目仓库。

| 顺序 | 设计任务 | 已有产物 | 开发前完成标准 |
| :--- | :--- | :--- | :--- |
| 1 | 对账共识与历史证据 | [历史指南核对](historical-guides-assessment.md)、[设计审阅目录](mvp-design-review.md)、[已批准拟写记录](mvp-authority-change-proposal.md) | 错误历史断言已隔离；当前事实、已确认约束和待审技术方案各有入口 |
| 2 | 冻结 MVP 场景、业务门禁和关闭 | [交付流程](../wiki/design/mvp-delivery.md)、[验收矩阵](../Kowa验收与运行/MVP验收矩阵.md) | 成功、失败、人工返修、取消、合并后验证失败均有唯一判定 |
| 3 | 明确领域身份、轮次和状态 | [生命周期设计](../wiki/domain/delivery-lifecycle.md)、[执行尝试合同](../wiki/contracts/execution-attempts.md) | WorkItem、Run、NodeRun、Task、HumanTask 的转换与重新运行关系经用户确认；持久化映射由任务 4 继续完成 |
| 4 | 完成跨端契约和版本语义 | [候选 schema](schemas/mvp-v1.schema.json)、[候选契约说明](mvp-contracts-draft.md)、[扩展设计](workflow-capability-design.md) | 每个共享契约有 owner、producer、consumer、版本、失败语义和相互匹配的样例；获准后进入 wiki/contracts |
| 5 | 完成身份、授权、隔离与 GitHub 交付 | [GitHub 权限审计](github-permission-audit.md)、[身份草案](mvp-identity-security-design.md)、[GitHub 交付草案](github-delivery-design.md) | 多人 Workspace、Agent 远端 Git、Runner/Server 授权与凭证路径各有明确边界；外部结果未知可对账 |
| 6 | 完成前后端、评测、非功能及验收准备 | [后端设计](../Kowa后端设计/MVP模块与数据设计.md)、[前端设计](../Kowa前端设计/MVP信息架构.md)、[质量与观测设计](../wiki/design/mvp-quality-observability.md)、[验收矩阵](../Kowa验收与运行/MVP验收矩阵.md)、[真实验收方案](../Kowa验收与运行/MVP真实集成验收方案.md) | 首期页面与状态一致；同步质量门禁和离线评测分开；真实验收所需环境、版本、水位与人工操作明确 |

任务 1—3 的产品与领域结论已确认；任务 3 的持久化映射由后续后端阶段落实。任务 4 的[跨端最小语义契约](../wiki/contracts/mvp-cross-end-semantics.md)已建立，精确 [JSON schema 与工作流样例](schemas/README.md)仍是候选 wire。任务 5 已选每机本地 Provider+个人 `gh` 账号、Agent 原生 Git/gh、机器 commit/PR 身份和独立 `publish_pr`；有权真人 Review，Coder 提前 PR 不作专项硬防护。Server 同机个人 gh 只读对账为首期候选，真实隔离仍待实施阶段验证；前后端与验收草案已按机器身份同步。这个描述是证据导航，不拥有执行期状态。

## 与开发阶段的边界

上述设计完成后，另行决定首个前后端阶段合同、跨端契约 owner 与验证命令。在阶段权威建立前，不写生产代码。设计期可以审阅 schema 和文档，不能把样例检查称为 Kowa 实际运行通过。

真实端到端集成验证要等服务、Runner、Web、GitHub App 测试授权、项目/知识测试仓库以及实际工作流存在后，用新的 WorkItem/WorkflowRun/Artifact/PR 执行。它是后续实现验收，不是可以在目前仅有文档的仓库中提前“完成”的设计任务。开发前的任务是把验收步骤与判据写到可复跑程度。

## 待进一步确认的产品选择

当前已确认小团队多人使用、GitHub App Web 登录，首条真实运行在本机 Mac 且只供同机浏览器访问。首条闭环为一个 Server、一个独立 Runner；随后同机第二 Runner 专项验证交接。Runner 调用本机 Provider，Server/Runner 共用一个 macOS 账号。Agent 原生 Git/gh 与 Runner 当前个人 `gh` 登录是 MVP GitHub 写入路径，commit author/committer 和 PR 创建者均归该个人账号；Web 发起人另行记录。common/decision 已按此勘误。人在 GitHub 完成 Review/合并，Kowa 仍需核对外部事实；同账号下模型/项目脚本的实际凭证边界尚无验收证据。

## 建立后端/前端首批阶段前的准入清单

本表只列创建首批 `stage/Sxx-*.md` 与阶段状态行**之前**必须确定的设计输入；阶段 `record/Sxx.md` 不能预填尚未发生的执行证据。完成这些设计不表示工程已实现或真实验收已通过。

| 顺序 | 必须完成的任务 | 当前状态 | 阶段规划准入证据 |
| :--- | :--- | :--- | :--- |
| 1 | 收口机器 `gh` 身份下的人审与越界副作用规则 | 已完成设计：真人 Review、独立 `publish_pr`、提前 PR 不作专项处理；非目标分支/知识仓写入作为逻辑拒收与真实负向验收风险 | [common](../common.md)、[decision](../decision.md)、[交付流程](../wiki/design/mvp-delivery.md)与[语义契约](../wiki/contracts/mvp-cross-end-semantics.md)一致；未宣称 GitHub 硬隔离 |
| 2 | 冻结控制/执行面的 GitHub 授权与观察边界 | 已完成首期设计：Server 业务授权与平台事实核对、Runner 个人 gh 执行；同机 Server 只读 gh 查询为候选适配器，分机凭证属于后续设计 | [执行边界](../wiki/architecture/workflow-execution.md)、[GitHub 交付设计](github-delivery-design.md)明确身份、凭证来源、对账和未知结果；真实可用性待阶段验证 |
| 3 | 勘误权威共识与真实决策 | 已完成：[准确替换稿](mvp-machine-gh-authority-proposal.md)获批，`common.md`/`decision.md` 已替换旧 A/B 本人令牌与 Server 终点发布 | 两份权威和正式 wiki 只保留当前机器身份方案 |
| 4 | 冻结固定流程的 Git/PR 节点责任与失败路径 | 已完成业务语义：[交付流程](../wiki/design/mvp-delivery.md)和[语义契约](../wiki/contracts/mvp-cross-end-semantics.md)包含 Coder push、`verify_change`、独立 `publish_pr`、版本重判与外部结果未知 | 候选 JSON 的精确 NodeType/Capability 与错误字段由后端 owner 阶段和 Runner/Web 消费者共同冻结 |
| 5 | 指定跨端最小契约唯一 owner 与消费版本 | 已完成语义 owner：[正式跨端契约](../wiki/contracts/mvp-cross-end-semantics.md)给出模块 owner、Producer/Consumer、行为版本边界与拒绝类别；候选 JSON 尚未正式发布 | 后端首批合同阶段获唯一阶段 owner，前端显式依赖其冻结版本；精确 HTTP 路由、DDL 和字段为阶段产物 |
| 6 | 对齐前端信息架构与 MVP 验收目标 | 已完成设计口径：前端草案、正式流程、验收矩阵和真实操作方案区分 Web 发起人、机器 actor、真人 Review 和外部未知 | 真实页面/接口与 W1—W9 在实施阶段验证，不把设计目标当测试通过 |
| 7 | 完成阶段入口审计与阶段依赖图 | [跨端依赖地图](mvp-stage-dependency-map.md)已给目标和 owner 顺序；两端仍 `currentStage=null` | 创建实际 Sxx 前再次核对 HEAD、预存工作区、文档索引、门禁，并在阶段文件写必读/允许修改/验证/人工出口；record 只记录之后真实发生的工作 |

以下事项**不阻止编写阶段计划**，但必须被对应阶段设为实现或退出验收条件：本机安装/登录 Claude Code 与 `gh auth setup-git`、真实 GitHub App/仓库授权、实际 Task 沙箱与凭证负向测试、PostgreSQL 迁移、Provider 协议测试、W1—W9 新运行和 PR 合并后验证。当前任何一项均不能记为已通过。
