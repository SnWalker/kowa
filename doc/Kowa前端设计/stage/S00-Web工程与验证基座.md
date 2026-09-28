# S00：Web 工程与验证基座

> 状态只在本端总体设计维护；执行事实进入 record/S00.md。

## 阶段设计依据

当前无 package.json 或 Web 工程；必须先固定 React/TypeScript 基座、测试层次和可访问的页面壳。

## 现行勘误（仅 REOPENED 时填写）

不适用：普通绿地阶段。

## 唯一工程目标

建立可构建、可测试、可路由且具备基础可访问性的 React 18/TypeScript 5 Web 工程基座。

## 前置与跨端依赖

- 本端前置阶段：无。
- 跨端阶段：无。
- 共享契约 owner：不适用。
- 冻结契约及版本：不适用。

## 必读

AGENTS、common、前端总体设计/接力、MVP信息架构、前端验证规则、web-preview-reference/index、MVP 流程。

## 推荐 Skills

- `vercel-react-best-practices`
- `vercel-composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

未来 package.json、pnpm lock/workspace、src/app、路由、测试、构建和 E2E 配置。

### 允许修改

工程脚手架、设计 token、通用布局、错误边界、路由壳、测试工具、lint/typecheck/test/build/e2e 命令。

### 明确禁止

业务页面实现、复制预览示例数据、多模板入口、前端业务状态机或发明 API 字段。

### 并行写入范围

独占 Web 工程根和前端验证入口；不修改后端工程或共享合同。

## 当前问题与证据

- CONFIRMED：前端版本、页面范围和预览用途已确定。
- CODE_CONFIRMED：package.json、pnpm-workspace 和 src 不存在。
- DATA_CONFIRMED：不适用。
- STRONG_INFERENCE：路由/测试 seam 先稳定可降低后续页面重复。
- HYPOTHESIS：具体组件库和断点需在本阶段验证后记录。
- 文档与源码差异：信息架构存在，工程未建立。
- 证据不足项：浏览器兼容与视觉基线。
- 固化旧错误的测试：无。

## Producer、Transition、Consumer

纯绿地；路由/应用壳生产页面上下文，后续功能页面消费。

## 本阶段新增或修改的模型

只建立 UI 基础类型、主题和导航 seam，不建立领域状态模型。

## 约束

预览图不是像素基线；可访问性从基座开始；Mock 不冒充正式合同；会话/权限不存 localStorage。

## 实施清单

- [ ] 以缺失 package/build/test 入口建立红灯。
- [ ] 建立应用壳、路由、主题、错误/加载状态。
- [ ] 固化 lint/typecheck/test/build/e2e。
- [ ] 验证键盘导航、焦点和最小响应式壳。

## 测试与自动验证

### Characterization 与目标红灯

Characterization 不适用；test -f package.json && test -d src 初始应失败。

### 定向测试

mise exec -- pnpm test

### 受影响回归、构建与静态门禁

pnpm lint/typecheck/test/build、最小 E2E、文档门禁、git diff --check。

## 运行时验收

本机打开应用壳，核对路由、刷新、错误边界、键盘焦点和窄屏布局；不连接真实后端。

## 人工操作边界

不适用。

## 退出条件

所有固定脚本可复跑，生产构建成功，最小 E2E/可访问性检查通过，record 完整。

## 停止点

完成后停止，不实现登录或 Workspace。
