# S01：身份、Workspace 与双仓绑定

> 状态只在总体设计维护；执行事实进入 record/S01.md。

## 阶段设计依据

MVP 是小团队多人系统，登录、成员资格、仓库安装和 Runner 身份必须分离，一项 WorkItem 只能写一个项目仓。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现服务端 GitHub 会话、Workspace 成员授权、项目/知识双仓绑定及其跨端合同。

## 前置与跨端依赖

- 本端前置阶段：S00。
- 跨端阶段：无。
- 共享契约 owner：backend:S01。
- 冻结契约及版本：kowa.identity-workspace.v1。

## 必读

common、permission-identities、mvp-cross-end-semantics、身份安全设计、后端设计/验证规则、验收 A02/A28/C04—C06。

## 推荐 Skills

- `golang-how-to`
- `golang-context`
- `golang-security`
- `golang-error-handling`
- `golang-database`
- `golang-testing`
- `golang-uber-fx`

## 核心文件与修改范围

### 核心文件

未来 Identity/Workspace domain、app、HTTP、PostgreSQL adapter、迁移、机器 schema 和合同测试。

### 允许修改

OAuth state/PKCE、服务端会话、首管理员允许名单、成员角色、RepositoryBinding、授权查询和审计。

### 明确禁止

把 installation 当用户、把登录当管理员、将 token 暴露给浏览器、实现 Workflow 调度或执行真实 App 安装。

### 并行写入范围

独占 Identity/Workspace 模块及 kowa.identity-workspace.v1；前端只能消费冻结版本。

## 当前问题与证据

- CONFIRMED：三种身份与双仓职责已冻结。
- CODE_CONFIRMED：Identity/Workspace 领域与用例、GitHub OAuth adapter、HTTP seam、PostgreSQL Store/迁移和 `kowa.identity-workspace.v1` 已实现，定向与全量测试通过。
- DATA_CONFIRMED：PostgreSQL 17.11 隔离实例已验证登录回调、会话、成员授权/撤销、双仓配置、并发幂等、审计失败回滚及 up/down migration；无真实 OAuth/仓库授权证据。
- STRONG_INFERENCE：无。
- HYPOTHESIS：会话期限与回环证书参数待部署阶段验证。
- 文档与源码差异：已消除；机器 schema、HTTP seam、正反样例和消费者说明已以 `kowa.identity-workspace.v1` 发布。
- 证据不足项：真实 GitHub App 回调、安装可见性与撤销行为，按本阶段人工边界推迟至 S08。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

OAuth/管理员命令生产身份与配置；授权用例校验版本和范围；Web、Workflow 准入及审计消费。

## 本阶段新增或修改的模型

WebIdentity、Session、WorkspaceMember、RepositoryBinding、WorkspaceConfigVersion；不得合并 Runtime 或机器 GitHub 身份。

## 约束

actor 来自会话；写命令含 expectedVersion/幂等键；知识仓只读；第二可写仓拒绝；跨 Workspace ID 猜测不可访问。

## 实施清单

- [x] 先以越权、冲突和第二可写仓建立红灯。
- [x] 实现领域/用例/存储与 HTTP seam。
- [x] 发布 schema、正反样例和前端消费者说明。
- [x] 覆盖会话撤销、CSRF、审计失败和仓库不可见。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；覆盖未认证、未授权、版本冲突、跨 Workspace、第二可写仓。

### 定向测试

mise exec -- go test ./internal/identity/... ./internal/workspace/...

### 受影响回归、构建与静态门禁

mise exec -- go test ./...；mise exec -- go vet ./...；mise exec -- go build ./...；文档门禁；git diff --check。

## 运行时验收

使用测试身份和隔离数据库验证登录回调、成员撤销与双仓配置；真实 GitHub App 授权推迟到 S08。

## 人工操作边界

不执行真实 App 安装；S08 前由用户准备回调、App 和测试账号。

## 退出条件

kowa.identity-workspace.v1 发布机器 schema/版本、正反样例、Producer/Consumer 核对、失败语义及矩阵映射；自动测试与迁移验证通过。

## 停止点

完成后停止，不进入 Workflow/Execution。
