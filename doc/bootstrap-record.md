# Kowa pre-S00 引导记录

本文只记录首个正式阶段建立前的治理引导事实，不拥有后端或前端阶段状态。

## 2026-09-24 / 治理与方法迁移

- 开始 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`
- 预存工作区：`.gitignore`、`.workbuddy-ai/`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 均为未跟踪内容；未回滚、清理、暂存或提交。
- 实际修改范围：Kowa 治理文档、共识结构、阶段与 record 模板、前后端状态骨架、文档治理门禁及其自测试；未修改生产代码。
- 证据来源：Kowa 已确认共识；`doc/blog/ai_develop_workflow/` 方法论、规则样例和 RAG 工程档案；三组独立 Subagent 的迁移、内部一致性与领域模型审查。
- 验证：`bash -n scripts/check-doc-governance.sh scripts/check-doc-governance-test.sh`、`scripts/check-doc-governance.sh`、`scripts/check-doc-governance-test.sh`、未跟踪文件逐文件空白检查。
- 当前结论：pre-S00 治理骨架可以用于定义首个正式阶段；在各端 `currentStage` 与状态行建立前不进入生产代码施工。

## 2026-09-25 / 已确认 MVP 设计首次落盘

- 授权：用户逐项确认 MVP 范围、历史设计优先参考、后端基线、知识绑定、返修与关闭政策后，明确“解除最初限制”；本轮授权仅落实文档与治理，不创建开发阶段、生产代码、提交或真实操作。
- 开始与结束 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`。
- 预存工作区：`.gitignore`、`.workbuddy-ai/`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 均为未跟踪内容；未清理、回滚、暂存或提交。
- 开工事实：`CONFIRMED` 两端仍为 pre-S00；`currentStage`、`activeReopen`、`suspendedReopen` 全为 null，状态表为空。`CODE_CONFIRMED` 历史 demo 只有能力侧与两条测试源码；缺少完整领域/应用代码和依赖，未运行历史 Go 测试。
- 文档差异：原接力直接指向首个阶段合同，未反映仍需收敛设计；本次改为设计入口与实际下一动作，未改变阶段状态。common/decision 只写此前已批准的结论；状态/Task 身份等技术细化保留草案。
- 实际修改边界：共 20 个文件（12 个既有文件更新、8 个设计/验收文件新增）。更新 `.gitignore`、common、decision、wiki 总索引及 design/domain 索引、两端总体设计导航与接力、跨端进展指针、本记录；新增 MVP 流程、生命周期草案、契约草案、GitHub 草案、两端设计、验收矩阵和设计审阅目录。
- 工作区保护：编辑前对 12 个既有文件建立临时字节副本，用于比对预存内容；新增内容均在当前 Kowa 内。历史项目、AGENTS.md、mise.toml、脚本和生产代码未修改。
- 忽略规则：收窄根 data/artifacts/logs/tmp/worktrees 等目录；保留嵌套源码、测试夹具与环境模板；忽略本地 AI memory、私密目录、TS 缓存和 E2E 输出；团队 IDE 文件仅放行候选，不代表其中内容可免审。
- 验证命令：`bash -n scripts/check-doc-governance.sh scripts/check-doc-governance-test.sh`，退出 0；`./scripts/check-doc-governance.sh`，退出 0，`DOC_GOVERNANCE_PASS`；`./scripts/check-doc-governance-test.sh`，退出 0，`DOC_GOVERNANCE_TEST_PASS`（源码包含 8 处负向断言，本次输出 6 次正向门禁通过）；`git diff --check`，退出 0。
- 定向核验：内联 Python 检查 20 个目标文件的空白/末尾换行、62 个本地链接；逐例执行 `git check-ignore --no-index -q <path>` 核验 35 个应忽略/应保留路径；比对两端 frontier/reopen 和接力/投影 JSON，检查 stage/record 为空。退出 0，`DESIGN_AUDIT_PASS`。
- 定向样例包含：根运行数据应忽略，`internal/data/example.go`、`doc/wiki/data/example.md`、`testdata/artifacts/sample.json`、`testdata/expected.out`、`testdata/errors.log`、`.env.test.example`、锁文件和迁移源码应保留。
- 限制：`git diff --check` 不覆盖未跟踪文件，故另做目标文件空白与链接检查。28 项业务验收只是目标断言，未执行业务测试、构建、迁移、GitHub 授权或真实 WorkflowRun。Characterization 不适用：无 Kowa 生产行为改动，不虚构旧行为测试。
- 未完成项：用户将提供的 3 篇文档、Workflow/Capability 扩展与 wire 冻结、身份权限与凭证隔离、Provider/CI 真实兼容性、容量和证据保留参数。详细清单见 `doc/research/mvp-design-review.md`。
- 当前结论：本次设计与治理落盘完成，不代表 MVP 设计全部冻结，更不代表实现完成或阶段退出；两端继续点见各端接力文件。

