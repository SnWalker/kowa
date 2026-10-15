# S03：UI 基座与 GitHub 主题

> 状态只在总体设计维护；执行事实进入 record/S03.md。
> 2026-10-16 前端阶段重排：新增本阶段，原 S03—S06（DAG/历史、HumanTask/恢复、PR/证据/关闭、可访问性与真实控制面对接）顺延为 S04—S07；更早文档中的旧编号按当时语义阅读。

## 阶段设计依据

用户已明确指令：参考历史预览页完成本项目 Web 实现，增加 GitHub 风格主题（在复用基础上只改样式），严禁手搓组件、使用最佳开源组件样式库。视觉方向由用户 pin 为 GitHub 风格，`frontend-design` skill 负责执行质量（字阶、间距、反模板化），不改变该方向。当前 `src/styles/global.css` 与 `tokens.css` 是 S00 蓝图期的工具无关手写样式，页面由各阶段各自堆叠类名；本阶段把全站表现层收敛到统一的组件库与主题令牌上，使后续阶段不再手搓基础控件、不再各自定义视觉常量。

## 现行勘误（仅 REOPENED 时填写）

不适用。

## 唯一工程目标

引入 Tailwind CSS v4 + shadcn/ui 作为唯一基础组件来源，把 GitHub 开源设计令牌（`@primer/primitives` 的调色板、字阶、间距、圆角、边框）映射为 Tailwind 变量与 shadcn 主题契约，并把 S01/S02 已交付的全部页面迁移到该基座：行为与契约不变、可访问语义不变，仅表现层改变，并按 `web-preview-reference` 的信息架构校准页面骨架与视觉质量。

## 前置与跨端依赖

- 本端前置阶段：S02。
- 跨端阶段：无（本阶段不新增跨端契约，不新增任何 HTTP 消费）。

本阶段不新增消费操作，因此不建立第二份消费绑定：既有 S01/S02 的 9 个 `http` 操作及其双 pin（manifest 文件 SHA-256 与 `schemaSha256`）继续是该事实的唯一权威，见 [S01 消费准入证据](../record/S01-消费准入证据.md) 与 [S02 消费准入证据](../record/S02-消费准入证据.md)。迁移只替换表现层，禁止改动 HTTP 消费路径、fail-closed 解码、错误类别呈现、CSRF、幂等/`requestKey`/草稿、409 处理、缓存键隔离（identity/workspace/cacheEpoch/generation）与 StrictMode/BroadcastChannel 语义。开工时按固定恢复入口复核这些 pin 在当次 HEAD 上仍然有效，并把复核作为证据写入 record（复核不构成重新绑定）。

## 必读

`doc/common.md` 与 `AGENTS.md` 的共识与纪律；[Web 工程基座](../Web工程基座.md)；[前端验证规则](../验证规则.md)；[MVP 信息架构](../MVP信息架构.md)；[预览索引](../web-preview-reference/index.md) 与两组预览图；[record/S01](../record/S01.md)、[record/S02](../record/S02.md) 与两份消费准入证据；`frontend-design` skill 全文（`SKILL.md` 的计划 → 对照 brief → 构建 → critique 流程）。

## 推荐 Skills

- `frontend-design`
- `react-best-practices`
- `composition-patterns`
- `webapp-testing`

## 核心文件与修改范围

### 核心文件

`package.json` / `pnpm-lock.yaml` / `components.json` / `vite.config.ts` / `tsconfig.json`（组件库与主题工具链）；`src/styles/`（Tailwind 入口与 GitHub 令牌映射）；`src/ui/`（shadcn/ui 组件源码与共享组合组件）；`src/app/{AppShell,App,ThemeSelect}.tsx`、`src/auth/LoginPage.tsx`、`src/workspaces/*`、`src/runtimes/*`、`src/workitems/*`（页面表现层）；`tests/unit/`、`tests/e2e/`（仅最小选择器更新与视觉/可访问性验证）。

### 允许修改

组件库与主题的引入与配置；全站 DOM 结构中的容器、类名与布局；共享 UI 组合组件（`Page`/`Section`/`Notice`/`Badge`/`DataList`/`EmptyState`/`LoadingState`/`ErrorBoundary`）的内部实现；标题层级、间距、圆角、边框、状态色、等宽代码字体与图标；浅色/深色两套令牌；因 DOM 结构变化而必须更新的测试选择器。

