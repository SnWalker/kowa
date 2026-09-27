# Kowa 的 AI-first 分阶段交付实践

调研日期：2026-09-22

## 1. 核心结论

完全由 AI 生成代码可行，但不等于无人治理。

OpenAI 的 agent-first 项目由 Codex 生成代码、测试、CI、文档和评测工具；人负责意图、验收标准、架构边界与最终结果。其关键不是更大的提示词，而是先建设 Agent 可读的仓库知识、工具、测试、评审和恢复闭环。[OpenAI：Harness engineering](https://openai.com/index/harness-engineering/)

Kowa 适合采用：

`共识约束 → 阶段计划 → 小批量实现 → 自动验证 → 独立复核 → 更新状态`

可以先做前端原型，但不宜完成全部生产前端后才开发后端。正式实现应先约定契约，再按用户能力做端到端垂直切片。

## 2. 最佳实践依据

### 2.1 规格先于实现，逐步细化

GitHub Spec Kit 的流程是 `Specify → Plan → Tasks → Implement → Converge`，每一步产生可供下一步使用的 Markdown 制品。[GitHub Spec Kit](https://github.github.com/spec-kit/)

OpenAI 的全 AI 编码实践采用 depth-first：把目标拆成设计、编码、评审、测试等较小构件，逐步形成更复杂的交付能力。[OpenAI：Harness engineering](https://openai.com/index/harness-engineering/)

因此，不应把整个系统作为一次 Agent 任务，也不宜只按“前端、后端、测试”横向分期。

### 2.2 仓库是 Agent 的长期记忆

OpenAI 曾尝试使用一个巨大的 `AGENTS.md`，但因内容易过时、难验证而失败；最终改为短 `AGENTS.md` 充当地图，结构化仓库文档作为系统真源。[OpenAI：Harness engineering](https://openai.com/index/harness-engineering/)

复杂计划、进度、技术债和验证证据应纳入 Git，并建立链接和机械检查，避免知识只存在于对话中。

### 2.3 计划必须可恢复、可验收

OpenAI 的 ExecPlan 指南要求计划是持续更新、可独立恢复的 living document；里程碑应独立可验证，以用户可观察行为而非“代码已写完”作为验收依据。[OpenAI：Using PLANS.md](https://developers.openai.com/cookbook/articles/codex_exec_plans)

该指南已归档，宜采用其“自包含、可恢复、行为验收、保留证据”原则，不必照搬格式。

### 2.4 AI 越快，批次越应小

DORA 指出，AI 生成的大变更难以审查和安全集成；工作批次应独立、有价值、可测试，通常在数小时至数天内完成。[DORA：Working in small batches](https://dora.dev/capabilities/working-in-small-batches/)

GitHub 也建议把 AI 生成的大功能拆为按依赖排序、可独立审查和测试的小 PR。[GitHub Docs：Stack AI-generated code](https://docs.github.com/en/copilot/tutorials/stack-ai-generated-code-in-pull-requests)

## 3. 对 Kowa 文档结构的建议

现有设想总体可行，但必须防止同一事实出现多个真源。

- `doc/common.md`：只保存当前有效的项目共识。
- `doc/decision.md`：只保存确有备选方案且已经作出的决策。
- `doc/Kowa后端设计/stage/Sxx-*.md` 与 `doc/Kowa前端设计/stage/Sxx-*.md`：各端阶段唯一的当前执行契约。
- 各端 `record/Sxx.md`：记录实际执行过程和证据，不复制方案或拥有阶段状态。
- 各端 `总体设计与进度.md`：各自阶段序列的唯一状态权威，并维护机器可读的 `currentStage` 和责任阶段重开状态。
- 各端 `当前阶段与下一步.md`：只保存最近会话的接力信息，不拥有阶段状态。
- `doc/当前进展.md`：只读汇总前后端当前指针、阻塞和下一动作。
- `doc/wiki/`：保存前后端都需要引用的当前知识；端内稳定设计分别保存在对应设计目录。

阶段计划至少包含：

- 用户可见目标与非目标；
- 前置依赖和权威接口；
- 实施任务及影响范围；
- 测试、评测和验收命令；
- 风险、回退和完成定义。

配对 record 只记录：进度、问题、意外发现、方案偏差、运行证据和阶段复盘。真实项目决策仍进入 `doc/decision.md`，不能在 record 建第二套决策真源。

阶段状态固定为 `NOT_STARTED`、`IN_PROGRESS`、`HUMAN_ACTION_REQUIRED`、`BLOCKED`、`DONE`、`REOPENED`。完成阶段被新证据推翻时，必须重开最早的责任阶段，并冻结后继阶段基线；每端的 `总体设计与进度.md` 使用 `activeReopen` 与 `suspendedReopen` 保留可恢复的责任路由，`activeReopen` 贯穿责任阶段从重开到重新完成的整个返工周期。返工期间若发现更早责任阶段失效，当前返工 owner 转为 `BLOCKED` 并进入唯一的 `suspendedReopen`，更早 owner 接管 `currentStage`；不允许第三层嵌套。

阶段编号表达推荐顺序，但每份计划还应显式声明依赖，避免演变成僵硬瀑布。

## 4. 推荐实施阶段

1. **工程基座**：技术栈、目录、开发环境、CI、格式化、静态检查、测试框架和最小 `AGENTS.md` 导航。
2. **架构骨架**：核心领域模型、鉴权与安全边界、前后端契约、持久化和可观测性基线。
3. **首条真实链路**：打通一个页面到 API、存储、日志和 E2E 测试的 walking skeleton。
4. **前端体验验证**：依据现有预览图建立设计 token、基础组件和关键流程原型；Mock 必须绑定权威契约。
5. **按能力垂直交付**：逐项实现项目接入、工作流定义、运行发起、DAG/步骤详情、人工门禁、产物/PR 等。
6. **可靠性与发布**：完善重试、幂等、恢复、权限、并发、超时、性能、安全、迁移与回滚。

每个垂直切片覆盖前端、后端、数据、错误处理、日志和测试，但施工时拆成前后端各自拥有的阶段，通过冻结契约和显式跨端依赖协作，并形成一个或少量可审查 PR。

每阶段执行同一闭环：

`确认规格/契约 → 先确认验收测试 → AI 实现 → AI 自检 → 独立 Agent 复核 → 人审高风险点 → 合并 → 更新记录`

## 5. 前端先行的可行性

### 可行部分

先做前端原型适合尽早确定信息架构、视觉、组件复用和交互流程，并可快速发现产品理解偏差。

### 主要问题

若先完成全部生产前端，再做全部后端，前端会长期依赖假数据；鉴权、分页、异步状态、错误语义、重试、实时更新和性能限制通常到集成末期才暴露，容易造成接口漂移和返工。

DORA 更建议从 service/API 层开始，以便小批量验证和暗发布，而不是从完整 UI 层向下推进。[DORA：Working in small batches](https://dora.dev/capabilities/working-in-small-batches/)

更稳妥的顺序是：

`前端原型校准 UX → 定义版本化 API/事件契约 → 打通真实 walking skeleton → 按能力垂直实现`

契约确定后，前后端可以并行，不必严格串行。

GitHub 的 contract-driven 指南要求双方在实现前约定输入输出、错误、重试、超时与兼容语义；Mock 只能验证消费者对 Mock 的行为，不能替代真实 provider 和端到端验证。[GitHub Spec Kit：Contract-Driven Development](https://github.github.com/spec-kit/guides/contract-driven-development.html)

## 6. 需要重点防止的失败模式

- 阶段过大，形成难审查、难回退的 AI 巨型 PR。
- common、stage、record 和状态页重复描述同一事实。
- 只有任务勾选，没有可执行验收与运行证据。
- 用 Mock 成功代替真实契约和 E2E 测试。
- 同一 Agent 定义需求、实现并作为唯一验收者。
- 只积累代码，不把失败转化为规则、测试、工具和评测集。

最终目标可以是“代码完全由 AI 生成”，但产品意图、架构风险、安全边界和发布责任仍应由人掌握。
