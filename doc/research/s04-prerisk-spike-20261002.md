# S04 前置风险验证 Spike 报告（2026-10-02）

性质：调研材料，非正式文档。本报告不拥有阶段状态，不改变任何 backend/frontend 阶段状态，不修改 `doc/common.md`、`doc/decision.md` 或任何 `stage/` 合同；所有“建议”和“需批准事项”只供用户裁决。正式的沙箱仓库约定见 [测试仓库约定](../Kowa验收与运行/测试仓库约定.md)。

## 0. 开工审计与边界

| 项 | 事实 |
| :--- | :--- |
| HEAD / main | `bffbb66ab814f796de1533ca7731bf6e4c4e406b`，等于 `origin/main`（fetch 后核对），含 PR #13、#14 |
| main CI | `gh run list --branch main`：Backend run `36995320848`、Frontend run `36995320806`，headSha 均为 bffbb66，conclusion 均为 success |
| 隔离工作区 | `.claude/worktrees/distracted-knuth-867620`，分支 `claude/distracted-knuth-867620`，开工时 `git status --short` 为空 |
| AGENTS.md | 在隔离树内重读（183 行），`git hash-object AGENTS.md` = `f4b20b405a1cf15b5fbe77a7a80b52fbcdcda46b` |
| 受保护 S03 WIP | `/Users/liufei/workspace/kowa`，HEAD `a8d997e0ec2ec1e1623a4c3e34e2c82ba352b69d`，`git status --short -uall` 共 41 项；本会话只做 `rev-parse`/`status` 与对一个文件的 `test -r`/`test -w` 权限位探测，没有任何写入或 stage |
| `v1 callback failed` 偶发 | 本会话未运行 `TestIdentityWorkspaceServerPostgres`（仅改文档），因此没有状态码/响应体可贴，也未重跑；若文档 PR 的 CI 出现该偶发，按指示先贴状态码与响应体 |
| 阶段状态 | 未改动。后端 `currentStage` 仍为 S04（`NOT_STARTED`），未执行 S04 |
| 生产代码 | 未改动。本次仅新增两份文档 |

**事实等级**：`DATA_CONFIRMED` = 命令输出或运行产物直接确认；`CONFIRMED` = 两条以上独立路径交叉确认；`CODE_CONFIRMED` = 当前 Kowa 源码/测试确认（本报告未涉及 Kowa 源码行为）；`STRONG_INFERENCE` = 证据高度支持但缺一个直接闭环；`HYPOTHESIS` = 未验证。

**开工输出（AGENTS §2）**：预存工作区为空；文档差异见 §9；证据不足项见 §8；固化旧错误的测试：无（本 spike 不触碰测试）；拟新增测试：无（spike 不写生产代码，S04 阶段再按目标红灯建立）；实际修改边界：`doc/research/s04-prerisk-spike-20261002.md`、`doc/Kowa验收与运行/测试仓库约定.md`，外加沙箱仓库内的临时分支/PR（已清理）。

**环境**：Darwin 25.6.0 arm64；`codex-cli 0.157.1`；`claude` 2.1.285；`gh` 2.100.0；`git` 2.50.1（Apple）；`go` 1.25.0（mise 全局 shim，沙箱仓库无 `mise.toml`）。本机无 `timeout` 命令（`zsh: command not found: timeout`，DATA_CONFIRMED），所有带死线的实验使用附录 A 的 Python 监管脚本。

## 1. 结论摘要

| # | 问题 | 结论（事实等级） |
| :--- | :--- | :--- |
| 1 | 两个 CLI 能否非交互并产出可解析结构化输出 | 能。Codex 须显式 `-m`、必须关闭 stdin，否则挂起；Claude 用 `structured_output` 字段而非 `result`。Provider 侧强制 schema，截断/超时时**不会产生半截 JSON，而是缺少结果**；Codex 被 SIGTERM 后包装进程仍退出 0（CONFIRMED） |
| 2 | Agent 能否 clone/commit/push/建 PR | Claude 默认配置下一次完成；Codex 默认 `workspace-write` 沙箱**不能** commit（`.git` 只读）也不能联网，须放宽（`writable_roots`+`network_access` 或 `danger-full-access`）才能完成（CONFIRMED）。push 实际走磁盘 SSH 私钥，与 `gh auth status` 的 keyring 令牌是两条独立凭证路径；`gh auth status` 为 ✓ 时 HTTPS push 仍失败（CONFIRMED） |
| 3 | 取消与超时 | 按进程组 SIGTERM 能结束 Provider 本体，但 Provider 的工具子进程各自独立进程组、Agent 派生的 `setsid` 后台进程**全部存活**；仅杀 Codex 的 node 包装进程会遗留原生进程。超时的 `git push` 与 `gh pr create` 都出现“客户端被杀但远端已成功”，可用 `git ls-remote` / `gh pr list --head` 对账（CONFIRMED） |
| 4 | 凭证可见范围 | 任务进程（Claude、Codex 完全访问、朴素 Runner 子进程）可读 SSH 私钥、Codex `auth.json`、gh 配置，能经 Keychain 取 gh 令牌，并对同账号所有仓库（含 Kowa 源仓，`push:true`）拥有写权限（元数据确认，未实际写入）。Codex `workspace-write` 默认阻断写、网络和 Keychain，但**仍可读**上述凭证文件；环境变量原样透传（CONFIRMED） |
| 5 | 小任务成本/耗时 | 修复 fixture 失败测试并开 PR：Claude 1 次成功，37.3 s，$0.209；Codex 2 次尝试（第 1 次沙箱内无法交付），共 190.4 s，约 35.4 万输入/5.5 千输出 token，订阅制无美元数字（DATA_CONFIRMED） |
| 6 | Server 读取 GitHub 事实 | 同机个人 `gh` 只读查询可行（PR、Review、merge OID、CI、head OID 均取得）。该令牌具 `repo` 写作用域，“只读”只是约定；账号漂移可用 `gh api user` 的 login+numeric id 与 `gh auth status --json` 检测（DATA_CONFIRMED / 部分 STRONG_INFERENCE） |

