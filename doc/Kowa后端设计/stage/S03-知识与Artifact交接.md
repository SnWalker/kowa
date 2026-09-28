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
- CODE_CONFIRMED：无存储实现。
- DATA_CONFIRMED：无真实制品。
- STRONG_INFERENCE：内容与确认事件分离避免摘要循环和审计丢失。
- HYPOTHESIS：保留期和生产对象存储参数后置。
- 文档与源码差异：候选字段未进入正式机器合同。
- 证据不足项：大文件、故障恢复和并发清理性能。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

知识/Task/受控操作生产内容；校验并发布不可变引用；Workflow、Runner、Web、评测消费。

## 本阶段新增或修改的模型

KnowledgeContent/Snapshot/BindingEvent、EffectiveInput、Artifact/Reference；确认事件不得混入内容摘要。

## 约束

先上传校验再发布；同内容可复用但确认不丢失；未发布/摘要错/跨 Workspace 拒收；有效引用不可清理。

## 实施清单

- [ ] 以缺失、空知识、摘要错和孤立上传建立红灯。
- [ ] 实现本地开发 Artifact Adapter 与稳定端口。
- [ ] 实现知识选择、确认和输入组装事务。
- [ ] 发布 schema、样例、访问控制与清理竞态测试。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖 A03—A06、A16—A17、A27、C01—C02。

### 定向测试

mise exec -- go test ./internal/knowledge/... ./internal/artifact/...

### 受影响回归、构建与静态门禁

mise exec -- go test ./...；mise exec -- go vet ./...；mise exec -- go build ./...；文档门禁；git diff --check。

## 运行时验收

隔离存储中删除上游任务目录后，下游仍按 ArtifactRef 读取并校验；不使用真实业务仓。

## 人工操作边界

不适用。

## 退出条件

kowa.knowledge-artifact.v1 发布完整机器合同、正反样例、消费者核对与矩阵映射；持久化/清理测试通过。

## 停止点

完成后停止，不调用模型 Provider。
