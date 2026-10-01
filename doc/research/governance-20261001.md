# 2026-10-01 合同与 AI 工作流治理修订记录

本页仅保存获批跨阶段治理事实，不拥有阶段状态、正式 API 或业务决定。权威入口仍为 AGENTS、两端总体设计与各 stage。

## 来源与边界

- HEAD/main：eed56d78f24875d05e04476b882784d719e72fce；施工位于 /Users/liufei/.codex/worktrees/frontend-s01/kowa、codex/frontend-s01。
- main/PR #8 已重新核验合入，最终 head fb2b9d9a2c0d85c83d6063a7cb597b4ecc0ab88a 和 main 三项 CI成功；原交付树相同。没有本轮提交或远端 CI。
- 预存四份前端审计成果先快照、再接续；原 WIP HEAD a8d997e0ec2ec1e1623a4c3e34e2c82ba352b69d 的 41 文件、S03 交付工作区不施工。
- 用户本会话批准有限审计、责任与依赖修订及短工作流/门禁增强。不自动 commit、push、创建 PR、部署或登记状态。

## 审计事实与分类

CONFIRMED（阶段与正式 README/schema 交叉）：frontend:S01 只依赖身份 v1不足以交付 Runtime和会话恢复；S02 所需 WorkItem/固定流程入口不在 workflow-execution 核心，S03 明确不含 HTTP/HumanTask；前端 S03 查询/历史与 S04 全恢复需 S06 组合交付。归类为规划漏项，不自动重开。

CODE_CONFIRMED：identity.WebIdentity 无 JSON tags，callback 直接序列化。上一轮临时 overlay 实际得到 identity={GitHubUserID:101,Login:member-a}，不符合冻结 githubUserId/login，目标退出1。S01 为最早原合同违约责任。

CODE_CONFIRMED：MemoryStore.RegisterRuntime 同 epoch 只比较机器 ID和 capabilities，PostgreSQL 还比较 Provider ID/version/authenticated。上一轮三个临时目标探针均返回 nil；认证 false 报告后 LeaseTask 仍领取 task-1，目标退出1。S02为独立后续原合同违约责任。该数据是隔离内存探针，不代表真实 PostgreSQL 同样错误；下一返工会话须复现并建立永久目标矩阵。

CODE_CONFIRMED：Server 组合根只装配 health；新 HTTP交付须验证装配，不能仅用 handler fixture。S03 stage 的无实现/未冻结描述已过时，本轮勘误，不重开。未发现新的原合同违约或固化旧错误测试。

## 实际修订

短规则明确交付层次、逐操作消费绑定、实际 producer/组合根、多适配器拒错后合法请求、缺口分类与受限消费者预检；审计/获批文档治理可同会话接续，生产实施仍一阶段。

S01/S02 记录责任与下一返工边界；S04补 Runtime/执行配置，S05补 WorkItem基础及人工/交付模块，S06补完整 Web/知识/恢复旅程；前端保留全部目标，修订具体依赖并用待冻结消费块记录需求。stage 表与消费绑定一致、依赖全图无环；所有阶段状态和 reopen 对象不变。

新增依赖/消费检查器是绿地工具，characterization 不适用；旧治理自测在修改前通过，作为旧合法状态的回归基线。新增14个测试先因缺少检查器全部红灯（子进程exit2、suite exit1），实现后绿灯，随后增加缺失绑定和schema篡改反例，最终16例。JSON缺失/空值变异改为结构操作，确保恰好命中一字段；补嵌套重开恢复周期。未增加新的阶段状态。

## 验证

| 命令/检查 | 实际结果 |
| :--- | :--- |
| 修改前 bash scripts/check-doc-governance-test.sh | exit0；原4正/12反重开、8 JSON反例、3跨端反例及既有fixture通过 |
| 新检查器红灯 python3 scripts/check-stage-contracts-test.py | suite exit1；初始14例因尚无检查器失败，子进程exit2，绿地工具红灯 |
| 最终 bash scripts/check-doc-governance-test.sh | exit0；重开7正/12反（含暂停owner恢复与完成）、JSON8反、跨端3反及既有fixture；合同绑定16例（3正/13反）通过 |
| mise exec -- make verify | 最终exit0；格式、Go测试9包（输出未统计逐test数量，部分cache）、2进程场景、vet/build、迁移结构、3合同、模块及治理自测通过 |
| bash scripts/web-env.sh pnpm lint/typecheck/test/build | 四命令均exit0；Vitest 1文件/6测试通过，生产构建通过 |
| bash -n 两治理脚本；Python AST检查 | exit0，无语法错误 |
| 修改文件链接/空白检查；git diff --check | exit0；33文件、32个相对链接无缺失；新增文件另查换行与空白 |
| 状态/保护核验 | 两端状态枚举、frontier和reopen JSON与HEAD逐项相同；common/decision及Go/pnpm依赖文件与HEAD相同；原后端41文件与S03干净交付的HEAD/状态/暂存/摘要不变 |

中间失败保留：前端lint首轮exit1，因为工作区node_modules缺失；按锁文件 pnpm install --frozen-lockfile exit0 后四门禁通过，未改依赖版本。首次接入新消费fixture产生尾随空格，治理exit1/make exit2；改为整行结构替换后通过。S02历史勘误标题曾不符合模板，make exit2；恢复规定标题且保留准确历史说明后最终make通过，没有放松门禁。

无路由/认证/样式生产变更，未补跑浏览器E2E；未写数据库或修改迁移，未重跑PG集成，未调用真实OAuth/Provider。上述绿色只证明本轮治理修订与回归，不验收未来API或页面，不修复S01/S02生产缺陷。所有本轮修改仍未提交，无本轮最终head远端CI。

## 后续操作

先在本会话完成审阅与获准 Git交付；合入后新开“backend:S01 回调 wire 与会话交接/恢复”，只登记/执行最早责任。S01退出后新会话S02，再S04。前端旧会话暂不实施，待S04完整退出、操作合同/摘要/证据冻结后重新准入。

下一后端会话提示词：从最新已合入 main按 AGENTS固定入口恢复；保护原41文件WIP及frontend-s01未提交成果。核验本轮治理成果已交付且来源明确，若未交付先停止实施。仅处理backend:S01：复现callback字段违约，依据原冻结合同登记S01 REOPENED，保存S02/S03 DONE及S04—S08 NOT_STARTED真实后继基线和activeReopen；不得同时重开S02。先characterization与目标红灯，再完成批准的会话交接/刷新恢复和真实Server装配。路由版本/算法由S01明确冻结，不预设整体v2迁移。隔离测试，无真实App授权/生产写入。完整门禁与必要最终head CI后退出、停止；S02责任留下一会话。Git写操作须由本任务另行明确授权。

## 获准 Git 交付（2026-10-01）

用户明确授权提交、推送本轮治理与阶段合同修订并创建 PR，禁止合并、生产阶段实施或重开登记。前述未提交描述为本地校验时水位。交付只含本工作区已审阅的33文件；完整最终提交 SHA、PR、run/job及最终 head CI 结果保存在本 PR 描述，避免为自引用SHA反复改变head。若CI失败仅修本轮治理边界，不进入生产阶段。