## 2. Q1：Provider CLI 可用性与结构化输出

### 2.1 可用性阶梯

CLI “存在 ≠ 已登录 ≠ 能执行”，三者在本机都被证实可以分离。

| 层级 | 命令 | 退出码 | 关键输出 | 等级 |
| :--- | :--- | :--- | :--- | :--- |
| 存在 | `command -v codex claude gh git` | 0 | codex → `/opt/homebrew/bin/codex`；claude → mise shim（进程树显示 `/opt/homebrew/bin/claude`） | DATA_CONFIRMED |
| 版本 | `codex --version`；`claude --version` | 0 / 0 | `codex-cli 0.157.1`；`2.1.285 (Claude Code)` | DATA_CONFIRMED |
| 登录态 | `codex login status`；`claude auth status` | 0 / 0 | “Logged in using ChatGPT”；`loggedIn:true, authMethod:claude.ai`（输出含邮箱/组织，已不写入本文） | DATA_CONFIRMED |
| 未登录对照 | `CODEX_HOME=<空目录> codex login status`；`CLAUDE_CONFIG_DIR=<空目录> claude auth status` | 1 / 1 | “Not logged in”；`loggedIn:false, authMethod:none` | DATA_CONFIRMED |
| 可执行 | `codex exec`（默认配置） | 1 | 登录态正常，但用户全局默认模型 `gpt-6.1-sol` 对 ChatGPT 账号不可用：`turn.failed`，HTTP 400 `The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account` | CONFIRMED（失败流 + 加 `-m gpt-5.5` 后成功） |

结论：S04 探测必须有“真实最小执行”一级，且 Runner 必须显式传 `-m`，不得依赖用户全局 `~/.codex/config.toml`（STRONG_INFERENCE：模型可用性随账号/时间变化）。

### 2.2 成功调用基线

```bash
# Codex（必须 </dev/null；缺 -m 会用用户默认模型）
codex exec -m gpt-5.5 --skip-git-repo-check --ephemeral --sandbox read-only --json \
  --output-schema /abs/schema.json -o /abs/last.json "<prompt>" </dev/null

# Claude
claude -p --output-format json --json-schema "$(cat schema.json)" \
  --no-session-persistence --max-turns 3 "<prompt>" </dev/null
```

| 观察 | 实测 | 等级 |
| :--- | :--- | :--- |
| Codex 不关 stdin 即挂起 | 以工具管道调用，≥180 s 不返回；stderr 仅 `Reading additional input from stdin...`；改用 `stdin=DEVNULL` 后 14.0 s 完成。`--help` 也说明“管道 stdin 会被追加为 `<stdin>` 块” | CONFIRMED |
| Codex 成功形态 | 退出 0；`-o` 文件内容 `{"verdict":"pass","summary":"The probe worked."}`；事件 `thread.started`→`turn.started`→`item.completed(agent_message)`→`turn.completed`；usage `input_tokens 14772 / cached 1408 / output 23` | DATA_CONFIRMED |
| Codex 的 `item.completed` 且 `item.type=="error"` 并不一定是失败 | 实际是“Under-development features enabled…”警告；真正的失败是顶层 `turn.failed` | DATA_CONFIRMED |
| Codex 每条 agent_message 都带 schema | 一次任务有 9 条 `agent_message`，中间消息也是合规 JSON（含 `fixed:false` 的进度）；**最终结果必须取最后一条/`-o` 文件**，不能取第一条 | DATA_CONFIRMED |
| Claude 成功形态 | 退出 0；`structured_output` 为对象，`result` 为同内容字符串；`stop_reason:"tool_use"` 同时 `terminal_reason:"completed"`（结构化输出经合成工具返回，**不要用 stop_reason 判断**） | DATA_CONFIRMED |
| 用户级配置被带入 | Claude `-p` 默认拉起 5 个 `npm exec` MCP 进程（context7、memory、playwright、sequential-thinking、github）；首次调用 `cache_creation_input_tokens` 32,705、$0.1318。加 `--strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources project` 后为 4,704、$0.0240，6.7 s | CONFIRMED |
| `claude --bare` 不可用于 OAuth 登录 | 退出 1；`result:"Not logged in · Please run /login"`，且 `subtype:"success"` 与 `is_error:true` 并存 | DATA_CONFIRMED |
| Codex 隔离用户配置 | `--ignore-user-config --ignore-rules` 仍可用登录态与 `-m`，退出 0，input 13,176 token | DATA_CONFIRMED |

### 2.3 失败行为矩阵（Provider 侧）