## 2026-09-25 / 三篇历史指南核对与扩展设计

- 授权与范围：延续用户解除只读限制后的文档设计任务，按本轮明确提供的三个路径甄别参考；不修改历史项目，不更新 common/decision，不创建阶段或生产代码。
- 开始与结束 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`。
- 预存工作区：`.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 为未跟踪内容；上一轮文档均为预存内容，本轮先快照相关文件再增量修改，未回滚、暂存或提交。
- 已确认事实：完整读取用户给定材料 446 + 165 + 191 = 802 行；定向检查 23 个代码路径，4 个现存、19 个缺失。CODE_CONFIRMED：现存 Makefile 的 check 只执行 build/guards；demo 能力支持非阻塞输入；当前两条 YAML 显式节点 ID 数为 20 和 14。节点文本计数不是编译通过证据。
- 文档差异：总览节点数量不能代表当前快照；Agent 示例代码截断、能力 ID 命名混杂、只跑 make check 不足以验证新增能力；旧入站 push 与 Kowa 出站约束需要适配。核心 loader/compiler/SDK 缺失，未把多篇 AI 文档互引算作独立实现验证。
- 实际修改边界：9 个文件，新增指南核对与 Workflow/Capability 扩展设计 2 篇；更新设计审阅目录、契约草案、后端模块设计、后端接力、MVP 流程导航、验收矩阵和本记录 7 篇。未改 wiki 分类权威范围，新增候选方案均在 research，不进入正式 contracts。
- 采用设计：分层加载/编译/准入、冻结定义、控制依赖与数据绑定、统一运行器、静态能力目录与在线注册分离、非阻塞交互及有界返修。仅内置 YAML 发布、受限表达式、精确版本与出站长轮询等保留为待审技术选择，不擅自升级共识。
- 测试边界：无生产行为修改，characterization 不适用；新增 B01—B12 共 12 个验收目标，全矩阵为 40 项，均未执行。历史 Go 测试未运行，不安装缺失依赖。
- 自动验证：`bash -n scripts/check-doc-governance.sh scripts/check-doc-governance-test.sh`、`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、`git diff --check` 均退出 0；分别获得 DOC_GOVERNANCE_PASS 与 DOC_GOVERNANCE_TEST_PASS。自测试仍为 8 处负向断言、6 次正向门禁通过。
- 定向核验：内联 Python 检查 9 个目标文档空白与末尾换行、63 个本地链接、40 个唯一验收编号；比对 8 个受保护文件字节摘要（含 AGENTS/common/decision/阶段状态权威）及后端接力 JSON；stage/record 为空。退出 0，GUIDES_DESIGN_AUDIT_PASS。未跟踪文件另行检查，不依赖 git diff --check 的覆盖范围。
- 残余风险与下一动作：资料已到位；继续审阅定义格式/发布、最小节点和表达式、目录准入、出站传输及 Task 身份，然后冻结 schema。权限、CI/Provider 真实兼容性及运行预算仍待核定。当前仍为 pre-S00，不产生阶段退出结论。

## 2026-09-25 / 设计收敛、身份边界与治理门禁修复

- 授权：用户要求根据已有信息完成可推进事项，并在 Q16 指定首期只做一条固定 workflow、后续新流程复用能力且旧流程可增加 Agent 节点；Q17 批准新执行新 Task、重复投递复用；Q18 批准出站 HTTP 长轮询。已批准部分写入 common/decision，作者格式与 wire 等未被顺带视为全部批准。
- 开始与结束 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`。
- 预存工作区：`.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 为未跟踪内容；上一轮设计与脚本均先保存临时副本再增量修改。未修改历史项目、提交、暂存或安装依赖。
- 开工事实：CONFIRMED 两端仍为 pre-S00，状态表空；CODE_CONFIRMED 治理 jq 将缺失属性视为 null，提取时空字符串也变成未开始。文档另有确认事件混入不可变快照、摘要混入唯一查找键的歧义。
- Producer/Transition/Consumer：文档 JSON 生产状态字段；extract_json_block 和 jq 解释字段；validate_track/投影核对消费解释后的前沿。缺失与 null、空串混同导致门禁误放行。修复仅加强形状校验，不伪装为完整状态机证明。
- Characterization：保留既有 pre-S00/普通阶段/重开/嵌套重开的正向自测试。原测试未覆盖字段缺失与空串；新增 8 个保持 JSON 语法合法的负向 fixture 作为目标红灯。
- 红灯：首次 `./scripts/check-doc-governance-test.sh` 退出 1，输出 `JSON_SCHEMA_TEST_FAILED: 8 unexpected pass(es)`；6 个字段缺失和2个空前沿在旧门禁中均被接受。fixture 只在临时目录中，未建立真实开发阶段。
- 最小修复：要求 JSON 为对象、显式包含前沿/重开槽位/投影字段，非 null 的阶段引用符合 Sxx。未更换脚本语言或引入依赖，未扩展为生产代码施工。
- 设计完善：新增固定流程与执行边界、执行尝试行为合同；身份/权限/凭证隔离草案成稿；拆分知识内容与确认事件，区分候选产物发布与批准使用，修正幂等唯一键和定义名/版本唯一性。补 Provider 适配验证清单，前端与接力去除多模板和已批准事项的待审表述。
- 实际修改：21 个文件，18 个既有文件更新、3 个新文档。包括两份治理脚本、common/decision、相关索引、领域/契约/扩展/GitHub/审阅设计、两端设计与接力、验收矩阵和本记录。AGENTS、mise、.gitignore、进展投影和两端状态权威未改；接力 JSON 不变。
- 验证：`bash -n scripts/check-doc-governance.sh scripts/check-doc-governance-test.sh`、`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、`git diff --check` 均退出 0。自测试输出 `JSON_SCHEMA_TEST_PASS: 8 negative cases` 与 `DOC_GOVERNANCE_TEST_PASS`；合计 16 个负向用例（原 8 + 新 8），6 次正向门禁通过。
- 定向核验：内联 Python 检查 22 个候选目标文件（含1个未变化索引）、92 个本地链接、6 个受保护文件摘要、两端接力 JSON、空 stage/record 与48个唯一验收编号，退出0，`REFINEMENT_AUDIT_PASS`。未跟踪文件另查空白与末尾换行，不以 git diff --check 覆盖全部为假设。
- 业务验证限制：C01—C08 新增8个目标断言，总矩阵48项，未执行业务验收。没有 Kowa 业务工程，不运行不存在的构建/迁移，也不把脚本自测试算作 MVP 验收。
- 剩余项：NodeRun 状态/轮次细节、作者格式/目录 schema、wire 路由/时间参数、登录初始化与人员授权、执行隔离、CI/Provider 真实验证和容量/保留参数。独立复核、完整跨端依赖准入及历史转换检查仍不能由现有结构门禁自动证明。
- 收工结论：已完成本轮授权设计与治理修复；无开发阶段退出或推进，后续依据更新后的两端接力继续。

