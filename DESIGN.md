# 设计文档

## 范围与金额

本项目是 Go + SQLite + Vue 3 的本地全额退款演示，不接入真实支付。订单与退款在 API、数据库均用整数 `amount_cents`（人民币分）；页面按元格式化。前端只提交 Bearer 令牌、幂等键和原因，金额、币种、商户及原渠道均由后端读取。`A-1001` 成功、`A-1002` 响应丢失、`A-1003` 明确失败、`A-1004` 未支付；申请不能覆盖渠道模式。

## 数据、并发与恢复

| 表 | 关键字段与约束 |
| --- | --- |
| `orders` | `id` 主键；`merchant_id`、`amount_cents`、`currency`、`status`、`provider_channel`；金额大于零；商户索引 |
| `refunds` | `id` 主键；`order_id` 唯一且引用订单；`(merchant_id, idempotency_key)` 唯一；全额、状态、原因、原渠道、`provider_request_id`、时间戳 |
| `provider_records` | `(provider_channel, provider_request_id)` 联合主键；订单、金额、结果、提交次数；仅模拟器通过 `Provider` 接口读写 |

服务端映射令牌到商户与角色，读取按商户过滤；跨商户对象返回 `404`，只读写入返回 `403`。同键同订单、同原因返回原退款；异参返回 `409`。服务先提交 `PROCESSING` 预占和稳定渠道请求号，数据库唯一约束裁决并发；只有预占成功的请求调用渠道。`PROCESSING → SUCCEEDED / FAILED / UNKNOWN`，`UNKNOWN → SUCCEEDED / FAILED / UNKNOWN`，终态不倒退。超时保留 `UNKNOWN`，查证只用原请求号调用 `QueryRefund`，不重提。SQLite 使用外键、WAL、busy timeout；不依赖分布式锁。渠道受理后崩溃可能留下非终态，需人工查证。

初始化可重复执行且不清空退款。匹配原种子 ID、商户、金额、渠道的旧 USD 演示订单及关联退款会仅更正币种为 CNY，保留金额、状态和退款 ID；其他数据不变。`reset-demo` 是单独的显式清空命令。详细列类型、迁移与查证流程见 [.openspec/changes/refund-backend/design.md](.openspec/changes/refund-backend/design.md)。

## API

所有 `/api` 请求需要 `Authorization: Bearer <token>`。`GET /api/orders` 返回当前商户列表；`GET /api/orders/{id}` 返回订单及退款摘要（有退款时含渠道请求号）；`POST /api/orders/{id}/refunds` 需要 `Idempotency-Key` 与 JSON `reason`；`GET /api/refunds/{id}` 读取状态；`POST /api/refunds/{id}/reconcile` 查证原渠道。错误返回稳定 `code`、`message`；请求级 `X-Demo-Outcome` 覆盖返回 `400`。

## 前端设计系统

工作台使用简体中文、紧凑列表和蓝色主操作（`#2563eb`，悬停 `#1d4ed8`）；页面背景 `#f5f7fa`、表面白色、正文 `#202a35`，金额使用等宽字体。控件与容器圆角 4–7 px，内容最大宽 1240 px。状态同时显示文字和颜色：订单已支付蓝色/未支付灰色；支付已支付绿色/未支付琥珀色；退款处理中蓝色、未知琥珀色、成功绿色、失败红色。支付状态目前由订单状态推导，并非独立渠道账本。

无有效 `refunddesk_identity` cookie 时先选演示身份；cookie 仅保存身份键，不是认证凭据。切换用户清除旧商户内存数据并重新请求；选择页标记上次身份。列表的订单号和“详情”入口均进入 `/orders/{id}`，页面只取当前身份的详情 API，不能靠 URL 或列表缓存绕过后端鉴权。

退款弹窗按原因、金额确认、提交、结果四阶段动画过渡，固定最大宽 560 px、高 720 px，内部溢出滚动，避免尺寸跳动。提交至少展示 1 秒 loading，并等待真实 API 结果；成功、明确失败、未知分别给出可理解的闭环提示，未知只允许查证原单。操作提示在顶部浮层展示 2 秒。弹窗有焦点管理、Escape、可见标签及降低动态效果支持；窄屏收紧边距。具体样式以 `frontend/src/style.css` 为准。

## 限制与证据

静态演示令牌、免密选择与模拟渠道不适用于生产；生产需真实身份管理、密钥管理、渠道幂等及长期未知状态告警。自动化覆盖金额、同键与并发、重启恢复、失败/未支付、商户隔离及前端异步状态，命令与复现步骤见 [README.md](README.md)。
