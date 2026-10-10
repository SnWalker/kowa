# 固定 MVP 工作流与执行边界

状态：active（已批准设计基线，未实现）。最近核验：2026-09-27。

本文拥有一条固定流程与后续扩展的关系、Runner 通信边界；具体作者格式、编译字段及 wire 仍见 [扩展方案](../../research/workflow-capability-design.md)。上位共识见 [common](../../common.md)，选择原因见 [decision](../../decision.md)。

## 1. 一条流程与可扩展性

MVP 只发布一条固定内置工作流，用户创建工作项时不选择多种模板。固定的是首期产品范围，不是把每个步骤写成只适用于该流程名的调度分支。

扩展分三种：

| 变化 | 作用对象 | 约束 |
| :--- | :--- | :--- |
| 新增流程 | 新定义引用已存在能力 | 不复制能力实现，不另建调度器 |
| 已有流程增加 Agent 节点 | 新定义版本及新增依赖/输入绑定 | 旧运行保持原冻结定义；新运行采用获准新版本 |
| 新增业务能力 | 新 Capability 契约与 Worker 实现 | 经审阅登记后才供流程引用，不由自注册绕过准入 |

新增普通业务节点优先复用既有节点执行语义；只有新的编排控制行为才涉及 NodeType 扩展。当前只实现实际被 MVP 使用的能力，不为后期预造空 Worker、兼容别名或备用路由。

## 2. 控制面与执行面

控制面拥有定义冻结、调度、业务授权、状态、人工作用、证据接受与 GitHub 事实对账；它不执行模型或项目测试。Runner 通过统一运行器监管 Worker；Worker 实现 Capability，Provider Adapter 对接模型/CLI。MVP 执行面的远端 Git 操作由 Runner 受控通道在获准 Task 范围内使用所在机器预先登录的个人 `gh`/Git 身份完成，Coding Agent 只在任务工作区读写文件并运行本地命令；它与 Web 登录用户是不同身份。Server 判定业务授权，但 TaskSpec 不能缩减个人账号在 GitHub 已拥有的物理权限。一个 Runner 可以承载多个能力，角色名称不同不等于安全隔离或独立上下文已成立。

| 边界 | 持有的权限与职责 | 不授予的权限 |
| :--- | :--- | :--- |
| Server | 验证 Web 会话、Workspace 成员、仓库绑定、HumanTask、运行状态和操作版本；决定 Task 获准的仓库/ref/操作，接受或拒绝结果并核对 GitHub 事实；分别记录 Web 发起人、机器 GitHub 身份和 Agent 来源 | 不执行模型、项目构建或测试；不把机器账号的 GitHub 写入冒称为 Web 用户操作 |
| Runner | 用独立的 Kowa Runtime 凭证向 Server 注册、领取 Task、心跳、取得冻结输入、上传结果/产物；监管 Worker，确认本机 Provider 与个人 `gh`/Git 状态，执行获准的 Git 操作并上报远端证据；独立 `publish_pr` 节点创建 PR | 不自行扩大 Task 的仓库/ref/操作或门禁权限；不能单凭 Task 完成批准方案、Review、合并或关闭 WorkItem |
| Worker/Provider | 在 Runner 交付的任务上下文中执行指定 Capability；Coder 只在任务工作区读写文件并运行本地命令；项目仓的 clone/fetch/pull、commit 与获准工作分支 push 由 Runner 受控通道代执行；QA/评审按固定提交只读消费 | 不直连控制面业务命令接口；不把任务指令视为对宿主个人账号的 GitHub 硬权限限制，不得把知识仓当作可写交付仓 |

Server 在派发前核实成员/Workspace/资源授权，报告接受时再验证 Task、租约、轮次、输入版本和产物。Runner 只执行分配给自己且能力匹配的 Task；本地检查不能替代 Server 的业务裁决，也无法把同机个人 `gh` 账号约束成仓库/ref 级硬隔离。私有仓 fetch/pull/push 和 `gh` 操作依赖 Runner 主机实际可用的认证状态，不能从 `gh auth status` 推断 Git push 一定可用。Coder 推送后由 `verify_change` 核对远端仓库、ref 和 OID；必需测试与独立评审通过后才进入独立 `publish_pr` 节点。GitHub 事实由 Server 核对，不能以 Agent 自报替代。

首条真实业务闭环使用本机 Mac 上一个 Server 进程和一个独立 Runner 进程；Runner 可监管多个 Agent/Worker 执行进程。MVP 的模型 Provider 是 Runner 所在 Mac 已安装、已登录且可调用的 Claude Code、Codex 等 CLI，团队成员共用这些本机执行能力；Server 与 Runner 共用一个 macOS 账号，控制面仍分别记录任务发起人、运行环境和实际 Provider。完成首条闭环后，可在同机启动第二个独立 Runner，用新的任务专门验证能力匹配、租约过期、重新派发与产物交接。多个 Runner 共用一台 Mac 不是多机器故障域，也不会自动隔离系统用户、凭证或仓库目录。

NodeRun、Task 和 Provider session 的身份分开；尝试语义见 [执行尝试合同](../contracts/execution-attempts.md)。数据库和版本化引用持有事实，HTTP 连接与本地执行目录不拥有业务状态。

## 3. 出站长轮询

MVP 使用 Runner 发起的 HTTP 长轮询获取任务，另以 HTTP 心跳和报告沟通。控制面不要求 Runner 暴露可入站访问的地址。未来可替换为出站持久流，但必须保持任务身份、租约、报告与授权的同一语义。

长轮询返回任务只是交付尝试，不证明 Worker 已执行。实际执行前后都要核验授权、尝试身份和租约；响应丢失可能发生在执行前或执行后，不能据此猜测结果。

取消通过出站交互传递并收束，具体时限和 wire 需在接口设计中冻结。租约失效只能隔离控制面写回，不能自动撤销已经发生的 GitHub 副作用。心跳健康也不等于业务进度正常，需分别展示。

Runner 是独立进程，任务沙箱是另一个边界。历史执行机以独立进程承载 Agent，但没有可验证的同机任务沙箱；本机多人验收不能从“独立 Runner”“共用一个 macOS 账号”或“本机 CLI 已登录”推导出文件、进程、网络和凭证已隔离。MVP 的远端写由 Runner 受控通道使用宿主个人 `gh`/Git 状态完成（不交付给 Coder），因此该账号可访问的仓库与凭证范围仍是已知限制，需通过真实负向验收记录边界；项目脚本仍须验证不能读取 Server 凭证或其他任务目录。整项 Task 容器化不是调用宿主 CLI 的默认前提。若实测无法建立这些边界，本机部署只能报告受限的可信环境能力，不能声称已经具备多用户硬隔离。

## 4. 未冻结部分

YAML 字段与加载实现、最小 NodeType/表达式集合、静态目录 schema、长轮询路由/超时/批量大小、重连退避和实际取消延迟仍待接口审阅。MVP 的 Runner 受控 Git 通道路径已选定；Server/Runner 分机后 GitHub 事实读取凭证与更严格执行隔离需另行设计。本文批准范围不包含这些参数，不得仅凭 active 标签声称全套执行协议已冻结。
