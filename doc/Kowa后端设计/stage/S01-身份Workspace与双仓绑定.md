# S01：身份、Workspace 与双仓绑定

> 状态只在总体设计维护；执行事实进入 record/S01.md。

## 阶段设计依据

MVP 是小团队多人系统，登录、成员资格、仓库安装和 Runner 身份必须分离，一项 WorkItem 只能写一个项目仓。

## 现行勘误（仅 REOPENED 时填写）

2026-10-01 CODE_CONFIRMED：v1 callback 实际直接序列化无 tags 的 WebIdentity；旧“文档与源码差异已消除”结论缺少 producer wire 验证，本轮目标测试补齐。Server 原仅装配 health；历史 HTTP seam 不代表已装配 http。仅 S01 为最早责任，后继基线冻结；S02 独立责任不在本会话处理。

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
- 文档与源码差异：历史“已消除”结论被当前callback字段直接证据推翻；本轮以目标红灯修复并验证严格schema和Server真实装配，详见现行勘误与record。
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

## 本轮合同修订（2026-10-01）

原合同违约：callback 直接序列化无 JSON tags 的 WebIdentity，实际 GitHubUserID/Login 违反 v1 的 githubUserId/login。下一实施会话以直接证据登记最早 S01 重开并冻结旧问题；本轮不改状态。

获批扩充：由 S01 唯一交付 OAuth 与 UI 安全交接、刷新后的身份/内存 CSRF 恢复和 Identity/Workspace Server 装配。保持成员/双仓语义；交接若改变原 callback 行为需明确版本，不能原地改写冻结 v1。路由版本与恢复算法在本阶段比较并冻结，不预设整体 /api/v2 迁移或 HMAC。真实 App/TLS 仍属 S08。

退出增加：实际响应严格 schema、错误/重复 state、非法 returnTo、会话撤销/过期、跨会话 CSRF、刷新/多标签/身份切换晚到响应及真实组合根隔离验收。删除最后旧消费者对应的过渡代码，核验最终 head CI后停止。

## 返工实施合同与验证入口（2026-10-01）

保持原Identity/Workspace目标。范围增加 `internal/server/identity.go`、Server Fx/config、PostgreSQL lazy pool lifecycle、web-session.v1机器合同、HTTP producer/schema与隔离组合根测试；不修改SQL/migrations、common/decision、后继阶段源码或前端页面。

Characterization适用于原callback无tags响应，原测试源码归档record/evidence/S01-callback-characterization.go.txt；永久测试改为原冻结schema目标。新恢复/Server入口无旧行为，目标红灯覆盖缺失RecoverSession与未装配API，不虚构characterization。旧callback字段错误与新API缺口分别追溯。

版本/算法/精确操作、外部默认拒绝边界与未来Web消费者义务以 [web-session.v1](../../../api/web-session/v1/README.md) 唯一冻结；v1 schema不改写。body/redirect不得带GitHub token或raw session；恢复不旋转、不延期、内存CSRF跨tab稳定，撤销/到期/跨session重读拒绝。前端late response generation义务由合同冻结，本阶段只验后端隔离，不声称页面交付。

定向：`mise exec -- go test -count=1 -json ./internal/identity ./internal/workspace ./internal/interfaces/httpapi ./internal/server ./internal/platform/config`。
隔离集成：`mise exec -- make test-identity-workspace-integration`（Store+真实Server/Fx graph、PostgreSQL17.11、local真实OAuth adapter、受控visibility、TLS测试传输、正反schema/刷新/tab/切换/撤销/到期；up/down表数归零）。
回归：`mise exec -- make verify`、`mise exec -- make test-race`、既有S02/S03隔离集成（仅回归，不执行其阶段）、`mise exec -- go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...`、golangci-lint v2.14.0、`git diff --check`。
退出：所有自动矩阵及最终PR head CI通过，记录run/job/SHA，S01重新DONE后清activeReopen并恢复previousCurrentStage=S04导航；S02/S03及后继基线不变。本会话停止，新会话先处理S02责任，再考虑S04。真实App/TLS/生产操作仍S08，不将其伪装为本次验收。