| 场景 | Codex | Claude | 等级 |
| :--- | :--- | :--- | :--- |
| 提示词与 schema 冲突（“只回 HELLO WORLD，并加 extra 字段”） | 退出 0，仍输出合规 `{"verdict":"pass","summary":"HELLO WORLD"}` | 同上，`structured_output` 合规 | DATA_CONFIRMED：schema 在 Provider 侧强制，提示词无法产生“额外字段” |
| 不传 schema，只在提示词要 JSON | 文字说明 + ```` ```json ```` 围栏 | `structured_output:null`，`result` 为说明 + 围栏 | DATA_CONFIRMED：这种模式下必然出现“额外文字” |
| schema 本身无效 | 退出 1，约 8 s；`turn.failed` HTTP 400 `invalid_json_schema` | 未测 | DATA_CONFIRMED |
| schema 文件不存在 | 退出 1，0.5 s，stderr `Failed to read output schema file ...`，无事件流 | 未测 | DATA_CONFIRMED |
| 死线 SIGTERM（4 s） | 包装进程**退出 0**；无 `-o` 文件，无 `turn.completed`，事件流仅 `thread.started`/`turn.started`/警告 | 退出 143；`--output-format json` 时 stdout 0 字节；`stream-json` 有合法前缀行但无 `result` | CONFIRMED：Codex 退出码 0 不能证明完成 |
| 轮次耗尽（`--max-turns 1`） | 未测 | 退出 1；`subtype:"error_max_turns"`，`is_error:true`，`structured_output:null`，`errors:["Reached maximum number of turns (1)"]`，`num_turns:2` | DATA_CONFIRMED |
| 预算耗尽（`--max-budget-usd 0.001`） | 不适用 | 退出 1；`subtype:"error_max_budget_usd"`；实际花费 $0.0240，**超出上限 24 倍**——预算是事后检查而非硬上限 | DATA_CONFIRMED |
| 未登录 | `turn.failed` 401；先有 5 次 `Reconnecting... n/5`（顶层 `error` 事件，非终态），共 28.4 s，退出 1 | 退出 1，`is_error`，JSON 约 1.4 s | DATA_CONFIRMED |

### 2.4 Kowa 侧解码要求（Go 演示，scratchpad 内一次性程序）

```text
case                                  | naive json.Unmarshal | strict (Decoder+DisallowUnknownFields+必填+枚举+无尾随)
valid                                 | accept               | accept
truncated                             | REJECT               | REJECT: unexpected EOF
prose+fence                           | REJECT               | REJECT: invalid character 'I'
missing summary                       | accept               | REJECT: missing required field
unknown extra field                   | accept               | REJECT: json: unknown field "extra"
enum violation ("PASS")               | accept               | REJECT: verdict outside enum
trailing garbage / two objects        | REJECT               | REJECT: trailing data after JSON value
duplicate key (last wins)             | accept               | accept   <- 标准库无法拒绝
empty                                 | REJECT               | REJECT: EOF
```

DATA_CONFIRMED（`go run .` 退出 0）。含义：宽松 `json.Unmarshal` 会放过缺字段、未知字段、枚举越界；重复键两种方式都放过，需要自定义 token 级检查才能拒绝。事实来源（Provider 强制 schema）与 Kowa 校验必须分离：前者不是后者的替代。

## 3. Q2：Git/gh 能力、凭证配置与身份

### 3.1 凭证配置状态

| 项 | 实测 | 等级 |
| :--- | :--- | :--- |
| `gh auth status` | 登录账号 `SnWalker`，`Active account: true`，来源 keyring，**Git operations protocol: ssh**，scopes `gist, read:org, repo`，退出 0 | DATA_CONFIRMED |
| git credential helper | `git config --show-origin --get-regexp credential` 仅有 Xcode CLT 系统级 `credential.helper osxkeychain`；全局/本仓无；没有 gh 的 helper | DATA_CONFIRMED |
| HTTPS 访问私有仓 | `GIT_TERMINAL_PROMPT=0 git ls-remote https://github.com/<私有仓>.git` → `fatal: could not read Username ... terminal prompts disabled`；`git credential-osxkeychain get` 无 github.com 条目 | DATA_CONFIRMED |
| SSH | `~/.ssh/id_ed25519`（411 字节）；`ssh-add -l` 显示 agent 无身份；`ssh -o BatchMode=yes -T git@github.com` → `Hi SnWalker!`，退出 1（GitHub 对成功认证也返回 1）。BatchMode 能成功说明私钥无口令 | CONFIRMED（BatchMode 成功 + 411 字节 STRONG_INFERENCE 无口令） |
| `~/.ssh/config` | 不存在 | DATA_CONFIRMED |

### 3.2 clone → commit → push → PR（沙箱仓库，Agent 之外的基线）

```bash
gh repo clone SnWalker/kowa-sandbox <dir>          # 5.8 s，origin 为 git@github.com:...（SSH）
git switch -c exp/spike-s04-identity origin/main
git commit -m "spike: identity probe (fake data)"  # author/committer = git config 全局 user（邮箱已脱敏）
git push -u origin exp/spike-s04-identity          # 4.7 s，退出 0
gh pr create --repo SnWalker/kowa-sandbox --base main --head exp/spike-s04-identity --title ... --body ...  # PR #1
```

| 角色 | GitHub 记录的值 | 来源（谁决定） | 等级 |
| :--- | :--- | :--- | :--- |
| push 操作者 | `SnWalker`（`gh api repos/<R>/activity` 的 `actor.login`，`branch_creation`） | 磁盘 SSH 私钥 `id_ed25519` | DATA_CONFIRMED |
| commit author / committer | `author.login=committer.login=SnWalker`，邮箱为 git 全局配置，`verification.verified=false, reason=unsigned` | `git config user.email`，**未经认证的文本**，GitHub 按邮箱映射账号 | DATA_CONFIRMED |
| PR 创建者 | `SnWalker` | gh keyring 令牌 | DATA_CONFIRMED |

三条独立的身份来源（SSH 私钥、gh 令牌、`user.email`）此刻恰好都指向同一账号，没有机制保证它们保持一致；账号漂移在任一来源上都会让 push/commit/PR 的归属分裂。伪造他人邮箱作为 author 由 git 语义保证可行但**未实际 push**（避免错误归属真实用户）：STRONG_INFERENCE。

### 3.3 `gh auth status` 与实际 push 能力的差异

| 命令 | 结果 |
| :--- | :--- |
| `GIT_TERMINAL_PROMPT=0 git push https://github.com/<R>.git <sha>:refs/heads/exp/spike-s04-https` | 失败：`could not read Username ...`（`gh auth status` 同时为 ✓） |
| 同上加一次性 `-c credential.helper= -c 'credential.helper=!gh auth git-credential'` | 成功，`activity` 记录 `branch_creation` 且 actor 为 `SnWalker`（未改任何全局配置） |
| SSH push | 成功（§3.2） |

结论（CONFIRMED）：`gh auth status` 成功不能推断 `git push` 成功；两种协议用的是不同凭证。S04 探测须对**每个目标仓库**按实际 remote 协议验证读（`git ls-remote`）和写，并记录实际协议；“写探测”的具体手段（例如向一次性任务分支推送，或受限的 dry-run）本 spike 未比较，需 S04 选定并验证。