## 2026-09-25 / 开发前 MVP 设计清单与本机验收方案

- 授权与范围：用户要求列出并推进正式开发前设计任务，允许参考历史项目、继续完善 Kowa 文档；本轮不建立 S00、不更新 `currentStage`、不编写生产代码或执行真实 WorkflowRun。
- 开始与结束 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`。预存工作区为 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪；未清理、暂存、提交、推送或修改历史项目。
- 已确认输入：小团队多人使用；GitHub App Web 登录和服务端会话；人在 GitHub 提交适用于当前 PR 提交的 Review 并合并；GitHub App 受控发布；开发及首条真实验收均在本机 Mac 且只允许同机浏览器；首条一个 Server 和一个独立 Runner，后续同机第二 Runner 专项验证交接。Agent/测试沙箱与 Provider 账号归属仍待用户选择。
- 历史证据：定向核对历史 Runner 为独立进程的资料；历史本机 Git/CLI 身份共享，未证明任务沙箱。当前 Mac 已安装 Docker Desktop，但 daemon 未启动；Kowa 无服务、Runner、Web 工程或真实运行证据。独立进程与隔离验证分开记录。
- 实际修改：新增开发前任务清单、真实集成验收方案、质量观测设计、`common.md`/`decision.md` 拟写稿及 research 下候选 schema 说明；完善流程图、生命周期候选、跨端契约与错误语义、GitHub 交付、身份边界、后端关系 schema 候选和前端信息架构；更新相应 wiki 索引与设计审阅目录。先前放在正式 contracts 的未批准机器 schema 迁回 research，移除临时样例校验脚本及批量样例。未修改 common/decision、阶段状态权威或接力状态 JSON。
- 验证：`bash -n scripts/check-doc-governance.sh scripts/check-doc-governance-test.sh`、`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、`git diff --check` 均退出 0；门禁输出 `DOC_GOVERNANCE_PASS`、`DOC_GOVERNANCE_TEST_PASS`。这些只验证治理文档；`git diff --check` 不覆盖未跟踪文件。未执行业务构建、数据库迁移、Provider/沙箱验证或真实交付。
- 现状：两端保持 pre-S00，`currentStage=null`，阶段表仍空。任务隔离、模型凭证、详细领域状态、外部人工操作待办身份和正式 wire 均未冻结；48 项业务验收目标及 W1—W9 真实场景均未运行。下一步继续用 grilling 收敛待决项，再由用户审核权威文档拟写稿。

