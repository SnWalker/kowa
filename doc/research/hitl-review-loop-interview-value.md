# 自动评审回环与 Human-in-the-Loop 的面试价值

调研日期：2026-09-23

## 1. 客观结论

对大厂 Agent 应用/平台研发岗位，这项经历有价值，但价值分两层：

- **只描述流程图，价值中等**：`plan → plan_review → plan`、最多三轮、之后人工审批，属于主流 Agent 框架和 Coding Agent 产品已有的标准模式，不能仅凭节点名称证明技术深度。
- **能讲清生产级语义，价值较高**：若本人真正负责结构化反馈、方案版本链、持久化暂停/恢复、超限转人工、重复回调幂等、过期审批防护、权限审计及效果度量，这会同时体现 Agent 编排、后端状态机和 AI 质量治理能力。

因此，简历可以把它作为第一条核心项目点，但不宜表述成“设计了 Multi-Agent 流程”后便结束。最有区分度的不是“有两个 Agent 和一个 Human 节点”，而是把不稳定的模型判断变成可控、可恢复、可衡量的研发流程。

公开一手资料无法证明某道题在“大厂面试中出现的频率”；下面结论是依据官方岗位要求、官方招聘说明及头部厂商实际产品/框架反推的招聘价值，不冒充面试题统计。

## 2. 一手证据

### 2.1 招聘端明确需要评测、人工评审与升级机制