### 3.4 Agent 自主完成（同一提示词、同一 schema、独立 clone）

| Provider/配置 | 结果 | 远端核验 |
| :--- | :--- | :--- |
| Claude `-p --permission-mode acceptEdits --allowedTools "Bash(go test:*)" "Bash(git:*)" "Bash(gh pr create:*)" Edit Read Glob Grep` | 一次完成：修复→commit→push `task/spike-claude-fix`→PR #3；自报与远端一致 | `git ls-remote` 的 ref SHA = 自报 `b7f674a…`；PR #3 base=`fixture/failing-test`、仅改 `calc/calc.go`（+2/−2）；commit 含 `Co-Authored-By: Claude …` 尾注（未认证文本） |
| Codex `--sandbox workspace-write` | 修复并通过测试，但**无法 commit**，如实上报 `.git/index.lock: Operation not permitted`；未 push、无 PR | 本地仅工作区有修改；`codex sandbox` 独立复现同一错误（§5.2） |
| Codex `--sandbox danger-full-access`（重置工作区后重试） | 一次完成 commit/push/PR #4；自报与远端一致 | ref SHA `5bb9fcb…`；PR #4 仅改 `calc/calc.go`（+1/−1），commit 无来源尾注 |

等级：DATA_CONFIRMED（三次均以 GitHub 事实交叉核验）。两个 Provider 都遵守了提示词里“不得推 main/fixture”的规则，但这只是 OBSERVATION_ONLY：沙箱分支未受保护（§8），技术上没有阻止它们。

## 4. Q3：取消、超时与对账

### 4.1 终止进程树

进程树结构（DATA_CONFIRMED）：Codex = `zsh → node(/opt/homebrew/bin/codex) → 原生 codex`；Claude = 单进程 + 每个用户 MCP 配置一个 `npm exec` 子树；Codex 工具命令（`zsh -lc …`）由 Codex 放入**各自独立的进程组**。

| 实验 | 命令要点 | 观察 | 等级 |
| :--- | :--- | :--- | :--- |
| 整组 SIGTERM（监管者以 `start_new_session` 启动，死线后 `killpg`） | Codex 正在跑 `sleep 197` | Provider 本体退出，包装进程 returncode 0；`sleep 197` 以 `ppid=1`、独立 pgid **存活** | CONFIRMED |
| 同上，Agent 派生 `setsid` 后台进程 | `nohup python3 -c "os.setsid();time.sleep(300)" <marker> &`（`sleep 2` 后返回） | Codex、Claude 两种 Provider 下都**存活**（`ppid=1`，新会话） | CONFIRMED |
| 同上，但不加 `sleep 2` | Codex 的工具命令一返回，`setsid` 来不及执行 | 该后台进程**未存活**（疑为随工具进程组被回收；竞态） | STRONG_INFERENCE |
| 仅杀领导进程 SIGTERM | Codex 包装进程 | node 包装与原生进程均退出；工具子进程 `sleep 150` 存活 | DATA_CONFIRMED |
| 仅杀领导进程 SIGKILL | Codex 包装进程 | node 进程被杀，**原生 codex 进程 `ppid=1` 仍运行** | DATA_CONFIRMED |
| 仅杀领导进程 SIGTERM | Claude | 退出 143，MCP 子进程树全部退出 | DATA_CONFIRMED |
| 环境标记发现 | 在启动环境放 `KOWA_SPIKE_TASK=<id>`，事后 `ps -A -E -o pid,command` 过滤 | 能找到逃逸的 `setsid` 进程（标记经两种 Provider 均透传，§6.3）；macOS 无 cgroup | CONFIRMED（发现可行）；进程主动清洗环境可规避：STRONG_INFERENCE |

清理：实验后我手工 `pkill` 了所有 marker/`sleep 15x` 进程并计数为 0（`ps | grep … | wc -l` = 0）。

含义：取消不能只依赖“杀进程组”或“杀领导进程”。S04 需要“进程组 + 环境标记扫描 + 工作区级清理”并如实记录残留；macOS 同账号下无法保证零残留（STRONG_INFERENCE，未评估专用系统用户/容器方案）。取消后 Provider 内部在途的 `git push` 子进程亦可能完成（§4.2 同理）。

### 4.2 超时后外部副作用未知时的对账

在 `exp/spike-s04-timeout` 上以递增死线（SIGTERM）运行：

```bash
python3 sup.py --timeout D -- git push origin exp/spike-s04-timeout      # D = 0.5, 1.5, 2.5, 3.5
git ls-remote origin refs/heads/exp/spike-s04-timeout
python3 sup.py --timeout D -- gh pr create --repo <R> --base main --head exp/spike-s04-timeout ...   # D = 0.3 … 2
gh pr list --repo <R> --head exp/spike-s04-timeout --state all --json number,state,headRefOid
```

| 操作 | 结果 | 等级 |
| :--- | :--- | :--- |
| `git push`，死线 0.5/1.5/2.5 s | 被杀（returncode −15），远端无该分支 | DATA_CONFIRMED |
| `git push`，死线 3.5 s | **客户端被杀（−15），但远端分支已存在且 OID 等于本地提交** | DATA_CONFIRMED |
| `gh pr create`，死线 0.3–1.5 s | 被杀，无 PR | DATA_CONFIRMED |
| `gh pr create`，死线 2 s | **客户端被杀、无任何输出，PR #2 已创建，head OID 正确** | DATA_CONFIRMED |
| 重复 `gh pr create`（同 head/base） | 退出 1，stderr `a pull request for branch "…" into branch "main" already exists:` + 已有 PR URL | DATA_CONFIRMED |
| 关闭 PR 并删除分支后 | 分支列表干净，但 `refs/pull/N/head` 隐藏引用保留，PR 编号单调递增 | DATA_CONFIRMED |

