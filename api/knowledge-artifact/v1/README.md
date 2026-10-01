# kowa.knowledge-artifact.v1

唯一 owner：backend:S03。机器合同 [schema.json](schema.json)；行为权威 [共享合同](../../../doc/wiki/contracts/knowledge-artifact-v1.md)。本目录冻结 ArtifactRef、KnowledgeSnapshot、BindingEvent、EffectiveInput 的 JSON 形状，不定义 HTTP 路由、Runner 凭证或 HumanTask wire。

## 字段与规范化

- ArtifactRef：schemaVersion、artifactId（服务端生成 48 位小写 hex）、workspaceId、subjectId、kind、producer（task/operation 二选一）、sha256 digest、size、mediaType、storageRef=artifact:<artifactId>、requiresApproval。正文摘要按原始字节计算；单个对象最大 16 MiB。storageRef 由控制面解析，不能使用本机路径或短期 URL。
- KnowledgeSnapshot：snapshotId=ks_<内容摘要 hex>、digest、content。content 固定 schemaVersion/workspaceId/repositoryId/40 位 commit/emptyResult/entries；entry 固定相对 path/digest/scope/required/base64 content。成功 Probe 后允许空 entries；不可达或缺文件不是空结果。
- BindingEvent：eventId、workspaceId、subjectId、snapshotId、humanResponseId、previousEventId、actorId、version、confirmedAt。actor/time 来自已认证服务端；确认不进入快照内容摘要。相同内容每次新答复保留新事件；同 actor/subject/response 同请求幂等、异请求冲突。
- EffectiveInput：inputId=ei_<内容摘要 hex>、digest、content。content 含需求版本、项目 commit、定义 digest、策略/配置版本、知识快照、绑定事件、答复、上游 name/nodeRunId/ref 与类型化 fields。上游按 name、fields 按 name 排序，不接受冲突/保留字段覆盖。RequiredUpstream 是服务端从冻结定义提供的必需绑定声明，不从 wire JSON 或 Provider fields 读取；其版本权威是 definitionDigest。

canonical 为 Go 1.25.14 encoding/json 对合同类型按声明字段顺序编码的 UTF-8 紧凑字节（默认 HTML escaping，entries 按 path 排序，数组空值为 []，content 字节为 base64）；确认事件不被加入知识内容。存储 canonical bytea，禁止从 JSONB 随意重序列化算身份。示例及 Go 断言冻结该字节规则。

## 生产者与消费者核对

| 角色 | 精确 seam / 必须消费的事实 |
| :--- | :--- |
| 知识 producer | Capture + Source.Probe/Read 必须对应绑定仓库精确 commit；DirectorySource 只读可信物化目录，实际 Git 物化由 S04 实现 |
| 发布/确认 owner | PublishKnowledgeSnapshot 只创建可读候选；ConfirmKnowledge 重查成员、知识仓、期望版本，事务提交快照/独立事件/当前绑定 |
| 输入 consumer | knowledge.Service.Assemble 校验上游正文；AssembleKnowledgeInput 重查当前确认及上游发布/批准状态，原子添加 input 引用与 canonical |
| Task producer | S02 的已接受 COMPLETED/AWAITING_INPUT result.output.artifactRefs 必须含完整精确 Ref；未接受或未列出的上传不能发布 |
| Runner consumer | S04 后续按冻结 EffectiveInput 与 ArtifactRef 获取/校验，不能读生产者目录或分支最新值；本阶段子进程契约测试模拟消费者 |
| Web consumer | 可展示已发布候选；可读不表示已批准。不能由预填字段、summary 或 URL 推断准入；实际 UI 后续显式依赖本版本 |
| 评测/关闭 consumer | 后续使用已发布且保留的精确引用，建立 run/closure 引用；S03 不实现门禁/关闭决策 |

未存在的 Runner/Web/关闭集成不伪称已实现。S03 提供稳定应用端口与消费者正反例，后续 consumer 阶段必须登记 owner/backend:S03 和此冻结版本。

## 错误与权限

Scope 来自认证适配器，不从请求正文相信 actor。读要求未撤销成员；写要求 admin/developer。禁止跨 Workspace。ErrValidation=VALIDATION_ERROR、ErrForbidden=FORBIDDEN、ErrConflict=VERSION_CONFLICT、ErrUnavailable/ErrIntegrity=RESOURCE_UNAVAILABLE、ErrUnpublished/ErrUnapproved/ErrUnconfirmed/ErrReferenced/ErrGracePeriod=POLICY_BLOCKED。具体 HTTP 映射后续接口阶段使用该语义；上传或校验失败绝不返回发布成功。

## 样例与门禁

5 个 positive 样例（Artifact、非空/空快照、确认事件、有效输入）；5 个 negative-schema（非法 digest、缺 workspace、未知字段、路径穿越、本机路径）；2 个 negative-semantic（结构合法但摘要或 storage identity 不符）。JSON Schema 不能验证摘要、授权、数据库状态或引用竞态，Go/PG 测试补足。

`mise exec -- make contract-check` 使用固定 jv v0.7.0，CLI 用 GOTOOLCHAIN=local 与项目工具链一致；依赖隔离于应用 go.mod。`mise exec -- make test-knowledge-artifact-integration` 使用一次性 PostgreSQL 17.11、迁移升降、独立下游子进程与 race；CI verify 必跑。