### 明确禁止

手写按钮/输入框/下拉/复选/对话框/表格/徽章/提示/标签页等基础组件（一律取自组件库）；改动 HTTP 消费、严格解码、错误类别、CSRF、幂等键、版本冲突处理与缓存键语义；夹带新功能、新页面、新路由或新业务语义；照抄预览图中的示例业务数据、旧品牌文案与环境信息；把预览图当像素级验收基线；提交 PNG/PDF 预览制品；修改后端生产代码、冻结合同、迁移或后端依赖。

### 并行写入范围

本阶段独占全部前端表现层与主题令牌；不与他人共享写范围，也不触碰 `doc/common.md`、`doc/decision.md`。

## 当前问题与证据

- CONFIRMED：用户指令（GitHub 风格主题、开源组件库、参考历史预览页）已由 Coordinator 核定；预览 `index.md` 已入库，24 张 PNG 为本地制品、被 `.gitignore` 排除。
- CODE_CONFIRMED：进入时前端无任何组件库依赖（`package.json` 仅 `react`/`react-dom`/`react-router-dom`），样式集中在 `src/styles/global.css`（工具无关手写类名）与 `src/styles/tokens.css`（自定 `--color-*`），页面各自拼装类名。
- DATA_CONFIRMED：迁移前绿色基线已冻结（单测 140 例通过、fixture 三引擎 E2E 120 passed／33 skipped／0 failed、真实控制面三引擎 E2E 102 passed／0 failed、axe 用例通过），命令与退出码见 record/S03.md。
- STRONG_INFERENCE：既有 E2E 依赖的是可访问角色与名称（`getByRole`/`getByLabel`/`getByText`）而非类名，保持语义即可让同一批旅程在换基后继续通过；仅因 DOM 结构变化而失败时做最小选择器更新，保留原语义断言。
- HYPOTHESIS：引入组件库与 Radix primitives 后产物体积增长可接受；须在 `pnpm build` 实测并在 record 记录前后对比，不做"未测量即通过"的判断。
- 文档与源码差异：进入时逐项核对，无差异。
- 证据不足项：真实屏幕阅读器验收与人工视觉抽查仍属 S07；本阶段以 axe、截图与视觉冒烟清单为准，不声称已通过真实读屏验收。
- 固化旧错误的测试：无（既有测试保护 S01/S02 的契约与旅程行为，不固化样式细节）。

## Producer、Transition、Consumer

用户与 Coordinator 冻结视觉方向（GitHub 风格）与组件库选型（producer）；本阶段把页面 DOM 与样式从手写 CSS transition 到组件库与主题令牌，行为与契约保持不变（transition）；S04 起的后续阶段与用户在新的组件/主题上继续实现与使用（consumer）。本阶段不产生任何业务事实。

## 本阶段新增或修改的模型

不新增业务模型。新增的是表现层模型：GitHub 主题令牌映射（浅色=GitHub Light、深色=GitHub Dark，含中性灰层级、细描边、紧凑表格与表单、蓝色主色、等宽代码字体、克制状态色）与入仓的组件库源码（shadcn/ui）。前端仍不拥有任何 WorkItem/Run/NodeRun/HumanTask 状态权威。

## 约束

- 行为不变：HTTP 消费、fail-closed 解码、错误类别、CSRF、幂等/`requestKey`/draft、409 处理、缓存键隔离、StrictMode/BroadcastChannel 语义全部保持。
- 语义不变：可访问名称、角色、标签与文案语义保持（E2E 依赖 `getByRole`/`getByLabel`）；双主题（system/light/dark）与 `data-theme` 契约保持。
- 仅表现层改变，不夹带新功能或新语义。
- 不得手搓基础组件；一律取自组件库。不得绕过主题令牌写裸色值或裸像素。
- 预览图只用于信息架构与布局校准，不是像素级验收标准。
- 不提交 PNG/PDF；不修改后端生产代码、冻结合同与迁移。

## 实施清单

