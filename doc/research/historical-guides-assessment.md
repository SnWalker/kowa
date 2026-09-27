# 三篇历史指南的核对与采用边界

核验日期：2026-09-25。性质：历史材料评估，不是 Kowa 实现或冻结契约。

## 1. 来源与核验方法

用户说明三篇材料由 AI 分析历史项目总结，大部分可信、少量需甄别。完整读取共 802 行：

- [总览，446 行](/Users/liufei/workspace/feishu_ai_workflow/docs/wiki/architecture/koda-feishu-wiki-overview.md)。
- [新增 Workflow 指南，165 行](/Users/liufei/workspace/feishu_ai_workflow/docs/guides/how-to-add-a-workflow.md)。
- [新增 Agent 指南，191 行](/Users/liufei/workspace/feishu_ai_workflow/docs/guides/how-to-add-an-agent.md)。

只核对材料明确引用的关键路径、现存两个 builtin YAML、demo-agent 与 Makefile，以及已有架构/协议页。不递归扫描历史仓库，不修改历史文件，不运行缺少依赖的历史工程。

证据级别的对象必须说明：CODE_CONFIRMED 表示当前保留代码/配置直接可见；历史文档叙述没有代码闭环时只作为设计依据。多个 AI 文档相互重复不构成独立实现验证。

## 2. 可以采用的主线

| 机制 | 依据与等级 | Kowa 用法 |
| :--- | :--- | :--- |
| 声明式定义与冻结运行 | 指南、现存 YAML、Kowa common 相互支持：CONFIRMED（设计方向） | 以版本化定义描述流程，运行不随文件变化 |
| Parse/Compile/运行准入分层 | 指南的设计叙述；实现文件缺失：STRONG_INFERENCE | 分别验证格式、依赖/数据流、执行能力与权限 |
| depends_on 与 inputs_from 分开 | CODE_CONFIRMED：builtin YAML 直接使用二者 | 控制依赖不能代替数据绑定，数据引用须来自合法上游 |
| Capability 是执行单元 | CODE_CONFIRMED：demo 的 ID/Descriptor/Execute | 新能力优先接统一运行器，不按名称另建调度器 |
| 运行器统一接入与报告 | CODE_CONFIRMED：demo main 调用 agenthost.New 并传 Caps；Host 包缺失 | 可参考调用边界，不声称完整 Host 行为已核验 |
| 非阻塞人工交互 | CODE_CONFIRMED：demo 返回 awaiting_input + InputRequest | 控制面持久化等待，人工答复后再执行 |
| 结构化输出与产物引用 | 指南、产物协议及 demo 输出：CONFIRMED（分层方向） | 业务结果、摘要、进度与文件引用分离 |
| 定义形状与语义回归 | Workflow 指南与现存 Makefile：CONFIRMED（检查职责） | golden 只辅助审阅，不能代替行为和负向测试 |

## 3. 具体问题与修正