对账配方（CONFIRMED 可行）：push 用 `git ls-remote origin refs/heads/<b>` 比对期望 OID；PR 用 `gh pr list --head <b> --state all --json number,state,headRefOid`，并以 `headRefOid` 判断是否为本次任务的 PR。重复创建被 GitHub 拒绝且回显已有 URL，因此“先查后建”与“建后失败再查”都收敛，但**不能以 `gh pr create` 的退出码或输出作为“是否创建”的依据**。

## 5. Q4：凭证与目录可见范围

仅在假数据、沙箱仓库上；只检查“可读/字节数/权限位”，不输出内容。探测脚本见附录 A.3。

### 5.1 无沙箱的任务进程（Claude Bash 工具、Codex `danger-full-access`、朴素 Runner 子进程）

| 探测 | 结果 | 等级 |
| :--- | :--- | :--- |
| 读 `~/.ssh/id_ed25519` | 可读，411 字节 | CONFIRMED（直接 + Claude 内） |
| 读 `~/.codex/auth.json` | 可读，3,968 字节 | CONFIRMED |
| 读 `~/.config/gh/hosts.yml` | 可读（82 字节，令牌在 keyring） | DATA_CONFIRMED |
| `gh auth token \| wc -c` | 41（经 Keychain 取得令牌；未打印） | CONFIRMED |
| 出站网络 | SSH 与 HTTPS 到 GitHub 均通 | DATA_CONFIRMED |
| 其他仓库写权限 | `gh api repos/SnWalker/kowa --jq .permissions` → `admin/push:true`（Kowa 源仓）；沙箱仓同；令牌 scope 含 `repo`。**未对任何非沙箱仓做写入尝试** | DATA_CONFIRMED（权限元数据） |
| 其他目录 | 受保护 WIP 目录文件可读；文件按权限位对当前用户可写（`test -w`，未写入） | DATA_CONFIRMED |

### 5.2 Codex 的 seatbelt 沙箱

用 `codex sandbox [-c sandbox_mode="workspace-write"] -- sh probe.sh` 与 Agent 实际运行双重确认：

| 探测 | 默认 `workspace-write` | `writable_roots=[.git]` + `network_access=true` |
| :--- | :--- | :--- |
| 写工作区文件 | 允许 | 允许 |
| 写 `.git`（`git add/commit`） | **拒绝**：`Unable to create '.git/index.lock': Operation not permitted` | 允许，commit 成功 |
| 写 `$HOME` | 拒绝 | 未测（默认拒绝） |
| 写 `/private/tmp` 下工作区外目录 | **允许**（临时目录是可写根） | 未测 |
| 读 SSH 私钥 / `auth.json` / `hosts.yml` | **均可读**（411 / 3,968 字节，`codex sandbox` 与 Agent 内双重确认） | 未测（读策略未改，网络放开后可外传：STRONG_INFERENCE） |
| `gh auth token`（Keychain） | 0 字节 | **41 字节** |
| 联网 | 拒绝（SSH 解析失败、`curl` 代理连接失败、`gh api` `operation not permitted`） | SSH 登录 `Hi SnWalker!` |

CONFIRMED。含义：Codex 沙箱不保护读侧，只限制写和联网；一旦为了 push 打开网络，Keychain 令牌与可读的私钥即可被 Agent 取走/外传。常见的“把 Coder 关进沙箱”并不能同时满足 common 的“Coder 提交并推送”。

### 5.3 环境变量与用户级配置

| 观察 | 结果 | 等级 |
| :--- | :--- | :--- |
| 环境透传 | 父进程设 `FAKE_API_TOKEN=fake-not-a-secret`、`KOWA_SPIKE_TASK=taskenv`；Claude、Codex（两种沙箱）的工具进程均能看到这两个变量，以及本机的 `ANTHROPIC_BASE_URL`、`CLAUDE_CODE_MESSAGING_TOKEN` | CONFIRMED：Provider 不按 `TOKEN/KEY` 命名过滤 |
| 用户级配置带入 | Claude 默认加载用户 MCP（含 github 服务）；Codex 读 `~/.codex/config.toml` 与 `~/.codex/AGENTS.md` | 前者 DATA_CONFIRMED；后者 STRONG_INFERENCE（文件存在且默认加载，未直接观察到注入） |
| 隔离开关 | Claude `--strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources project`、Codex `--ignore-user-config --ignore-rules` 均正常工作 | DATA_CONFIRMED |

## 6. Q5：小任务成本与耗时

任务：fixture/failing-test 上修复 `Clamp` 上界并开 PR（同一提示词与 schema，独立 clone，clone 约 6 s）。

| 尝试 | 配置 | 墙钟 | token | 命令/轮次 | 结果 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Codex #1 | `workspace-write` | 102.5 s | in 180,811（cached 157,440）/ out 2,918 / reasoning 345 | 13 条命令 | 修复成功，**无法 commit**，未交付 |
| Codex #2（重试） | `danger-full-access` | 87.9 s | in 173,696（cached 160,512）/ out 2,540 / reasoning 131 | 15 条命令 | 交付，PR #4 |
| Claude | `acceptEdits` + 工具白名单 | 37.3 s（`duration_ms` 32,139，API 15,113） | in 14 / cache_read 219,980 / cache_create 38,162 / out 1,253；**$0.2092** | 9 轮；1 次 `permission_denials`（含管道的复合 Bash 命令不在白名单） | 一次交付，PR #3 |

DATA_CONFIRMED。Codex 订阅制不给美元数字，只给 token；两者 token 口径不同，不宜直接换算。Claude 默认配置带入 MCP/用户设置的开销大（§2.2：首调用 cache 创建 32.7k→4.7k），上表 Claude 行为默认配置，隔离后应更低（该任务未用隔离配置复测：HYPOTHESIS）。重试次数：Claude 0；Codex 1（第 1 次因沙箱不能交付）。

异常（STRONG_INFERENCE）：Codex #1 事件流里没有 `git commit` 的 `command_execution` 记录，但 Agent 报告了该错误并被独立复现证实——被沙箱拒绝的命令可能不进入 `--json` 事件流，因此不能只靠事件流做证据，需结合 Git/GitHub 事实。

