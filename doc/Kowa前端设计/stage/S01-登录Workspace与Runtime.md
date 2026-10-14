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
- 跨端阶段：backend:S01、backend:S04、backend:S05。
- 共享契约 owner：backend:S01（`kowa.web-session.v1`、`kowa.identity-workspace.v1`）、backend:S04（`kowa.runtime-web.v1`）、backend:S05（`kowa.delivery-governance.v1`）；逐操作责任见消费准入。
- 冻结契约及版本：四个 v1 合同均由对应 owner 冻结于 main，并以 manifest 文件 SHA-256 与 `schemaSha256` 双重 pin（见消费准入块），准入证据见 [record/S01-消费准入证据](<../record/S01-消费准入证据.md>)。
- 间接影响：backend:S02 的 Runtime 注册/执行核心通过 backend:S04 的 `kowa.runtime-web.v1` 投影到达本阶段（间接责任，不构成本阶段直接消费绑定）；backend:S03 知识/Artifact、backend:S06 固定流程与组合 Web 查询不在本阶段范围。

## 必读

- `../MVP信息架构.md`、`../web-preview-reference/index.md`、`../Web工程基座.md`、`../验证规则.md`。
- `../../wiki/glossary/permission-identities.md`、`../../wiki/contracts/mvp-cross-end-semantics.md`。
- `../../../api/identity-workspace/v1/README.md`、`schema.json` 与 `examples/`。
- `../../../api/web-session/v1/README.md`、`../../../api/runtime-web/v1/README.md`、`../../../api/delivery-governance/v1/README.md` 与各自 `schema.json`、`examples/`（含消费者义务与负例）。
- `../../Kowa后端设计/stage/S01-身份Workspace与双仓绑定.md`、`../../Kowa后端设计/record/S01.md`、`../../Kowa后端设计/record/S04.md`、`../../Kowa后端设计/record/S05.md`。
- `../../research/mvp-identity-security-design.md`、`../../Kowa验收与运行/MVP验收矩阵.md` 的 A02/A28/C04—C06。
- 当前源码 seam：`src/app/App.tsx`、`src/app/AppShell.tsx`、`tests/unit/foundation.test.tsx`、`tests/e2e/foundation.spec.ts`、`playwright.config.ts`、`vite.config.ts`；核对后端 producer 时只读 `internal/interfaces/httpapi/identity_workspace.go`、`runtime_web.go`、`delivery.go`、`internal/server/identity.go`、`internal/server/app_integration_test.go`。

### 2026-10-01 准入审计勘误（历史，保留）

CODE_CONFIRMED：现有冻结 v1 仅定义 Identity/Workspace/Repository，不定义 RuntimeView、Provider 或机器 GitHub 身份 Web 查询。OAuth callback 返回 JSON，尚未冻结 UI 交接和刷新后的身份/内存 CSRF 恢复方式。本阶段要求的完整生产页面暂不能只靠该合同完成；准入保持未通过，不以 fixture 或自创 API 填补。Runtime 的 owner、冻结版本与 Web HTTP surface，以及会话交接/恢复合同须先明确；不得擅自扩大跨端依赖。详见 record/S01.md。本勘误保留原目标，未宣布范围变更。

### 2026-10-15 准入解除（CODE_CONFIRMED）

上述两类缺口已由 owner 阶段在 main 冻结解决：`kowa.web-session.v1`（backend:S01）冻结 v2 会话交接/恢复/登出；`kowa.runtime-web.v1`（backend:S04）冻结 Runtime/Provider/执行配置 HTTP 投影；`kowa.delivery-governance.v1`（backend:S05）冻结 `gitRemote.status` 只读机器身份与 Git remote 投影。旧 requires 中的 `backend:S02 runtime.registration` 与 `backend:S04 runtime.query` 经 producer→transition→consumer 复核判为文档漂移（前者无 manifest 且属 Runner application 生产者，后者不在 S04 实际 manifest），已按实际 HTTP 操作重新绑定并 pin manifest 文件 SHA-256 与 `schemaSha256`。工作区列表/发现仍无 owner 操作，按“创建后进入 + 已知 ID 直接进入”实施并登记为 backend:S06 `web-application.v1` 候选缺口。证据见 [record/S01-消费准入证据](<../record/S01-消费准入证据.md>)。

## 推荐 Skills

- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

登录页与会话守卫、session client（`src/api/`、`src/auth/`）、Workspace 入口与设置页（`src/workspaces/`）、Runtime 列表/详情与 Git remote 投影（`src/runtimes/`）、合同类型与严格解码、合同绑定 fixture 与组件/E2E 测试，以及本地可复跑的真实控制面验收装配（`tests/webharness/`、`scripts/web-e2e-control-plane.sh`，仅测试用）。

### 允许修改

登录/登出、会话过期、成员与双仓表单、Runtime 健康/Provider/gh 展示、版本冲突和错误呈现；前端路由、样式、测试与本地验收脚本。

### 明确禁止

浏览器保存 token、自报 actor、把 installation/Runtime 在线当管理员、直接调用 Runner 接口；修改后端源码/合同/schema/迁移/依赖；把测试装配当业务实现或提交证书/私钥/凭证。