## 2026-09-26 / 设计文件落盘复核与术语澄清

- HEAD 仍为 `8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区仍为 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪，未暂存或提交。两端 `currentStage=null`、状态表为空。
- 针对用户指出的疑似写入不全，逐一核实上轮主要新增/修改文件存在，候选 JSON 均可解析；定向检查 90 个本地文档链接，缺失 0。正式 `wiki/contracts/schemas/` 下的旧候选位置和临时样例脚本不存在，候选文件在 `doc/research/schemas/`；它们的迁移与删除是上轮有意调整。上轮重复移动已迁移文件的命令曾失败，但最终文件位置已核实。
- 本轮仅澄清文档术语：Runner 启动的 Coding Agent/模型工具进程、目标项目仓库的构建/测试/脚本进程统称任务执行子进程；后者不同于 Kowa 自身测试。更新身份设计、GitHub 交付、任务清单、权威拟写稿与审阅目录；未修改 common/decision、生产代码或历史项目。
- 验证：`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、`jq empty` 两份候选 JSON 和 `git diff --check` 退出 0。`git diff --check` 不覆盖未跟踪文档；实际文件和定向链接另行检查。未执行 MVP 业务测试或真实集成验收。

## 2026-09-26 / 本机 Provider 与成员 GitHub 身份纠偏

- HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区仍为 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪。两端 `currentStage=null`、阶段表为空；未暂存、提交、安装依赖、触发真实运行或修改历史项目。
- 用户澄清：MVP 的独立 Runner 在本机 Mac 调用宿主已安装/已登录的 Claude Code、Codex 等共享 Provider；此前“每 Task 受限容器”的整体执行假定不成立。项目脚本隔离与运行 CLI 的 macOS 账号仍待核定。
- 用户确认：可恢复失败暂停当前 WorkflowRun、终结才 FAILED；GitHub Review/合并等待建立只提示、由外部事实自动收束的 HumanTask。A 发起的运行以 A 的 GitHub 用户权限受控推送分支并自动创建 PR，commit author/committer 与 PR 创建者均为 A；B 的运行使用 B 的权限。早先“App installation 推送/创建 PR”方案被新要求取代；App 仍用于用户授权和仓库安装范围，不代替成员写入。
- 环境只读事实：当前交互 shell 可运行 `codex-cli 0.154.0`，`claude` 不在 PATH；没有检查模型登录或真实调用。当前 Mac 用户 `liufei` 的 `gh auth status` 成功，故同一账号下的模型/项目命令可能绕过受控 A/B GitHub 身份；Runner OS 账号与凭证隔离须做负向验收。
- 实际修改：细化身份/权限、GitHub 交付、固定流程、领域状态与外部操作待办、前后端候选设计、跨端契约、真实验收方案及矩阵；候选固定流程把受控推分支/建 PR 从 Runner 能力节点改为 Server 系统用例。受控读输入按 A/B 权限从 GitHub 固定提交物化，Runner 不持成员 GitHub 令牌；已发布代码下游仍以 GitHub OID 交接。MVP 服务候选不配置 App 私钥，只用 GitHub App 用户授权的 client secret/用户令牌。`common.md`/`decision.md` 的准确措辞只更新在 research 拟写稿，两个权威文件未改。发布前变更传输与 common 的 GitHub 稳定交接措辞仍待审。
- 文档核验：`jq empty` 两份候选 JSON、`./scripts/check-doc-governance.sh`、`git diff --check` 均退出 0；定向检查 20 个文件、120 个本地链接，缺失和末尾空白均为 0。未进行完整 JSON Schema 标准/消费侧验证，也未执行业务测试或真实 GitHub 操作；`git diff --check` 不覆盖未跟踪文档。

## 2026-09-26 / 权威共识批准与单账号部署边界