## 7. Q6：Server 读取 GitHub 事实

```bash
gh pr view <n> --repo <R> --json number,author,state,headRefOid,baseRefName,mergeCommit,reviewDecision,reviews,statusCheckRollup
gh api repos/<R>/pulls/<n>/reviews
gh api repos/<R>/commits/<sha>/status            # combined status
gh api repos/<R>/commits/<sha>/check-runs
gh api repos/<R>/git/ref/heads/<branch>          # 当前 head OID
gh api repos/<R>/activity                        # push/branch 事件与 actor
gh api rate_limit
```

| 事实 | 实测 | 等级 |
| :--- | :--- | :--- |
| PR / head / 合并 OID | 沙箱 PR #1：`state:OPEN, mergeCommit:null, headRefOid:e7aeaf6…, mergeable:MERGEABLE, reviewDecision:"", statusCheckRollup:[]`。已合并例子（Kowa 源仓 PR #14，**只读**）：`mergeCommit.oid=d09bed7…`，其父为 `[3ff972b, fa7da4b]`（head），`mergedBy=SnWalker`，检查 `lint/web-verify/verify` 均 SUCCESS | DATA_CONFIRMED |
| Review | 沙箱 `pulls/1/reviews` 长度 0；Kowa PR #14 `reviews:[]`（自合并，无 Review）。**未能验证“非作者 Review”**——需第二个 GitHub 账号 | DATA_CONFIRMED（空）/ HYPOTHESIS（非作者 Review 语义） |
| CI 空洞陷阱 | 沙箱无 Actions：`commits/<sha>/status` → `state:"pending", total_count:0`；`check-runs.total_count:0`。**“pending”并不表示有检查在跑**，Server 必须区分“无检查”与“检查中” | DATA_CONFIRMED |
| 限流 | `gh api rate_limit`：core 5,000/小时，graphql 5,000/小时（该次查询时剩余 5,000 与 4,998） | DATA_CONFIRMED |
| 凭证路径 | 同机个人 gh（keyring 令牌，scope `gist, read:org, repo`）可完成以上全部读取；该令牌具写能力，“只读”仅是调用约定 | DATA_CONFIRMED |

账号漂移检测（命令均已执行）：

| 检查 | 命令 | 结果 |
| :--- | :--- | :--- |
| 活动账号与固定身份 | `gh api user --jq '{login,id}'` | `{"id":135722937,"login":"SnWalker"}`：用不可变数字 `id` 固定，不用 `login` |
| 机器可读授权状态 | `gh auth status --active --json hosts` | `{"hosts":{"github.com":[{"state":"success","active":true,"login":"SnWalker","tokenSource":"keyring","scopes":"gist, read:org, repo","gitProtocol":"ssh"}]}}` |
| 无登录 | `GH_CONFIG_DIR=<空目录> gh api user` | 退出 4，提示 `gh auth login` |
| 令牌失效 | `GH_TOKEN=<无效> gh api user` | 退出 1，`Bad credentials`（HTTP 401） |
| scope 变化 | `gh api -i user` 响应头 `X-Oauth-Scopes` | `gist, read:org, repo` |
| SSH 身份 | `ssh -o BatchMode=yes -T git@github.com` | `Hi SnWalker!`（退出 1） |

DATA_CONFIRMED。未执行 `gh auth switch`（账号级设置，超出授权）：切换活动账号会让运行中的 Server/Runner 静默换身份——STRONG_INFERENCE（gh 文档语义），因此每个任务前后都应复核 §3.2 的三个身份来源一致且等于预期固定 id。

## 8. 证据不足与未验证项

| 项 | 状态 |
| :--- | :--- |
| 沙箱分支保护 | **不可用**：免费私有仓 `branches/main/protection` 与 `rulesets` 均 HTTP 403 “Upgrade to GitHub Pro or make this repository public”。`main`/`fixture/*` 未受保护，当前对它们的保护仅是纪律；Agent 强推/删除分支在技术上不被阻止（STRONG_INFERENCE：admin 权限 + 无保护） |
| 非作者 Review / 合并流程 | 未测：无第二个 GitHub 账号；合并被禁止 |
| Provider session 恢复（`codex exec resume`、`claude --resume`） | 未测 |
| Claude Code 自带 sandbox 设置、`--dangerously-skip-permissions` | 未测 |
| 进程清洗环境后的逃逸发现、专用系统用户/容器隔离 | 未测 |
| 并发多任务、Runner 重启、长轮询取消延迟 | 未测（属 S04 实现范围） |
| Agent 强推/删远端分支等越权 | 未实际尝试（不做越权尝试） |
| 细粒度只读 PAT / GitHub App installation token 作为 Server 读凭证 | 未测（账号级配置） |
| Codex 被沙箱拒绝的命令是否出现在事件流 | 仅观察到一次缺失，未系统验证 |

## 9. 文档差异

| 位置 | 差异 |
| :--- | :--- |
| [MVP 真实集成验收方案 §1](../Kowa验收与运行/MVP真实集成验收方案.md) | 环境观察为 2026-09-26：`codex-cli 0.154.0`、`claude` 不在 PATH、未验证 Git credential helper。当前为 `0.157.1`、`claude 2.1.285` 在 PATH、helper 仅系统级 osxkeychain 且 HTTPS 无凭证。该节已过期，建议后续治理会话刷新 |
| [S04 阶段](../Kowa后端设计/stage/S04-RunnerProvider执行闭环.md)“当前问题与证据” | `HYPOTHESIS：Claude 可用性、取消延迟和 shell 沙箱能力待实测`、`证据不足项：真实 Provider 登录、Git remote、子进程停止和凭证可见范围` 现已有实测，可升级为 DATA_CONFIRMED / CONFIRMED |
| 沙箱 README“Reset” | 称“把 main 或 fixture 还原到 `baseline` 标签的提交”。`fixture/failing-test` 当前为 `0a62e29…`（baseline `d4943b0…` 之上一个提交），按字面还原会丢失故障。已在[测试仓库约定](../Kowa验收与运行/测试仓库约定.md)记录正确 SHA；修改沙箱 README 需用户操作 |