OpenAI 的 Forward Deployed Engineer 岗位把“评测、验证证据、人工评审工作流、升级路径和上线标准”放在同一职责中，并要求通过评测、错误分析、可观测性和客户反馈改善可靠性。这直接说明，生产 Agent 岗看重的不是单独接一个人工节点，而是完整的质量与控制闭环。[OpenAI：Forward Deployed Engineer, Healthcare](https://openai.com/careers/forward-deployed-engineer-%28fde%29-healthcare-nyc-new-york-city/)

Microsoft AI 的 Evals Engineer 岗位要求评价用户实际接触的“模型 + 编排 harness + 工具 + 数据”，负责指标、数据集、多轮轨迹评分和结果到改进行动的闭环，说明评审器设计与工作流本身都属于被考察的工程对象。[Microsoft AI：AI Evaluations, Health](https://microsoft.ai/careers/job/4391533009/member-of-technical-staff-ai-evaluations-health/)

Anthropic 的公开招聘说明强调技术面会讨论候选人的实际经历，并看重直接能力证据。由此可推断，简历上的机制只有在能回答设计取舍、故障语义和验证结果时才会转化为面试优势。[Anthropic Careers：How we hire](https://www.anthropic.com/careers)

### 2.2 “自动评审后再由人评审”是合理模式，但已逐渐成为基线

Anthropic 将“生成器产出—评估器反馈—生成器迭代”定义为 evaluator-optimizer 模式，并强调两个适用前提：评价标准清晰，迭代带来可测量改善；同时建议设置最大迭代次数等停止条件。[Anthropic：Building Effective AI Agents](https://www.anthropic.com/engineering/building-effective-agents)

GitHub Copilot 官方给出的 PR 流程正是“先自动评审并修复高置信问题，再进入人工评审”，理由是把人的时间留给设计取舍和产品影响；人工请求修改后，Coding Agent 会继续产生新提交。这与 Kowa 的自动 `plan_review` 通过后进入 Human 审批、驳回后回到 `plan` 在控制结构上高度一致。[GitHub Docs：Copilot code review across the PR lifecycle](https://docs.github.com/en/copilot/tutorials/use-copilot-code-review-across-the-pull-request-lifecycle)、[GitHub Docs：Review the output and iterate](https://docs.github.com/en/copilot/how-tos/copilot-on-github/use-copilot-agents/overview)

OpenAI 也把失败阈值和高风险动作列为触发人工介入的两类主要条件，并特别指出 Coding Agent 失败时应把控制权交还用户。因此“三轮自动回环后停止或转人工”方向正确，但“默认值为 3 且可配置”本身只是必要的停止条件，不是强亮点；面试官更可能追问为何是 3、达到上限后如何处置以及成本/质量依据。[OpenAI：A practical guide to building agents](https://openai.com/business/guides-and-resources/a-practical-guide-to-building-ai-agents/)

### 2.3 HITL 的高价值落在持久化与一致性，而不是一个等待按钮

OpenAI Agents SDK 的 HITL 会把待审批调用暴露为 interruption，序列化 `RunState` 后可跨进程、跨时间恢复；官方还特别要求服务端保存可信状态、鉴权与授权审批人、校验待处理项，并防止并发或重放导致同一快照恢复两次。[OpenAI Agents SDK：Human-in-the-loop](https://openai.github.io/openai-agents-python/human_in_the_loop/)

LangGraph 同样要求用 checkpointer 保存中断状态，使人工节点可暂停数日后从原处恢复，并区分瞬时错误重试、携带上下文的模型返修和等待用户输入三类路径。[LangGraph：Thinking in LangGraph](https://docs.langchain.com/oss/javascript/langgraph/thinking-in-langgraph)

这意味着：若项目只是同步阻塞线程等待人工输入，价值有限；若实现了不占用执行资源的持久化挂起、恢复、幂等和版本校验，面试价值明显更高。

### 2.4 自动 Reviewer 不能被默认当作真值

Anthropic 的 Agent 评测实践指出，模型评分具有非确定性，必须用人类专家校准；建议能用确定性检查时优先使用确定性 grader，开放问题再使用具有清晰 rubric 的模型 grader，并按维度拆分评分。它还强调多次 trial、轨迹记录、能力评测与回归评测，避免一次自动评审制造虚假信心。[Anthropic：Demystifying evals for AI agents](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents)

因此，`plan_review` 若只是另一段 Prompt，技术说服力不高。需要说明评审标准从何而来、输出如何结构化、误判如何发现，以及自动评审与人工最终判断如何校准。

## 3. 对当前机制的逐项评价

| 机制 | 面试价值 | 客观评价 |
| --- | --- | --- |
| `plan` 与 `plan_review` 职责分离 | 中等 | 符合 evaluator-optimizer；需说明为何独立 Reviewer 比同一 Agent 自检更可靠，以及模型/上下文是否隔离。 |
| 驳回时携带评审意见回到 `plan` | 中高 | 比简单重试更有意义，体现“基于反馈重新生成”；关键是反馈结构、旧方案引用和新版本血缘。 |
| 新方案作为新版本供下游消费 | 高 | 能引出不可变制品、版本绑定和避免下游消费旧版本等后端难题。 |
| 默认最多三轮且可配置 | 低到中 | 是必要的防无限循环措施，但配置项本身简单；应有超限状态、人工升级及成本/收益数据。 |
| 自动评审通过后再人工审批 | 中高 | 与 GitHub 的“机器先筛、人工关注高价值判断”一致；要能说明人的审批权限、超时、撤回、审计和过期决策处理。 |
| 人工驳回后携带意见重新执行 | 高（若可恢复） | 同时涉及长时挂起、状态恢复、幂等回调和方案版本并发控制，是最值得深挖的部分。 |

## 4. 面试官最可能继续验证的技术深度

1. `plan_review` 的输出契约是什么：`verdict`、问题严重度、证据、修改建议是否结构化，格式错误如何处理？
2. 为什么自动评审最多三轮？三轮后的状态是失败、暂停还是转人工？如何防止同一意见反复振荡？
3. Reviewer 如何避免与 Planner 的相关性错误：是否使用不同 Prompt、模型、上下文或确定性规则？
4. 每轮方案是否不可变并形成版本链？评审意见绑定哪个版本？下游如何保证只消费被批准版本？
5. Human 节点等待数小时或数天时是否释放 Worker？服务重启后如何恢复？重复审批回调是否幂等？
6. 人工审批到达时，如果方案已更新或工作流被取消，怎样拒绝过期决策？审批人身份和意见如何审计？
7. 怎样证明自动回环有用：首轮通过率、平均返修轮数、超限率、人工驳回率、自动评审与人工结论一致率、人工评审耗时和最终任务成功率如何变化？

能用真实案例和数据回答其中大部分，才可评价为高价值经历。

## 5. 建议的简历表达

> 负责技术方案评审回环与 Human-in-the-Loop 门禁，将方案生成、自动评审和人工审批建模为可恢复状态流；自动评审未通过时结构化回传意见，并基于上一版本迭代生成新方案，支持可配置的最多三轮返修及超限转人工；人工驳回后携带审批意见回退重执行，通过版本绑定保证下游仅消费已批准方案。

这版比“负责整条 Multi-Agent 研发工作流编排”更可信，也把个人职责集中在反馈闭环、状态控制和制品一致性上。若项目尚未实现“可恢复”“超限转人工”“版本绑定”中的任一项，应删除相应表述，不能把设计目标写成已交付事实。
