# S03：知识与 Artifact 交接

> 状态只在总体设计维护；执行事实进入 record/S03.md。

## 阶段设计依据

方案、编码和评审必须消费同一冻结知识及可校验产物，不能依赖本地目录或把“可读”误作“已批准”。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现 KnowledgeSnapshot/EffectiveInput 与不可变 Artifact 的发布、引用、校验、读取和删除保护合同。

## 前置与跨端依赖

- 本端前置阶段：S02。
- 跨端阶段：无。
- 共享契约 owner：backend:S03。
- 冻结契约及版本：kowa.knowledge-artifact.v1。

## 必读

common 知识/制品不变量、跨端语义契约、候选合同 §§3—8、质量设计、验收 A03—A06/A16—A17/A27/C01—C02。

## 推荐 Skills

- `golang-how-to`
- `golang-database`
- `supabase-postgres-best-practices`
- `golang-security`
- `golang-error-handling`
- `golang-testing`

## 核心文件与修改范围

### 核心文件

未来 Knowledge/Artifact domain、输入组装、文件/S3 Adapter seam、迁移、schema 与合同测试。

### 允许修改

知识快照、确认事件、有效输入、上传后发布、digest/归属校验、引用和孤立内容清理保护。

### 明确禁止

向量检索前提、自动写知识仓、代码用本地路径跨节点交接、生产 S3 部署。

### 并行写入范围

独占 Knowledge/Artifact 模块及 kowa.knowledge-artifact.v1。

## 当前问题与证据

- CONFIRMED：知识确认、冻结版本和 Artifact 真源边界已批准。
- CODE_CONFIRMED：Knowledge/Artifact 模块和 PostgreSQL 000003 已由原 S03 WIP 安全迁移；历史红灯与当前验证见 record。
- DATA_CONFIRMED：无真实制品。
- STRONG_INFERENCE：内容与确认事件分离避免摘要循环和审计丢失。
- HYPOTHESIS：保留期和生产对象存储参数后置。
- 文档与源码差异：候选字段已落实 api/knowledge-artifact/v1/schema.json、正反样例和正式共享合同。
- 证据不足项：大文件、故障恢复和并发清理性能。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

知识/Task/受控操作生产内容；校验并发布不可变引用；Workflow、Runner、Web、评测消费。

## 本阶段新增或修改的模型

KnowledgeContent/Snapshot/BindingEvent、EffectiveInput、Artifact/Reference；确认事件不得混入内容摘要。

## 约束

先上传校验再发布；同内容可复用但确认不丢失；未发布/摘要错/跨 Workspace 拒收；有效引用不可清理。

## 实施清单

- [x] 以缺失、空知识、摘要错和孤立上传建立红灯。
- [x] 实现本地开发 Artifact Adapter 与稳定端口。
- [x] 实现知识选择、确认和输入组装事务。
- [x] 发布 schema、样例、访问控制与清理竞态测试。

## 测试与自动验证

### Characterization 与目标红灯

首次实施为纯绿地，characterization 不适用；当前实现及退出证据见 record/S03。覆盖 A03—A06、A16—A17、A27、C01—C02，从缺失生产包的目标编译红灯开始。

2026-09-30：进行中新增代码与证据保留在工作区；Task 发布验收发现 S02 责任缺陷，按重开规则停止。此处是历史停止事实；2026-10-01 已恢复并退出，v1 已冻结，当前状态仍以总体设计为准，详情见 record/S03.md。

### 定向测试

mise exec -- go test ./internal/knowledge/... ./internal/artifact/...

### 受影响回归、构建与静态门禁

mise exec -- go test ./...；mise exec -- go vet ./...；mise exec -- go build ./...；mise exec -- make verify；mise exec -- make test-race；mise exec -- make test-identity-workspace-integration test-workflow-execution-integration test-knowledge-artifact-integration；git diff --check。

## 运行时验收

隔离存储中删除上游任务目录后，下游仍按 ArtifactRef 读取并校验；不使用真实业务仓。

## 人工操作边界

不适用。

## 退出条件

kowa.knowledge-artifact.v1 发布完整机器合同、正反样例、消费者核对与矩阵映射；持久化/清理测试通过；最终 PR head 必需 lint/verify CI 全 SUCCESS（同时核对 web-verify）后确认退出。

## 停止点

完成后停止，不调用模型 Provider。

本阶段正式合同、消费者和 A03—A06/A16—A17/A27/C01—C02 映射见 [Knowledge/Artifact v1](../../wiki/contracts/knowledge-artifact-v1.md)。本阶段不调用真实 Provider，不运行生产迁移。