| ID | 原材料位置/说法 | 核验结论 | Kowa 处理 |
| :--- | :--- | :--- | :--- |
| H01 | Workflow §4：“通常只写 YAML”，§5 又要求路由可选中 | 前提不足；新增能力、接入入口或 NodeType 都可能需要代码 | 明确“仅复用已存在能力、节点语义和选择入口时”才主要改定义 |
| H02 | 总览 11 条模板、frontend-feature 21 节点 | 当前 builtin 目录只保留两条；前端 YAML 有 20 个显式 id，父流程 14 个 | 数量仅描述历史某版本，不作为当前事实或 MVP 范围；文本计数不是编译验证 |
| H03 | Agent §3 声称 code_review 已落地 | 所列 descriptor/factory/catalog/SDK/self-improve YAML 缺失 | 不给 CODE_CONFIRMED；只使用能力边界与接口设计 |
| H04 | Agent 指南第 99 行 OutputKeys 代码片段 | 字符串与列表被截断，不能直接编译 | 禁止复制为实现模板；未来以真实 schema 和可编译示例为准 |
| H05 | tech-plan/code-generate、code_review、code.generate 混用；白名单也有点号/下划线差异 | CODE_CONFIRMED：现存 YAML 使用 tech.design、code.generate、mr.create，demo 使用 demo.form | 不自动规范化成同一 ID；冻结一份能力目录并逐字校验 |
| H06 | Agent 指南以 make check 收尾 | CODE_CONFIRMED：Makefile 的 check 只依赖 build/guards，test 是独立目标 | 另跑相关单元、协议、定义和真实行为验证；不能声称 make check 覆盖全部 |
| H07 | 总览：“BC 间不互调，只经事件”；出箱链路画为全覆盖 | 较新架构页明确 MQ module 为占位；应用层可编排用例 | 不强制所有跨域调用事件化，不宣称现成可靠出箱；事务/持久意图逐条设计 |
| H08 | 总览把 Server 调度画进执行层、Daemon 称完全无状态 | 角色图与进程职责混淆；本地凭证、缓存与进程确实存在 | Server 调度属于控制面；Runner 不持有业务真源，但并非没有本地状态 |
| H09 | 总览目录树在 domain、interfaces、web、scripts 下的缩进 | 与同一材料的进程拓扑及 codebase-map 不一致，属还原/排版风险 | 不按该树直接建 Kowa 目录，也不将图示外部 Mongo 推断为核心数据库 |
| H10 | source=remote 作为通用红线、历史 server 向 agent /push | remote/local 同历史 owner token 耦合；Kowa 已确认 Runner 出站 | 不继承同名字段的隐式授权或入站拓扑；能力、位置、权限分别建模 |
| H11 | project > global > bundled 覆盖、未知 kind 回退 bug-fix | 指南叙述，缺 loader/router 源码；会扩大首期配置入口 | 首期建议仅发布内置定义，未知选择报错，不读取隐藏本地 override |
| H12 | loop_max_iter=3、耗尽转人工 | YAML 可见 loop_exhausted_advance，但编译/循环代码缺失 | 不直接等同 Kowa 三次返修；需以初次评审+三次修订的边界用例核验 |
| H13 | golden 更新命令即可使快照通过 | 只能更新期望形状，不能证明新行为正确 | 必须审阅 diff 并运行独立语义断言，不以刷新快照消除真实失败 |
| H14 | 总览 knowledge(vN) 展示旁路 | 与历史知识版本文档吻合，但属于兼容旧消费者的取舍 | Kowa 延续统一快照方案，不恢复展示/消费双权威 |

补充：Parse 拒绝非法 NodeType 与 executor 的运行时兜底可以同时存在，并非矛盾；不能因提到 unknownNodeExecutor 就推断普通 YAML 可绕过解析期检查。旧历史 local 白名单也不证明 Kowa 控制面应执行模型任务。

## 4. 源码可核验范围

定向检查 23 个路径：现存 Makefile、demo-agent 的能力、测试和 main 共 4 个；其余 19 个缺失，包括 go.mod、loader/parse/embed、domain definition/dag、orchestrator/executor、SDK、catalog、developer descriptor/factory、agenthost，以及 small-feature/self-improve/self-iteration YAML。

这证明当前资料不足以复跑教程，不证明这些文件在历史完整项目中从未存在。现存测试源码仍只覆盖 demo 首跑/续跑，未执行；不能用于证明 code-review、Compile 或完整控制面行为。

辅助核对入口：

- [历史 Makefile](/Users/liufei/workspace/feishu_ai_workflow/Makefile)。
- [demo 能力](/Users/liufei/workspace/feishu_ai_workflow/cmd/demo-agent/cap_demo_form.go)、[demo 运行器入口](/Users/liufei/workspace/feishu_ai_workflow/cmd/demo-agent/main.go)。
- [前端 YAML](/Users/liufei/workspace/feishu_ai_workflow/internal/infra/workflowdef/builtin/frontend-feature.yaml)、[父流程 YAML](/Users/liufei/workspace/feishu_ai_workflow/internal/infra/workflowdef/builtin/parent-requirement.yaml)。
- [较新架构边界](/Users/liufei/workspace/feishu_ai_workflow/docs/wiki/architecture/koda-current-architecture.md)、[代码地图](/Users/liufei/workspace/feishu_ai_workflow/docs/architecture/codebase-map.md)。

## 5. 本轮采用产物

[Workflow 与 Capability 扩展设计](workflow-capability-design.md)承接可用机制、明确 Kowa 适配与冻结前断言。该稿不修改 common/decision，不新增生产代码或开发阶段；具体技术取舍仍以草案标识，不把资料到位等同接口已批准。
