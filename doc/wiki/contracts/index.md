# contracts 索引

记录跨端 HTTP、事件、IDL、Artifact、Agent 任务与结果等稳定接口及其兼容语义。

不记录某端内部类型、具体适配器实现或尚未批准的候选接口。每份契约必须说明 producer、consumer、版本及失败语义。

| 页面 | 权威范围 | 状态 | 最近核验 |
| :--- | :--- | :--- | :--- |
| [执行尝试与重复投递合同](execution-attempts.md) | 已批准的 Task 身份、Retry、人工续接与重复投递行为；不定义 wire | active | 2026-09-25 |
| [MVP 跨端最小语义契约](mvp-cross-end-semantics.md) | 共享合同唯一 owner、版本身份、GitHub 交付与失败语义；不定义 wire/DDL | active | 2026-09-27 |