- 用户授权：明确采纳 Q37 的 MVP 领域状态/轮次模型，Q38 选择首轮 Server、Runner 和本机 Provider 共用一个 macOS 账号，并批准 `doc/research/mvp-authority-change-proposal.md` 的 common/decision 拟写内容。此次仅落实设计文档，不创建开发阶段或生产代码。
- 开始与结束 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区仍为 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪。没有暂存、提交、推送、切换分支、安装依赖或执行真实 GitHub/WorkflowRun 操作。
- 实际修改：按获批内容更新 `doc/common.md` 的小团队成员、A/B GitHub 用户权限、本机共享 Provider 和代码临时传输/正式 GitHub 引用边界；向 `doc/decision.md` 追加 GitHub App Web 登录、成员身份写入、可恢复失败暂停与外部操作型 HumanTask 的所选/备选/原因/影响。将跨端交付生命周期及分类索引转为 active；更新两端接力、执行架构、身份安全、验收与设计审阅导航，记录首轮同 macOS 账号。
- 已确认边界：GitHub 仓库读取、工作分支推送、PR 创建和平台事实对账由 Server 的受控 GitHub 用例执行；Runner 调用本机共享 Claude Code/Codex 等 Provider 生成变更并运行目标项目命令，不持成员 GitHub 用户令牌。进程分离不等于同账号文件/凭证隔离；当前 macOS 用户的个人 `gh` 登录态仍是必须实测的绕过风险，不能据此宣称多人硬隔离已通过。
- 验证：`bash -n` 治理脚本、`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、`jq empty` 两份候选 JSON 和 `git diff --check` 均退出 0，输出含 `DOC_GOVERNANCE_PASS`、`DOC_GOVERNANCE_TEST_PASS`。定向检查 25 个目标文档、136 个本地链接，缺失与末尾空白均为 0。`git diff --check` 不覆盖未跟踪文件；没有执行完整候选 JSON Schema 消费侧验证或业务集成验收。
- 状态：后端、前端 `currentStage` 仍为 null，状态表为空；正式 wire、同账号沙箱与凭证隔离、真实 Provider/GitHub 权限验收仍待后续工作，不标记阶段 DONE/BLOCKED。

## 2026-09-26 / Server 与 Runner 权限责任澄清

- HEAD 仍为 `8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区为 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪。两端仍为 pre-S00，未创建阶段、生产代码、真实运行、提交或暂存。
- 按用户询问核对当前设计：Web GitHub 登录建立 Server 会话；Server 判断 Workspace、对象、人工任务、版本和成员 GitHub 权限，保管 A/B 用户令牌并处理仓库物化、受控推分支/建 PR 与外部对账。Runner 只使用独立 Kowa Runtime 凭证领取/报告已分配任务并监管本机 Provider/项目命令；Worker 不持 Runtime 或 GitHub 写凭证。
- 实际文档修改：在正式执行架构增加 Server/Runner/Worker 权限表并同步架构索引；在身份草案明确 Runtime 凭证边界和已选单 macOS 账号；在后端候选 schema 增加 Runtime 登记；在最小契约草案限制 Runner 领取与报告面。未修改 common/decision 或阶段状态权威。
- 验证：`./scripts/check-doc-governance.sh`、`git diff --check` 退出 0；定向检查 5 个修改文件、24 个本地链接，缺失与末尾空白均为 0。`git diff --check` 不覆盖未跟踪文件；未执行业务或真实环境隔离测试。

## 2026-09-26 / Agent 远端 Git 权限重审

- 开工 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区为 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪。后端、前端 `currentStage=null`、状态表空；未建立阶段、生产代码、真实任务或 GitHub 操作。
- 用户指出现设计把远端 Git 全交 Server、Runner/Agent 无 GitHub 能力，不能满足 Codex/Claude 编码时的 fetch/pull/push。核对结果：本地 commit 不需 GitHub 令牌；远端私有仓和 `gh` 操作需要认证。当前本机 Codex CLI 可运行 shell，`gh` 个人登录态存在；Claude CLI 在当前 PATH 不可见。历史 YAML 直接证明 code.generate 与 MR 创建分工，执行机 SSH/bytedcli 身份和串号风险来自历史文档，缺少对应实现源码，不记为 Kowa 或历史完整实现通过。
- 实际修改：新增 `doc/research/github-permission-audit.md`，逐项审计登录、clone/fetch、commit、pull/rebase、push、PR、Review/merge、CI 的授权人与执行位置；更新设计审阅目录、候选契约/Schema 说明、Workflow/Capability 和 GitHub 草案、后端设计与接力、验收方案；正式执行架构纠正“Runner 不能推分支”的过度断言，新增 glossary 身份术语并同步两个 wiki 分类索引。未修改 `common.md`、`decision.md` 或阶段状态权威；它们的 GitHub 执行位置勘误须在 Q39 答复后提交准确拟稿并获批准。
- 已确认边界：Server 拥有业务授权、Task 准入与结果接受，Runner/Agent 需要能在获准 Task 中发起 Git 操作；Runtime 凭证本身不是 A/B GitHub 身份。原生直连令牌或受控 Git 通道是未决设计，不从用户指出功能缺口直接推断可安全给模型原始令牌。
- 验证：`./scripts/check-doc-governance.sh` 与其自测试、`git diff --check` 均退出 0；定向检查 15 个目标文件、121 个本地链接，缺失与末尾空白均为 0。`git diff --check` 不覆盖未跟踪文档；未执行 Kowa 业务测试、真实 Provider/GitHub 或沙箱验收。
- 下一动作：等待用户回答 Q39，随后冻结 Agent 远端 Git 的操作界面、凭证委派、PR 节点归属与负向验收，再提议修订受影响的 common/decision。保持 pre-S00，不标记阶段 BLOCKED。