### 并行写入范围

独占身份/Workspace/Runtime 页面；只消费下列明确 owner 的冻结操作。

## 当前问题与证据

- CONFIRMED：身份分离和页面职责已定义。
- CODE_CONFIRMED：S00 基座存在且无本页面实现（`src/app/App.tsx` 只有 `/` 与兜底路由）；四个消费合同的 HTTP 操作已在本 HEAD 逐一对照 `internal/interfaces/httpapi/` 路由表。
- CODE_CONFIRMED：web-session v2 回调以 303 交接 UI，`session.bootstrap` 为只读快照；消费者义务（内存保存 CSRF、`requestId` 比对、身份/工作区切换清缓存、跨标签失效通知、聚焦后重取）明确写入 `api/web-session/v1/README.md`。
- CODE_CONFIRMED：runtime-web 的 `freshness` 三态与四层能力字段不可合并，`stale`/`unknown` 不得呈现为可用；`delivery-governance` 的 `writeObservation` 恒为 `none`。
- CODE_CONFIRMED：`kowa.identity-workspace.v1` 没有工作区列表操作（delivery.json operations 与 `identity_workspace.go:62-73` 路由表均无集合查询），本阶段不发明列表 API。
- DATA_CONFIRMED：真实 OAuth 与 App 安装授权留到后端 S08；本阶段用受控外部适配器与隔离 PostgreSQL 验证。
- STRONG_INFERENCE：服务端动作列表可避免前端复制授权（`availableActions` 类字段在 S02 以后合同出现，本阶段以服务端返回的角色/成员事实控制可见动作）。
- HYPOTHESIS：本机回环证书与真实 TLS 体验需 S08 外部验收校准；本地受控 TLS 装配只证明应用行为。
- 文档与源码差异：旧 requires 的 `runtime.registration`/`runtime.query` 与 main 不符，已按实际 manifest 勘误。
- 证据不足项：真实多账号浏览器切换与真实 gh 身份漂移仍需 S08 外部事实；本阶段以受控身份 stub 验证应用行为。
- 固化旧错误的测试：无（S00 foundation 只保护既有 shell，不证明 S01 行为）。

## Producer、Transition、Consumer

后端会话/Workspace 投影生产事实；前端查询、表单和缓存转换；成员用户消费。

## 本阶段新增或修改的模型

仅消费 WebIdentity/WorkspaceConfig/RepositoryBinding/RuntimeView，不新定义业务语义。

## 约束

切换身份/Workspace 清缓存；所有写入含版本/幂等键；未知字段阻断；权限拒绝不能伪装资源不存在以外的任意成功。

## 实施清单

- [ ] 从未认证、越权、冲突和双仓错误 fixture 建立红灯（合同样例绑定：官方 `examples/positive-*` 必须被接受，`examples/negative-*` 必须被拒绝）。
- [ ] 实现登录/session/守卫（v2 交接、内存 CSRF、`requestId` 比对、失效代数、跨标签通知、聚焦重取）。
- [ ] 实现 Workspace 配置（成员/双仓、版本冲突重审、策略与不可用拒绝）与 Runtime 页面（列表/详情/Provider/执行配置/Git remote 只读投影）。
- [ ] 覆盖缓存隔离、表单恢复、错误与可访问性；未知字段/形状 fail closed。

## 测试与自动验证

### Characterization 与目标红灯

纯绿地：进入时 `src/app/App.tsx` 只有 `/` 与兜底路由，没有既有会话/身份/Workspace/Runtime 行为可冻结，characterization 不适用；从目标契约与目标红灯开始（未认证守卫、越权拒绝、版本冲突重审、跨 Workspace 缓存隔离、双仓拒绝、Runtime/gh 未知态、合同负例拒绝）。

### 定向测试

```bash
bash scripts/web-env.sh pnpm test
bash scripts/web-env.sh pnpm test:e2e
```

### 受影响回归、构建与静态门禁

```bash
bash scripts/web-env.sh pnpm lint
bash scripts/web-env.sh pnpm typecheck
bash scripts/web-env.sh pnpm test
bash scripts/web-env.sh pnpm build
bash scripts/web-env.sh pnpm test:e2e
bash scripts/check-doc-governance.sh
bash scripts/check-doc-governance-test.sh
git diff --check && git status --short
```

### 真实控制面浏览器旅程（本地可复跑）

```bash
bash scripts/web-e2e-control-plane.sh
```

使用真实 `internal/server` 组合根 + 隔离外部适配器（本地 GitHub OAuth stub、受控仓库可见性、受控 git/gh 命令 runner）与隔离 Docker PostgreSQL，经临时自签 TLS 同源承载生产构建与 API；真实 OAuth/App 安装仍留 S08。

## 运行时验收

先用合同样例与组件测试；真实后端联调只使用隔离数据库与受控外部适配器，不执行 App 安装、不派发真实 WorkflowRun、不做 GitHub 业务写入。Web 关键旅程在浏览器层对真实控制面复跑（登录跳转→303 回调→会话恢复→工作区→Runtime/Git remote），不由 fixture 替代该层次。

