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
- 跨端阶段：backend:S01。
- 共享契约 owner：backend:S01。
- 冻结契约及版本：kowa.identity-workspace.v1。

## 必读

前端信息架构、身份术语、跨端合同、backend:S01 合同/record、身份安全设计、验收 A02/A28/C04—C06。

## 推荐 Skills

- `vercel-react-best-practices`
- `vercel-composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

登录回调 UI、session client、Workspace settings、runtime status、权限守卫及合同 fixture/tests。

### 允许修改

登录/登出、会话过期、成员与双仓表单、Runtime 健康/Provider/gh 展示、版本冲突和错误呈现。

### 明确禁止

浏览器保存 token、自报 actor、把 installation/Runtime 在线当管理员、直接调用 Runner 接口。

### 并行写入范围

独占身份/Workspace/Runtime 页面；只消费 backend:S01 v1。

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

页面只消费 kowa.identity-workspace.v1；正反 fixture、组件/E2E、权限与版本冲突体验通过。

## 停止点

完成后停止，不实现 WorkItem 与知识绑定。