## 2026-09-26 / 每 Runner 本机 gh 账号可行性与 MVP 身份范围

- HEAD 仍为 `8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 均未跟踪。两端 `currentStage=null`，无阶段执行；未暂存、提交、安装依赖或做真实 GitHub 操作。
- 只读环境事实：当前 Mac 有 `gh 2.100.0`、有效的宿主 `gh` 登录与 Codex CLI；当前 shell 找不到 `claude`；`git config --global --get credential.helper` 未返回配置，故不能从 gh 登录推断 git push 已可用。未读取或展示任何 token，未执行 `gh auth setup-git` 等写配置命令。
- 平台核对：GitHub CLI 的活动账号用于指定 host；`GH_TOKEN` 可覆盖已存凭证，`GH_CONFIG_DIR` 可另设配置目录，`gh auth setup-git` 才配置 Git credential helper。GitHub App 用户令牌与机器 gh 登录是不同身份；GitHub 推荐长期集成优先使用 App，机器账号/PAT 有管理和泄漏代价。对应官方链接见权限审计。
- 用户选择：把 Runner 本机 Provider+统一 `gh`、Agent 原生 Git/gh 纳入 MVP 交付规则，而非仅作技术试点；commit author/committer 采用 Runner 机器账号，Web 发起人/Agent 来源另记。Q43 机器账号类型、Q44 独立人工 Review 待答；此前 common/decision 的 A/B GitHub 写入身份暂未修改，需在拟稿完成并获用户批准后按权威规则替换。
- 实际修改：扩展 `doc/research/github-permission-audit.md` 的每机 gh 可行性和 MVP 身份风险，更新设计审阅目录和开发前清单；未改生产代码、正式阶段状态、历史项目或 common/decision。
- 边界：每机单个 gh 活动账号可供单人/统一身份的功能试点，也能支撑已选机器身份规则；它不提供 A/B 分别在 GitHub 署名的能力。首轮能否通过，还取决于 Provider 权限、Git credential helper/SSH、仓库保护和同账号隔离的真实验证。
- 验证：`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、`git diff --check` 均退出 0；定向检查 4 个文件、60 个本地链接，缺失与末尾空白均为 0。`git diff --check` 不覆盖未跟踪文档；没有运行 Kowa 业务测试或真实 GitHub 写操作。

## 2026-09-27 / MVP 阶段规划前准入清单

- HEAD 为 `8604596b0fdf1939c72651fda7849cef51d3c877`；预存 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 仍未跟踪。两端 `currentStage=null`、阶段表空；未建立 S00、写生产代码、暂存、提交、安装依赖或触发真实运行。
- 用户已选：Runner 直接使用本机个人 `gh`，机器账号作为 MVP GitHub 写入身份；commit author/committer 归该账号；PR 在必需验证/独立评审后的独立 Runner `publish_pr` 节点创建。Q44 人工 Reviewer 独立性与 Q46 提前 PR 的逻辑/硬门禁仍待答。
- 本轮修改研究期 GitHub 权限审计、权威替换拟稿、候选最小契约与 JSON 固定流程示例、Workflow/Capability、身份和后端候选设计及两端接力；在开发前清单增加阶段规划准入表。候选 `publish_change` 改为远端 OID 核对，`publish_pr` 为 Runner Capability。未修改受保护 `common.md`/`decision.md`，两者旧 A/B 写入身份仍与新选择冲突，须提交完整替换稿并获批准后勘误。
- 已厘清阶段前与阶段内的边界：前者需收口产品/身份/门禁、权威、流程和跨端最小契约及 owner；真实 CLI 安装/登录、GitHub 授权、迁移、隔离负向运行和 W1—W9 集成验证应进入未来阶段实施/退出合同，不得提前写入 record 伪造证据。
- 验证：`./scripts/check-doc-governance.sh`、`git diff --check` 与两份候选 JSON 的 `jq empty` 均退出 0；定向检查 11 个目标文档、115 个本地链接，缺失和末尾空白均为 0。`git diff --check` 不覆盖未跟踪文件；未执行完整 JSON Schema/消费者验证或业务测试。

## 2026-09-27 / 阶段规划准入清单推进