## 人工操作边界

真实 GitHub App 授权由用户在后端 S08 操作。

## 退出条件

实际消费本阶段绑定的冻结操作；正反 fixture、组件/E2E、权限与版本冲突体验通过。

## 停止点

完成后停止，不实现 WorkItem 与知识绑定。

## 消费准入（2026-10-15 重新准入，已 pin）

完整目标保持；下列操作均由已 `DONE` 的 owner 冻结为实际 `http` 层次，并 pin manifest 文件 SHA-256；`schemaSha256` 在准入时逐份核对一致。逐操作 producer→transition→consumer、正反样例与核验命令见 [record/S01-消费准入证据](<../record/S01-消费准入证据.md>)。operation 语义由唯一 owner 冻结，路由/版本不可由前端发明；前端不新增 endpoint、不重新解释错误类别。

```json
{
  "schemaVersion": "kowa-stage-consumption.v1",
  "requires": [
    {"owner": "backend:S01","contract": "kowa.web-session.v1","operation": "oauth.begin","manifest": "api/web-session/v1/delivery.json","sha256": "54deba9c0a09af1a6290a5243417105f914d8a3402dcfcb3368ee6d8d2a6258e","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.web-session.v1","operation": "oauth.callback","manifest": "api/web-session/v1/delivery.json","sha256": "54deba9c0a09af1a6290a5243417105f914d8a3402dcfcb3368ee6d8d2a6258e","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.web-session.v1","operation": "session.bootstrap","manifest": "api/web-session/v1/delivery.json","sha256": "54deba9c0a09af1a6290a5243417105f914d8a3402dcfcb3368ee6d8d2a6258e","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.web-session.v1","operation": "session.logout","manifest": "api/web-session/v1/delivery.json","sha256": "54deba9c0a09af1a6290a5243417105f914d8a3402dcfcb3368ee6d8d2a6258e","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.bootstrap","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.config","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.repositories","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.member.upsert","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S01","contract": "kowa.identity-workspace.v1","operation": "workspace.member.revoke","manifest": "api/identity-workspace/v1/delivery.json","sha256": "c5d1fbf1c40d3d02e1a1a2a02a9daab47b69570780ff8ca2dd6588151aa2f996","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S04","contract": "kowa.runtime-web.v1","operation": "runtime.list","manifest": "api/runtime-web/v1/delivery.json","sha256": "6f89d868ea1edd5234c9d6353ed34694d23481bd149415ec7a56407674340392","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S04","contract": "kowa.runtime-web.v1","operation": "runtime.view","manifest": "api/runtime-web/v1/delivery.json","sha256": "6f89d868ea1edd5234c9d6353ed34694d23481bd149415ec7a56407674340392","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S04","contract": "kowa.runtime-web.v1","operation": "executionConfig.read","manifest": "api/runtime-web/v1/delivery.json","sha256": "6f89d868ea1edd5234c9d6353ed34694d23481bd149415ec7a56407674340392","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S04","contract": "kowa.runtime-web.v1","operation": "executionConfig.update","manifest": "api/runtime-web/v1/delivery.json","sha256": "6f89d868ea1edd5234c9d6353ed34694d23481bd149415ec7a56407674340392","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"},
    {"owner": "backend:S05","contract": "kowa.delivery-governance.v1","operation": "gitRemote.status","manifest": "api/delivery-governance/v1/delivery.json","sha256": "10a3bf1957a78a79feb42ca568425c4e3eb327390c6c96238e8df90875e66a71","level": "http","evidence": "doc/Kowa前端设计/record/S01-消费准入证据.md"}
  ]
}
```

准入勘误（2026-10-15）：

- 移除 `backend:S02 / kowa.workflow-execution.v1 / runtime.registration`：`api/workflow-execution/v1/delivery.json` 在 main 上不存在，且该 operation 是 Runner application 生产者，不是 Web 消费；backend:S02 的责任通过 backend:S04 投影间接到达，不做直接绑定。
- 替换 `backend:S04 / kowa.runtime-web.v1 / runtime.query`（不在 S04 实际 manifest）为 `runtime.list`、`runtime.view`、`executionConfig.read`、`executionConfig.update`，均 `http`。
- 补齐 `backend:S01 / kowa.web-session.v1` 的四个 v2 交接操作（`oauth.begin`、`oauth.callback`、`session.bootstrap`、`session.logout`）；回调只使用 303 UI 交接，不使用 v1 JSON callback。
- pin `backend:S05 / gitRemote.status`（候选转正式），manifest 文件 SHA-256 与 `schemaSha256` 均已核对。
- 遗留消费者缺口：工作区列表/发现无任何已 DONE owner 的 HTTP 操作；本阶段按 IA `/workspaces/:workspaceId` 以“创建后进入 + 已知 Workspace ID/URL 直接进入（服务端重新判定成员资格）”满足页面目标，不做本地记忆冒充服务端列表，并把该缺口登记为 backend:S06 组合 Web 查询（`web-application.v1` 候选）的消费缺口，不改变任何 DONE owner。
