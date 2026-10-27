# AI 协作记录

使用 AI 辅助梳理题目契约、按 TDD 编写 Go 与 Vue 测试及实现、审阅异常路径和整理中文文档。未使用真实支付接口、客户数据或生产密钥。以下记录的是代码决策与可重跑证据；测试通过不替代开发者的现场解释。

| 问题 | AI 建议 | 采纳与调整 | 验证 |
| --- | --- | --- | --- |
| 同一订单并发退款 | 先持久化预占记录，以数据库唯一约束裁决竞争 | 使用 `UNIQUE(refunds.order_id)` 和商户范围内的幂等键唯一约束；仅预占成功者提交渠道，不依赖前端禁用按钮 | `TestTenConcurrentRefundRequestsHaveNoServerErrors`、`TestRefundIdempotencyAndPayloadConflict` |
| 渠道响应丢失 | 将超时当作未知，保存原请求号后查询 | `mock-unknown` 先持久化渠道结果，再模拟丢包；业务保留 `UNKNOWN`，查证只调用原请求的 `QueryRefund` | `TestUnknownRefundRecoversByPersistedProviderRequest`、`TestUnknownRefundCanReconcileAfterDatabaseReopen` |
| 演示结果与金额口径 | 可选场景有利于界面演示，但会破坏公开契约 | 移除请求级结果选择；结果只由种子渠道决定。新种子及匹配旧演示记录采用 CNY 整数分，前端按元展示 | `TestRefundRequestCannotOverrideSeededChannelOutcome`、`TestContractSeedDataIsRepeatableAndPreservesRefunds`、`frontend/src/App.test.ts` |

未覆盖生产支付接入、真实身份认证、部分退款和长期未知状态的自动调度；本项目仅为本地演示。
