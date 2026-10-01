# S01：登录、Workspace 与 Runtime

> 状态只在总体设计维护；执行事实进入 record/S01.md。

## 阶段设计依据

Web 必须区分登录用户、Workspace 权限、App 安装、Runtime 和机器 GitHub 身份，且所有动作由服务端授权。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

实现 GitHub 会话、Workspace 配置、成员/双仓管理与 Runtime/Provider/机器 gh 状态页面。

## 前置与跨端依赖

- 本端前置阶段：S00。
- 跨端阶段：backend:S01、backend:S02、backend:S04。
- 共享契约 owner：backend:S01、backend:S02、backend:S04（逐操作责任见消费准入）。
- 冻结契约及版本：既有 v1 为核心基线；新增操作待 owner 冻结，不能准入。

## 必读

- `../MVP信息架构.md`、`../web-preview-reference/index.md`、`../Web工程基座.md`、`../验证规则.md`。
- `../../wiki/glossary/permission-identities.md`、`../../wiki/contracts/mvp-cross-end-semantics.md`。
- `../../../api/identity-workspace/v1/README.md`、`schema.json` 与 `examples/`。
- `../../Kowa后端设计/stage/S01-身份Workspace与双仓绑定.md`、`../../Kowa后端设计/record/S01.md`。
- `../../research/mvp-identity-security-design.md`、`../../Kowa验收与运行/MVP验收矩阵.md` 的 A02/A28/C04—C06。
- 当前源码 seam：`src/app/App.tsx`、`src/app/AppShell.tsx`、`tests/unit/foundation.test.tsx`、`tests/e2e/foundation.spec.ts`、`playwright.config.ts`；核对后端 producer 时只读 `internal/interfaces/httpapi/identity_workspace.go`。

### 2026-10-01 准入审计勘误

CODE_CONFIRMED：现有冻结 v1 仅定义 Identity/Workspace/Repository，不定义 RuntimeView、Provider 或机器 GitHub 身份 Web 查询。OAuth callback 返回 JSON，尚未冻结 UI 交接和刷新后的身份/内存 CSRF 恢复方式。本阶段要求的完整生产页面暂不能只靠该合同完成；准入保持未通过，不以 fixture 或自创 API 填补。Runtime 的 owner、冻结版本与 Web HTTP surface，以及会话交接/恢复合同须先明确；不得擅自扩大跨端依赖。详见 record/S01.md。本勘误保留原目标，未宣布范围变更。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

登录回调 UI、session client、Workspace settings、runtime status、权限守卫及合同 fixture/tests。

### 允许修改

登录/登出、会话过期、成员与双仓表单、Runtime 健康/Provider/gh 展示、版本冲突和错误呈现。

### 明确禁止

浏览器保存 token、自报 actor、把 installation/Runtime 在线当管理员、直接调用 Runner 接口。

### 并行写入范围

独占身份/Workspace/Runtime 页面；只消费下列明确 owner 的冻结操作。

## 当前问题与证据

- CONFIRMED：身份分离和页面职责已定义。
- CODE_CONFIRMED：进入时应有 S00 基座，无本页面实现。
- DATA_CONFIRMED：真实 OAuth 留到后端 S08。
- STRONG_INFERENCE：服务端动作列表可避免前端复制授权。
- HYPOTHESIS：回环证书错误体验需真实环境校准。
- 文档与源码差异：以进入时审计为准。
- 证据不足项：真实多账号浏览器切换。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

后端会话/Workspace 投影生产事实；前端查询、表单和缓存转换；成员用户消费。

## 本阶段新增或修改的模型

仅消费 WebIdentity/WorkspaceConfig/RepositoryBinding/RuntimeView，不新定义业务语义。

## 约束

切换身份/Workspace 清缓存；所有写入含版本/幂等键；未知字段阻断；权限拒绝不能伪装资源不存在以外的任意成功。

## 实施清单

- [ ] 从未认证、越权、冲突和双仓错误 fixture 建立红灯。
- [ ] 实现登录/session/守卫。
- [ ] 实现 Workspace 和 Runtime 页面。
- [ ] 覆盖缓存隔离、表单恢复、错误与可访问性。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地；目标红灯对应登录、授权、版本冲突和跨 Workspace 拒绝。

### 定向测试

mise exec -- pnpm test -- identity workspace runtime

### 受影响回归、构建与静态门禁

pnpm lint/typecheck/test/build、认证/路由 E2E、文档门禁、git diff --check。

## 运行时验收

先用合同 provider 测试；真实后端联调仅使用隔离账号和数据库，不执行 App 安装。

## 人工操作边界

真实 GitHub App 授权由用户在后端 S08 操作。

## 退出条件

实际消费本阶段绑定的冻结操作；正反 fixture、组件/E2E、权限与版本冲突体验通过。

## 停止点

完成后停止，不实现 WorkItem 与知识绑定。

## 消费准入（2026-10-01）

完整目标保持；下列为待冻结需求，不是已交付 API。operation 的语义由唯一 owner 冻结，路由/版本不可由前端发明。已有 schema 不等于生产装配或用户旅程；准入须补齐 delivery 摘要及正反证据。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S01","contract": "kowa.web-session.v1","operation": "session.bootstrap","manifest": "api/web-session/v1/delivery.json","sha256": null,"level": "http","evidence": null},
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.config","manifest": "api/identity-workspace/v1/delivery.json","sha256": null,"level": "http","evidence": null},
    {"owner": "backend:S02","contract": "kowa.workflow-execution.v1","operation": "runtime.registration","manifest": "api/workflow-execution/v1/delivery.json","sha256": null,"level": "application","evidence": null},
    {"owner": "backend:S04","contract": "kowa.runtime-web.v1","operation": "runtime.query","manifest": "api/runtime-web/v1/delivery.json","sha256": null,"level": "http","evidence": null}
  ]
}
```