- HEAD 仍为 `8604596b0fdf1939c72651fda7849cef51d3c877`；`.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 保持未跟踪。两端 `currentStage=null`，未创建阶段、执行 record、生产代码、提交或真实 GitHub 运行。
- 用户收口 Q44/Q46：GitHub Review 由有权真人完成且不由 PR 创建账号自审；正常流程只在独立 `publish_pr` 节点建 PR，MVP 不为 Coder 可能提前调用宿主 gh 建 PR 提供专项硬防护/检测/清理，不将 DAG 顺序称为 GitHub 层权限限制。
- 设计期修改：更新 GitHub 权限审计、权威替换拟稿、固定工作流候选（Coder 原生 Git，`verify_change` 远端 OID 核对，独立 Runner `publish_pr`）、后端候选 schema/身份草案、前端信息架构、验收矩阵/可复跑方案、两端接力和开发前清单；新增跨端阶段依赖地图和后端模块 owner 建议。首期 Server 同机个人 gh 只读查询 GitHub 事实为候选，未来分机需要独立读取凭证。
- `common.md`/`decision.md` 仍保留旧 A/B 用户令牌写入决定；已将准确替换文字提交用户审核，未在明确批准前修改保护文件。正式 wiki 流程及跨端机器 schema 的最终冻结依赖权威勘误和消费者审阅。
- 验证：`./scripts/check-doc-governance.sh`、`jq empty` 两份候选 JSON、`git diff --check` 均退出 0；定向检查 12 个文件、110 个本地链接，缺失和末尾空白为 0。`git diff --check` 不覆盖未跟踪文档；无业务测试或真实集成证据。

## 2026-09-27 / 机器 gh 身份权威勘误与阶段前设计收口

- 开始与结束 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`；预存工作区和收工工作区均显示 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪。两端 `currentStage=null`，未创建 S00/阶段 record、修改生产代码、暂存、提交、推送、安装依赖或触发真实 GitHub/WorkflowRun 操作。
- 用户批准了 `doc/research/mvp-machine-gh-authority-proposal.md` 的准确替换稿，另确认 Q46 不为 Coder 可能提前创建 PR 增设专项处理。按获批内容替换 `doc/common.md` §2.7/§3.3 和 `doc/decision.md` 的 GitHub 写入决策。当前权威为 Web GitHub App 登录、Runner 本机个人 gh/Git 执行、机器账号署名、Coder 推送、独立 `publish_pr`、有权真人且非 PR 作者 Review、人在 GitHub 合并。
- 同步正式 MVP 流程、执行边界及分类索引；新增 `doc/wiki/contracts/mvp-cross-end-semantics.md` 并更新 contracts 索引，给跨端合同指定模块 owner、Producer/Consumer、版本身份及失败语义。更新 GitHub 交付研究、候选契约、后端与前端接力、后端候选设计、设计审阅和开发前清单；研究审计保留旧方案历史但已显式标记过时。阶段依赖地图继续作为设计输入，不创建实际阶段编号。
- 首期同机 Server 使用个人 gh 做只读 GitHub 事实查询仍为待实施验证的适配器方案；未来分机须另配 Server 读取凭证。机器个人账号的实际 GitHub 权限、Provider/项目脚本隔离和仓库保护未验收，不把 Kowa TaskSpec 说成远端硬权限。准确 HTTP、DDL、Provider 协议和候选 JSON 字段留给唯一 owner 阶段与消费者冻结。
- 验证：`bash -n scripts/check-doc-governance.sh scripts/check-doc-governance-test.sh`、`./scripts/check-doc-governance.sh`、`./scripts/check-doc-governance-test.sh`、两份候选 JSON 的 `jq empty`、`git diff --check` 均退出 0；输出含 `DOC_GOVERNANCE_PASS`、`DOC_GOVERNANCE_TEST_PASS`。定向检查 19 个本轮主要文档、131 个本地链接，缺失和末尾空白均为 0。`git diff --check` 不覆盖未跟踪文件；没有运行 Kowa 业务测试、真实 Provider/GitHub 操作、数据迁移或端到端验收。

## 2026-09-27 / 首批阶段计划撰写前最终设计审查