## 10. S04 拆分建议（仅建议）

实测表明现行 S04 一个阶段混合了五类风险：进程监管、Provider 适配、受控 Git/gh、Server 观测/配置/Web 查询、组合根。建议用户评估拆成四个顺序子阶段（是否拆分、编号与状态表变更需另行批准）：

| 子阶段 | 范围 | 主要验收 | 外部依赖 |
| :--- | :--- | :--- | :--- |
| S04-1 Runner 协议与监管内核 | 出站长轮询、租约/心跳/报告客户端、统一 Worker host、`scripted fake` Provider、严格结果解码（§2.4）、取消/超时/进程树清理（§4.1）、断连/重复/截断 | 全 fake；B11、C07、旧租约、响应丢失；不触碰真实 Provider/GitHub | 仅 S02 v1 |
| S04-2 Provider Adapter 与 Runtime 探测 | Codex 优先、Claude 次之；能力阶梯（存在→版本→登录→最小真实执行，§2.1）；固定调用基线（显式 `-m`、关 stdin、隔离用户配置、环境白名单）；Runtime observation 上报 | 能力 unavailable 不伪装；失败矩阵（§2.3）覆盖；少量真实 CLI 调用 | 本机 CLI；沙箱仓库非必需 |
| S04-3 工作区物化与 Git/gh 证据 | 冻结引用物化项目/知识仓、受控 Git/gh（协议选择、写探测、三身份一致性核对）、push/PR 对账（§4.2）、前后 OID 与命令证据采集、凭证与目录负向检查（§5） | 沙箱仓库真实 clone/push/PR/对账；知识仓只读负向 | 沙箱仓库；**知识库只读负向需第二个仓库** |
| S04-4 Server 观测、配置与 Web 查询、组合根 | Runtime observation/transport 与 runtime-web 合同、Workspace 授权范围、执行/验证配置（管理员、版本/幂等）、Server 对 GitHub 的只读观测端口与漂移检测（§7）、真实组合根 | 跨 Workspace/撤权/旧 epoch/陈旧水位正反测试；组合根 | 依赖 S04-1—3 的冻结输出 |

需在 S04 开工前由用户裁决的设计问题见 §11，其中沙箱策略（C1）直接决定 S04-2/S04-3 的合同形态。

## 11. 与 common / decision 的冲突及需批准事项

本节只列出，不修改任何权威文档。

| 编号 | 事项 | 证据 | 需要的裁决 |
| :--- | :--- | :--- | :--- |
| C1 | common §2.7/§3.3 写“Coder 在获准的项目工作分支上以 Runner 本机 GitHub 身份提交、推送”。Codex 默认 `workspace-write` 不能 commit/联网（§5.2）。可行路径：①`danger-full-access`（Agent 拥有宿主全部访问）；②`workspace-write`+`writable_roots=[.git]`+`network_access=true`（仍可读私钥，并暴露 Keychain 令牌）；③Runner 代 Coder 执行 commit/push（改变“Coder 推送”的字面语义，push 者仍是机器账号）；④仅用 Claude 作为 Coder | 同上，§3.4 | 选择路径；若选③或改变 §2.7/§3.3 表述，需 common/decision 修订批准 |
| C2 | decision “MVP 直接使用 Runner 本机个人 gh 身份”称只能在“明确 GitHub 分支保护、审计与负向验收条件下”评价能力。免费私有沙箱无法启用分支保护（§8） | 403 | 是否提供支持保护的验收仓库（公开仓、付费计划或另一账号），还是明确放弃该条件 |
| C3 | 沙箱 `main`/`fixture/*` 无技术保护 | §8 | 是否接受“纪律 + 会话前后 ref 断言”作为沙箱阶段的缓解 |
| C4 | Server 读 GitHub 用同机个人 gh：令牌 scope 含 `repo`，具写能力 | §7 | 是否采用细粒度只读 PAT 或 GitHub App installation token（账号级配置，由用户创建/授权） |
| C5 | 非作者 Review、合并观测需要第二个真人 GitHub 账号与对沙箱的协作者邀请 | §7 | 用户提供并邀请（修改仓库设置，AI 不可做） |
| C6 | 知识库只读负向、双仓物化需要第二个仓库 | §10 | 何时、以何名称创建（建议 `SnWalker/kowa-sandbox-knowledge`，私有，仅假数据），由用户批准或创建 |
| C7 | 本次沙箱内 push/PR 的授权只覆盖本会话。是否要在 AGENTS §6 增加“沙箱非默认分支写入”的常设授权，由用户决定；本报告与约定文档**不构成**常设授权 | 授权文本 | 保持逐会话授权，或批准修订 AGENTS |
| C8 | 用户全局 `~/.codex/config.toml` 的默认模型 `gpt-6.1-sol` 对当前账号无效 | §2.1 | 属环境问题：由用户修正；Kowa Runner 无论如何必须显式传 `-m` |
| C9 | MVP 验收方案 §1 环境观察过期；S04 阶段证据措辞可升级 | §9 | 在后续治理会话中刷新（本会话未改） |

## 12. 沙箱写入清单与清理证据

本会话在 `SnWalker/kowa-sandbox`（保持 private，设置未改）的全部写入：

| 对象 | 内容 |
| :--- | :--- |
| 分支 | `exp/spike-s04-identity`、`exp/spike-s04-https`、`exp/spike-s04-timeout`、`task/spike-claude-fix`、`task/spike-codex-fix` |
| PR | #1（identity）、#2（timeout，由超时 `gh pr create` 实际创建）、#3（Claude 修复）、#4（Codex 修复）；均未合并 |
| 清理 | `gh pr close 1 2 3 4`；`git push origin --delete` 五个分支 |
| 终态 | `git ls-remote origin`：仅 `refs/heads/main`（`d4943b0`）、`refs/heads/fixture/failing-test`（`0a62e29`）、`refs/tags/baseline`（`d4943b0`），外加 GitHub 保留的 `refs/pull/1..4/head`；PR 全部 CLOSED；`visibility: private` |

