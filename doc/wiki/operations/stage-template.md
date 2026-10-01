# Sxx：阶段名称

> 状态只在本端 `总体设计与进度.md` 维护。本文件是 Sxx 的工程实施合同；具体执行结果、偏差和证据见 `record/Sxx.md`。

## 阶段设计依据

说明当前问题、事实来源、为什么现在进入本阶段，以及它在总体设计和阶段依赖中的位置。

本阶段所有长期设计必须由 `CONFIRMED`、`CODE_CONFIRMED`、`DATA_CONFIRMED`、明确不变量或目标红灯支持。外部资料只能作为研究背景，不能替代 Kowa 的项目事实。

## 现行勘误（仅 REOPENED 时填写）

说明达到 `CODE_CONFIRMED` 或 `DATA_CONFIRMED` 的新证据、被推翻的旧结论、当前有效协议、最早责任 owner、后继阶段基线和禁止恢复的旧方案。普通阶段明确写“不适用”。

## 唯一工程目标

用一个可由用户行为或确定性证据验证的目标描述本阶段。

## 前置与跨端依赖

- 本端前置阶段：
- 跨端阶段：使用 `backend:Sxx` 或 `frontend:Sxx`。
- 共享契约 owner：跨端消费者填写具体 `backend:Sxx` 或 `frontend:Sxx`；本阶段是 owner 时填写自身编号；不涉及则写“不适用”。
- 冻结契约及版本：填写可机器识别的版本（如 `kowa.contract.v1`）或 `sha256:<64 hex>`；不涉及则写“不适用”。

## 交付与消费准入

- 交付层次：schema / application / http-seam / http / journey（填写实际承诺）。
- 入口与后续 owner：明确本阶段交付面，未交付部分不能隐含交给消费者。

按操作列出 owner、合同、入口和正反验收。跨端消费者添加 `kowa-stage-consumption.v1` JSON 块；每条 requires 含 owner、contract、operation、manifest、sha256、level、evidence。待冻结时 sha256/evidence 为 null，只能 NOT_STARTED；正式准入 pin delivery.json 摘要与证据。无消费需求时 requires=[]，不得据此豁免真实依赖。

## 必读

只列出进入本阶段必须恢复的共识、正式知识、上游契约、阶段记录和验证规则，不把整个仓库列为必读。

## 推荐 Skills

列出与本阶段目标最相关的项目级 Skills。它们是优先参考，不是退出硬门禁；执行者可按实际问题增减或跳过，并在 record 中记录实际使用及跳过原因。Skills 不得覆盖 `AGENTS.md`、正式契约或阶段边界。

## 核心文件与修改范围

### 核心文件

列出当前事实的 producer、transition、consumer 及对应测试入口。

### 允许修改

### 明确禁止

### 并行写入范围

列出本阶段拥有的目录、共享契约和不得由其他并行阶段修改的权威。

## 当前问题与证据

- `CONFIRMED`：
- `CODE_CONFIRMED`：
- `DATA_CONFIRMED`：
- `STRONG_INFERENCE`：
- `HYPOTHESIS`：
- 文档与源码差异：
- 证据不足项：
- 固化旧错误的测试：

## Producer、Transition、Consumer

修改既有行为时写出字段、状态或数据的完整链路。纯绿地阶段明确当前链路尚不存在，并写出目标 producer、transition 和 consumer；仅 characterization 可不适用。

## 本阶段新增或修改的模型

列出本阶段拥有的领域模型、接口或投影，并说明它们不能退化成什么。没有则写“不适用”。

## 约束

- 列出必须保持的业务不变量、唯一权威、顺序、失败语义、权限和可观测要求。
- 明确本阶段只负责什么，以及相邻阶段分别负责什么。
- 不在此复制正式契约的字段清单；通过链接引用权威契约。

## 实施清单

- [ ] 先建立适用的 characterization 测试；纯绿地阶段记录不适用依据。
- [ ] 建立能够证明目标契约尚未满足的首个红灯。
- [ ] 按 producer → transition → consumer 或契约 owner → consumer 的依赖顺序实施。
- [ ] 删除本阶段负责清理的旧消费者、临时兼容层或双写。
- [ ] 补齐可观测性、错误语义和必要文档。

## 测试与自动验证

### Characterization 与目标红灯

列出 fixture、预期失败及其对应的不变量。没有旧行为时说明不适用依据。

### 定向测试

```bash
# 写入可直接执行的精确命令
```

### 受影响回归、构建与静态门禁

```bash
# 写入回归、构建、文档门禁和 git diff --check 命令
```

## 运行时验收

说明必须使用的新 WorkflowRun、任务、制品、PR、日志或数据库水位，以及 `PASS`、`BLOCKING_FAIL`、`OBSERVATION_ONLY` 的判定边界。不需要运行时验收时写明依据。

## 人工操作边界

列出需要用户执行的真实操作、进入前水位、精确操作清单、用户需要返回的 ID，以及 AI 恢复后的只读验收范围。没有则写“不适用”。

## 退出条件

- 每项条件必须能给出测试、运行产物、日志、数据或人工验收证据。
- 共享契约 owner 阶段必须发布机器 schema、版本或内容摘要、正反样例、Producer/Consumer 核对、失败语义及验收矩阵映射；消费者阶段必须证明实际消费的版本与依赖声明一致。
- 自动验证通过但缺少必要真实验收时，不得标记 `DONE`。
- 实际 producer 符合 schema；承诺的生产入口已装配；多适配器/拒绝路径不变量有证据；直接消费者缺口已定位。
- record 已记录命令、退出码、测试数量、证据 ID、偏差、风险和未完成项。

## 停止点

说明本阶段明确不处理的相邻责任、触发 `HUMAN_ACTION_REQUIRED`、`BLOCKED` 或 `REOPENED` 的条件，以及下一阶段准入结论。`BLOCKED` 必须列出可复现条件、影响、为何不能由本阶段消除、解除条件和责任人；嵌套重开导致的暂停还必须链接 `suspendedReopen`。阶段完成后当前会话停止，不读取或实施下一阶段。