- 授权与边界：用户要求以磁盘事实完成最终设计审查与收尾；允许修正已确认且无需产品裁决的非保护文档问题。未创建 S00、阶段状态行或阶段 record，未写生产代码，未暂存、提交、推送、安装依赖或执行真实 GitHub/WorkflowRun 操作。
- HEAD 与预存工作区：开始 HEAD 为 `8604596b0fdf1939c72651fda7849cef51d3c877`，分支 `main`；`.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 及前端预览制品均为预存未跟踪内容。两端 `currentStage=null`、`activeReopen=null`、`suspendedReopen=null`，状态表及 stage/record 目录为空。
- `CONFIRMED`：common、decision、正式 wiki、两端设计、候选合同与验收方案对双仓职责、固定流程、Coder 原生 Git/gh、`verify_change`、独立 `publish_pr`、机器 GitHub 身份、真人 Review、人工合并、版本失效、失败恢复和外部结果未知的主语义已经收敛。精确 HTTP/DDL/wire、Provider 协议、GitHub 读取适配器和真实隔离属于后续唯一 owner 阶段的实施/退出条件，不是撰写阶段计划前的产品阻塞。
- `CODE_CONFIRMED`：当前仓库无 `go.mod`、`package.json`、`pnpm-workspace.yaml`、`cmd/`、`internal/`、`web/`、`frontend/` 或 `backend/` 工程入口；候选 schema 含 18 类消息，固定流程样例含 17 个普通依赖无环节点。当前只能做文档治理、JSON 结构与设计一致性检查，不能声称业务集成或真实运行通过。
- 已修正文档差异：质量设计将 PR 创建者及 commit author/committer 从错误的 WorkflowRun 发起人改为 Runner 机器个人 GitHub 账号，并同步 design 分类索引核验日期；身份设计删除 Q44/Q46 和 common/decision 仍待确认的过时表述；固定恢复入口允许 `NOT_STARTED` 且无执行事实时跳过尚不存在的 record，消除与门禁及“record 不预填”的冲突。未修改 `doc/common.md` 或 `doc/decision.md`。
- 仍需阶段承担的风险：候选机器 schema 尚未覆盖完整 Workspace/RunView/Git 操作与机器身份 wire，固定流程样例未冻结外部 HumanTask 创建和 GitHub 要求修改后的返修机器表达；治理脚本只校验跨端依赖阶段存在，不自动校验其已 DONE 或契约已冻结。阶段合同必须指定唯一 owner、冻结版本、正反样例、消费者审阅、人工出口和依赖完成判据。
- 验证：`bash -n` 两个治理脚本、治理门禁、自测试、两份候选 JSON 的 `jq empty`、`git diff --check` 均退出 0；自测试报告 8 个 JSON 负向样例。定向候选 JSON 审计解析 128 个本地 `$ref`、18 类消息和 17 个无环节点；定向文档审计检查 49 个 Markdown 文件、215 个本地链接、末尾换行及行尾空白，均通过。以上不是业务集成测试或真实运行验收。
- 结论：已具备进入首批后端、前端阶段计划撰写的设计输入；精确 wire、工程基座、Provider/GitHub/隔离实测及 W1—W9 新运行证据应写入对应阶段合同，不得在计划撰写时冒充完成。

## 2026-09-27 / 跨端依赖与冻结契约治理门禁

- 用户批准：将“跨端依赖完成状态”和“冻结 wire 版本/摘要”从人工审查升级为治理门禁，并要求阶段规划前提交当前 pre-S00 基线；阶段规划建议在独立分支进行。本轮不创建阶段、状态行或阶段 record，不进入生产代码。
- 开始 HEAD：`8604596b0fdf1939c72651fda7849cef51d3c877`；提交前工作区仍为既有 `.gitignore`、`AGENTS.md`、`doc/`、`mise.toml`、`scripts/` 未跟踪基线，两端 `currentStage=null`，阶段表及 stage/record 目录为空。
- `CODE_CONFIRMED`：原治理脚本只验证 `backend:Sxx` / `frontend:Sxx` 引用的状态行存在，不验证依赖阶段已 `DONE`，也不验证消费者合同填写了共享契约 owner 和冻结版本或摘要。
- 实际修改：在 `AGENTS.md` 与 AI 分阶段交付规则中规定消费者可预建为 `NOT_STARTED`，但进入任何非 `NOT_STARTED` 状态前，跨端依赖必须 `DONE` 且合同必须绑定具体 owner 和冻结版本/摘要；阶段模板补充 owner/消费者退出证据；治理脚本增加状态与合同字段门禁；自测试增加“依赖未完成、owner 缺失、版本未冻结”三个负向样例和有效样例。
- 版本格式：跨端消费者的 `共享契约 owner` 必须包含 `backend:Sxx` 或 `frontend:Sxx`；`冻结契约及版本` 必须包含可识别的 `vN[.N]`、`version/版本` 数字或 `sha256:<64 hex>`。共享契约 owner 阶段退出时还必须提供机器 schema、版本或摘要、正反样例、Producer/Consumer 核对、失败语义和验收矩阵映射。
- 验证边界：执行治理脚本语法检查、治理门禁、自测试、候选 JSON 语法与结构检查、文档链接/空白检查、`git diff --check` 和提交前 staged diff 检查；这些不是业务集成测试或真实运行验收。提交完成后的准确 commit 以 Git 历史为准，不在被该提交包含的正文中自引用其 hash。