偏差（如实披露）：①`gh pr close` 时附带了一条关闭评论（`--comment "Kowa S04 spike finished; closing disposable PR."`），评论不在授权清单内，仅出现在这 4 个一次性 PR 上；②用 `git push origin --delete` 删分支而非清单中的 `gh pr close --delete-branch`，效果等价；③误将一个重定向文件写到 `/tmp/dup.out`（随即删除）。没有向 `main` 或 `fixture/*` 推送，没有合并，没有评审/评论以外的写，没有修改任何其他仓库。

## 13. 2026-10-10 复核更正：Codex 模型可用性

- 原 §2.1 的结论“Codex 默认模型 `gpt-6.1-sol` 对当前账号不可用（HTTP 400，须显式 `-m`）”在 2026-10-10 复核时**不再复现**：本机 `codex-cli` 已由 0.157.1 升至 **0.162.1**，`codex exec --skip-git-repo-check --ephemeral --sandbox read-only "…" </dev/null` 使用 `~/.codex/config.toml` 的默认模型直接成功（退出 0、正常返回内容），交互式 TUI 亦正常。差异来自 CLI 版本还是服务端变化无法区分，按“0.162.1 + 当前账号：默认模型可用”记录。
- 仍需保留：非交互调用须关闭 stdin（2026-10-02 观察，本次未重测）；Runner 仍应显式固定实际使用的模型与调用参数，不把“本机默认”当作契约。
- 本报告其余各节结论（凭证可见范围、沙箱能力、取消与对账等）本次未复核，仍以 2026-10-02 的实测为准。

## 附录 A：辅助脚本（scratchpad 内，非产品代码）

### A.1 监管脚本 `sup.py`（新会话 + 死线 + 杀进程组 + 幸存者检查；stdin 恒为 /dev/null）

```python
import argparse, json, os, signal, subprocess, time
ap = argparse.ArgumentParser()
ap.add_argument("--timeout", type=float, required=True)
ap.add_argument("--kill-after", type=float, default=5)
ap.add_argument("--stdout"); ap.add_argument("--stderr"); ap.add_argument("--cwd")
ap.add_argument("argv", nargs=argparse.REMAINDER)
a = ap.parse_args(); argv = a.argv[1:] if a.argv and a.argv[0] == "--" else a.argv
out = open(a.stdout, "wb") if a.stdout else subprocess.DEVNULL
err = open(a.stderr, "wb") if a.stderr else subprocess.DEVNULL
t0 = time.time()
p = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err, cwd=a.cwd, start_new_session=True)
pgid = p.pid; res = {"argv0": argv[0], "pid": p.pid, "pgid": pgid}
try:
    p.wait(timeout=a.timeout); res["timed_out"] = False
except subprocess.TimeoutExpired:
    res["timed_out"] = True; os.killpg(pgid, signal.SIGTERM)
    try: p.wait(timeout=a.kill_after)
    except subprocess.TimeoutExpired: os.killpg(pgid, signal.SIGKILL); p.wait()
res["returncode"] = p.returncode; res["elapsed_s"] = round(time.time() - t0, 2)
ps = subprocess.run(["ps", "-A", "-o", "pid=,pgid=,command="], capture_output=True, text=True).stdout
res["survivors_in_pgid"] = [l.strip()[:160] for l in ps.splitlines() if l.split()[1:2] == [str(pgid)]]
print(json.dumps(res))
```

### A.2 仅杀领导进程 `leaderkill.py`

```python
import os, signal, subprocess, sys, time, json
sig = getattr(signal, sys.argv[1]); delay = float(sys.argv[2]); argv = sys.argv[3:]
p = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
time.sleep(delay)
def tree(pgid):
    out = subprocess.run(["ps", "-A", "-o", "pid=,ppid=,pgid=,command="], capture_output=True, text=True).stdout
    return [l.strip()[:110] for l in out.splitlines() if len(l.split()) > 3 and l.split()[2] == str(pgid)]
before = tree(p.pid); os.kill(p.pid, sig); time.sleep(4)
print(json.dumps({"signal": sys.argv[1], "leader_returncode": p.poll(), "tree_before": before, "tree_after": tree(p.pid)}, indent=1))
```

### A.3 凭证可见性探测 `probe.sh`（只输出布尔值/字节数）

```sh
echo "[write cwd]";  touch ./cwd-write-probe && echo cwd_write=ok; rm -f ./cwd-write-probe
echo "[write outside cwd]"; touch "$OUTSIDE/outside-probe" && echo outside_write=ok || echo outside_write=denied
test -r "$HOME/.ssh/id_ed25519"      && echo "ssh_key_readable bytes=$(wc -c < "$HOME/.ssh/id_ed25519")"
test -r "$HOME/.codex/auth.json"     && echo "codex_auth_readable bytes=$(wc -c < "$HOME/.codex/auth.json")"
test -r "$HOME/.config/gh/hosts.yml" && echo gh_hosts_readable
echo "gh_token_bytes=$(gh auth token 2>/dev/null | wc -c)"
ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com 2>&1 | head -1
curl -sS -m 5 -o /dev/null -w "http=%{http_code}\n" https://api.github.com/
env | cut -d= -f1 | grep -i -E 'token|secret|key|gh_|github|anthropic|openai' | tr '\n' ' '
test -r /Users/liufei/workspace/kowa/AGENTS.md && echo wip_dir_readable
test -w /Users/liufei/workspace/kowa/AGENTS.md && echo wip_file_writable_by_permission_bits   # 仅权限位，未写入
gh api repos/SnWalker/kowa --jq '.permissions.push'                                           # 仅元数据
```