- [x] 按固定恢复入口完成开工审计（AGENTS 哈希、HEAD、预存工作区、文档一致性与既有 pin 复核）。见 record 的"开工事实/开工验证"。
- [x] 冻结迁移前绿色基线并记录命令、退出码与数量（单测 140、三引擎 fixture E2E 120 passed／33 skipped）。见 record"开工验证"。
- [x] 落成设计计划（色板 4—6 值、字体、布局概念、关键页面骨架 ASCII 稿）并对 `web-preview-reference` 校准，先写文字再写码。见 record"设计计划"。
- [x] 引入 Tailwind CSS v4 与 shadcn/ui，生成 GitHub 主题令牌映射，保留 light/dark 切换与既有 a11y 断言。见 record"执行结果"与 [Web 工程基座](../Web工程基座.md) 的 S03 换基节。
- [x] 把共享 UI 层迁移到新基座（`src/ui/`：入库组件 + Kowa 组合件）。
- [x] 迁移全部 S01/S02 页面（登录、Workspaces、设置、Runtime、工作项列表/创建/详情与知识绑定）与 AppShell/导航/空态/错误态；E2E 规格零改动，只有夹具与 jsdom 桩各一处。
- [x] 以视觉红灯（逐页截图人工核对与视觉冒烟清单）驱动表现层收口，逐页截图证据见 [record/evidence](<../record/evidence/>)（本阶段未引入像素级截图比对，理由见 record 的偏差节）。
- [x] 跑通三引擎 fixture E2E、真实控制面 E2E、axe、构建与文档门禁。命令、退出码与数量见 record 的"自动验证命令"节（本机高负载时段的 WebKit 超时与真实控制面复跑一并记录）。

## 测试与自动验证

### Characterization 与目标红灯

characterization 适用且必须先行：迁移前冻结既有绿色基线（`pnpm test` 单测、fixture 三引擎 E2E、`scripts/web-e2e-control-plane.sh` 真实控制面 E2E、axe 用例），作为"行为不变"的比较基线。该基线按阶段前置存在，不是绿地。迁移中的目标红灯是表现层偏离（页面骨架与 GitHub 风格不符、主题令牌缺失或双主题下对照度/层级错误）；仅因 DOM 结构变化而失败的测试做最小选择器更新并保留原语义断言，禁止弱化覆盖。

### 定向测试

mise exec -- pnpm test -- foundation work-items workspace-settings runtimes

### 受影响回归、构建与静态门禁

`pnpm lint`／`typecheck`／`test`／`build`、三引擎 fixture E2E、`bash scripts/web-e2e-control-plane.sh`（真实控制面）、axe 可访问性、`bash scripts/check-doc-governance.sh`、`bash scripts/check-doc-governance-test.sh`、`git diff --check`、`git status --short`。

## 运行时验收

对隔离控制面（隔离 PostgreSQL 17.11 + 本地 OAuth stub + 受控 bare 仓/registry + 脚本化 Runner 传输 + 生产构建同一源）执行与 S01/S02 相同的旅程，验证换基后 HTTP 消费、fail-closed 解码、错误类别、409 与幂等行为不变；每页留存截图证据（含明暗主题、空态/错误态/陈旧态与知识绑定 RECOMMENDED→CONFIRMED），不声称真实 Provider 或真实 GitHub 写入已通过。

## 人工操作边界

不安装或授权真实 GitHub App、不触发真实 WorkflowRun、不合并 PR、不执行真实迁移与部署。视觉抽查可由用户执行，本阶段不要求用户操作，也不把阶段状态置为 `HUMAN_ACTION_REQUIRED`。

## 退出条件

全站页面在生产构建下由组件库与 GitHub 主题令牌渲染，浅色/深色两套令牌齐备；单测、三引擎 fixture E2E、真实控制面 E2E、axe、构建与文档门禁全部通过且测试数量不下降；既有 9 个 `http` 操作的双 pin 与消费者集合不变；预览图信息架构已校准，视觉偏差与未完成项逐项记入 record/S03.md；`currentStage` 翻转为 S04。

## 停止点

完成后停止：不实现 DAG/历史（S04）、HumanTask/恢复（S05）、PR/证据/关闭（S06）或响应式/真实控制面收官（S07），不新增页面、路由或业务语义。
