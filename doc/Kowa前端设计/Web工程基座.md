# Web 工程基座

S00 的端内实现说明；阶段状态只看 [总体设计与进度](总体设计与进度.md)。

## 工程入口与复跑

根目录是唯一 pnpm workspace 包，`src/main.tsx` 挂载 React 18，`src/app/App.tsx` 定义 `/` 与兜底未知路由。没有登录、Workspace 或其他业务页面，没有 API 调用或领域状态。

```bash
mise install
bash scripts/web-env.sh pnpm install --frozen-lockfile
bash scripts/web-env.sh pnpm dev
```

`mise.toml`、package.json engines 和 packageManager 固定 Node 22.23.2 / pnpm 10.34.5；React 固定 18.3.1、TypeScript 固定 5.9.3。所有直接依赖精确版本与 pnpm-lock.yaml 一并提交。`scripts/web-env.sh` 仅在子进程 PATH 前置项目 mise 路径，解决本机 shell 重排 PATH 时 pnpm/子脚本意外使用全局 Node 的问题；不修改全局工具。正常 PATH 环境可直接 `mise exec -- pnpm …`。

## 最小组合接口

- `AppShell` 负责顶栏、主导航、main、跳过导航、路由焦点与标题；`Outlet` 接入后续页面。
- `Page` 接受 `title` 与 `children`，不按业务类型堆叠布尔开关。
- `EmptyState` 接受标题与任意 children；`LoadingState` 提供可读的 live status。
- `ErrorBoundary` 隔离渲染异常，聚焦提示，隐藏内部异常细节；重试后聚焦恢复容器，下一次 Tab 可进入内容。路由内容以 location.key 为边界，导航时清除旧页面错误。它不替代未来 API 错误处理，无法捕获事件处理器或异步请求异常。
- 主题只有 system/light/dark 三种外观值，CSS token 定义背景、文字、边框、焦点、间距与圆角；不写 localStorage/sessionStorage，也不承载会话/权限。

默认遵循系统主题。导航使用原生链接/React Router，设置 aria-current；main 在路由变化时接收焦点，首次加载保留自然 Tab 顺序。48rem 以下侧栏移至主区上方，布局不使用固定页面宽度。此断点是工程验证范围，不是预览图片像素合同。

## 验证与边界

```bash
bash scripts/web-env.sh pnpm lint
bash scripts/web-env.sh pnpm typecheck
bash scripts/web-env.sh pnpm test
bash scripts/web-env.sh pnpm build
bash scripts/web-env.sh pnpm exec playwright install chromium firefox webkit
bash scripts/web-env.sh pnpm test:e2e
bash scripts/check-doc-governance.sh
git diff --check
```

组件测试使用 Vitest + Testing Library；E2E 使用 Playwright 的 Chromium/Firefox/WebKit。生产构建在 127.0.0.1:4173 验收；测试夹具另在 4174，仅挂载生产通用组件，允许触发渲染异常。夹具不是业务 Mock，没有正式 API 假数据，且不进入生产构建。E2E 默认拒绝复用已有服务，避免误验另一工作区。

axe 检查明暗主题、未知路由与错误提示，另验证键盘顺序、路由刷新/前进/后退、320px 窄屏及 200% 文字。自动检查不等于真实屏幕阅读器验收；后续复杂页面仍需按各自阶段合同验收。

GitHub Frontend workflow 的 `web-verify` 执行相同门禁与三引擎 E2E；既有 Backend workflow 原样保留。S00 将 `web-verify` 作为阶段退出必需证据，不擅自变更仓库 Ruleset。

部署不在本阶段范围。未来静态服务器须将非资源页面路径回退到 index.html，保留真实静态资源缺失错误；本机使用 Vite preview 验证 SPA 回退。

技术事实参考：[Vite 工程与版本要求](https://vite.dev/guide/)、[React Router 声明式安装](https://reactrouter.com/start/declarative/installation)。本项目保持 React 18，不采用 React 19 专用 API。

Mac WebKit 的键盘遍历验收使用 Option+Tab，其他平台使用 Tab；这是浏览器平台行为，不向应用加入键盘拦截。相关上游说明见 [Playwright WebKit tabbing](https://github.com/microsoft/playwright/issues/5609)。
