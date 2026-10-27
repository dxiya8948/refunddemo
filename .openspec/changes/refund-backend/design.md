# 退款后端技术设计

## 背景与目标

Go 服务实现了[退款后端规范](../../specs/refund-backend/spec.md)。主要风险是重试、并发或渠道响应丢失导致重复资金操作。因此，调用渠道前必须先持久化唯一的业务预占记录和渠道请求身份。本文描述现有实现，不构成生产支付接入授权。

目标：商户隔离、每笔已支付订单最多一次全额退款、同键重放、不确定结果持久化，以及重启后仅查询原请求的恢复机制。非目标：部分退款、外部身份签发、真实支付网关、独立支付状态账本。

## 模块与职责

| 模块 | 实现位置 | 职责 |
| --- | --- | --- |
| HTTP 边界 | `backend/internal/refunds/handler.go` | 路由、Bearer 身份、参数校验、商户隔离、JSON 响应 |
| 领域类型与渠道接口 | `backend/internal/refunds/types.go` | 订单/退款状态，`SubmitRefund` 与 `QueryRefund` 接口 |
| SQLite 存储与迁移 | `backend/internal/refunds/store.go` | 表结构、唯一约束、连接策略、非破坏性演示数据初始化 |
| 演示渠道适配器 | `backend/internal/refunds/provider.go` | 在独立渠道记录中保存确定性处理结果 |
| 组装入口 | `backend/cmd/server/main.go` | 数据库、渠道、令牌映射和 HTTP 服务 |

前端提供 Bearer 令牌、幂等键和退款原因；退款请求不得覆盖演示渠道结果。前端不提供权威商户 ID、退款金额、币种或渠道。`amount_cents` 是从订单复制的 `int64` 整数分，避免浮点金额计算；新演示数据使用 CNY，页面按人民币元展示。

## 持久化数据模型

### `orders`：订单来源

| 字段 | 类型与约束 | 含义 |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY` | 订单 ID |
| `merchant_id` | `TEXT NOT NULL` | 所属商户 |
| `amount_cents` | `INTEGER NOT NULL CHECK (> 0)` | 全额金额，最小货币单位 |
| `currency` | `TEXT NOT NULL` | 币种 |
| `status` | `TEXT NOT NULL` | 当前演示使用 `PAID` / `UNPAID`；前端支付状态由此推导 |
| `provider_channel` | `TEXT NOT NULL` | 原支付渠道 |

索引 `idx_orders_merchant(merchant_id, id)` 支持商户范围查询。当前没有独立的用户或支付状态表。

### `refunds`：业务退款与预占

| 字段 | 类型与约束 | 含义 |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY` | 退款 ID |
| `order_id` | `TEXT NOT NULL UNIQUE REFERENCES orders(id)` | 一单至多一条退款；启用外键检查 |
| `merchant_id` | `TEXT NOT NULL` | 所属商户，供隔离和幂等判定 |
| `amount_cents` | `INTEGER NOT NULL CHECK (> 0)` | 从订单复制的全额 |
| `currency` | `TEXT NOT NULL` | 从订单复制的币种 |
| `status` | `TEXT NOT NULL` | `PROCESSING` / `UNKNOWN` / `SUCCEEDED` / `FAILED` |
| `idempotency_key` | `TEXT NOT NULL` | 请求幂等键，和商户共同唯一 |
| `provider_channel` | `TEXT NOT NULL` | 原渠道名 |
| `provider_request_id` | `TEXT NOT NULL` | 首次预占时生成，供原请求查询 |
| `reason` | `TEXT NOT NULL DEFAULT ''` | 标准化退款原因，参与同键请求比对 |
| `failure_reason` | `TEXT NOT NULL DEFAULT ''` | 渠道明确失败的原因 |
| `created_at` / `updated_at` | `TEXT NOT NULL` | UTC 时间戳 |

另有 `UNIQUE(merchant_id, idempotency_key)`、索引 `idx_refunds_merchant(merchant_id, created_at DESC)`。`order_id` 的唯一性与商户内幂等键唯一性均以数据库约束为最终裁决，不依赖前端按钮禁用。

### `provider_records`：演示渠道受理记录

| 字段 | 类型与约束 | 含义 |
| --- | --- | --- |
| `provider_channel` / `provider_request_id` | `TEXT NOT NULL`，联合主键 | 渠道及原请求身份 |
| `order_id` | `TEXT NOT NULL` | 对应订单 ID |
| `amount_cents` | `INTEGER NOT NULL CHECK (> 0)` | 渠道受理金额 |
| `status` | `TEXT NOT NULL` | 模拟渠道处理状态 |
| `reason` | `TEXT NOT NULL DEFAULT ''` | 渠道失败原因 |
| `submit_calls` | `INTEGER NOT NULL DEFAULT 1` | 演示提交次数 |
| `created_at` | `TEXT NOT NULL` | 首次受理时间 |

业务代码只通过 `Provider` 接口访问该表，不将模拟器账本直接当作业务退款表读取。

`OpenDB` 开启外键、WAL 和五秒 busy timeout。每个服务进程只保留一条 SQLite 连接。并发时数据库约束而非预先查询负责最终裁决。升级旧数据库时，迁移保留原有退款，并补充原因字段及商户范围内的幂等键约束。`SeedDemoOrders` 仅对匹配原种子 ID、商户、金额和渠道的旧 USD 记录及关联退款更正币种为 CNY，不改变金额或退款身份；正常启动不删除退款历史。`reset-demo` 是单独的破坏性命令。

## 创建退款流程

1. 将 Bearer 令牌解析为服务端身份，并要求商户写权限。
2. 只解码一个 JSON 对象（请求体上限 1 MiB，拒绝未知字段），去空白并校验原因，同时校验 `Idempotency-Key`。拒绝请求级 `X-Demo-Outcome` 结果覆盖。
3. 同时按订单 `id` 与商户 ID 读取订单。先查同键重放：订单、金额、标准化原因均相同则返回原记录；请求内容改变则返回 `409`。
4. 拒绝已有退款的订单、未支付订单或未配置的渠道；金额、币种和渠道从原订单复制。
5. 生成退款 ID 和渠道请求 ID。调用渠道前先提交 `PROCESSING` 预占记录。若插入与另一请求竞争，重新按键/订单读取并映射为重放或 `409`。
6. 只有成功预占的请求调用一次 `Provider.SubmitRefund`。保存可识别的返回状态；渠道报错或状态无法识别时保存 `UNKNOWN`，保留请求 ID 供后续查证，最后返回持久化退款。

跨外部渠道的资金操作无法与 SQLite 预占合并成同一事务。预占后、提交前崩溃可能留下 `PROCESSING`；渠道受理后、返回前崩溃可能留下 `PROCESSING` 或 `UNKNOWN`。两者均可通过原请求 ID 查询恢复；不能盲目再次提交。

## 状态流转与查证

```text
PROCESSING -> SUCCEEDED | FAILED | UNKNOWN
UNKNOWN    -> SUCCEEDED | FAILED | UNKNOWN
SUCCEEDED、FAILED 为终态
```

`POST /api/refunds/{refundID}/reconcile`（同时兼容 `/recover`）按商户隔离且要求写权限。只有 `PROCESSING` 或 `UNKNOWN` 可调用。接口读取原渠道与请求 ID，只调用 `Provider.QueryRefund`。查询失败仍保持 `UNKNOWN`。SQL 更新带状态条件：终态不可变，乱序查询也不能让 `UNKNOWN` 退回 `PROCESSING`。

`mock-unknown` 在首次提交时先保存渠道成功记录，再模拟响应丢失。记录已持久化到 SQLite，因此重启后新建的处理器和渠道实例仍可查询原受理结果。`mock-fail` 保存明确失败结果，不能将其显示为超时导致的待确认状态。

演示结果只由初始化订单的渠道模式决定。`mock-success` 返回成功；`mock-fail` 保存明确失败原因并写回业务退款；`mock-unknown` 先保存渠道成功记录，再模拟响应丢失，业务侧暂记 `UNKNOWN`。请求级结果覆盖一律拒绝；同键重放沿用首次持久化结果。

## HTTP 与安全边界

已定义的 `/api` 接口需要 `Authorization: Bearer <token>`，`/healthz` 不需要。令牌到商户/角色的映射由服务端配置。查询按商户过滤；无权访问的资源返回 `404`，只读写入返回 `403`。

读取接口包括 `GET /api/orders`、`GET /api/orders/{orderID}`、`GET /api/orders/{orderID}/refunds` 和 `GET /api/refunds/{refundID}`。创建接口为 `POST /api/orders/{orderID}/refunds`，查证接口为 `POST /api/refunds/{refundID}/reconcile`。错误响应包含 `code` 与 `message`；无效请求为 `400`、冲突为 `409`、非预期存储或渠道配置错误为 `500`。

演示环境使用静态令牌，CORS 仅允许 `http://localhost:5173`。这不是生产身份认证或面向公网的 CORS 策略。生产部署需要受管理的身份体系、密钥管理、具备请求 ID 幂等能力的真实渠道适配器，以及对长期 `PROCESSING`/`UNKNOWN` 记录的告警。

前端免密选择用户只将身份键保存到 `refunddesk_identity` cookie（`Path=/; SameSite=Lax`），不保存 Bearer 令牌。切换用户先清除 cookie、弹窗和当前商户的内存数据，再回到同一选择页；重新选择后发出对应的 Bearer 请求。cookie 是演示界面的偏好记录，不是后端认证凭据，不能直接授予角色或商户权限；正式部署必须替换静态令牌和免密入口。

前端 `/orders/{orderID}` 为可直达的详情路由，不从列表缓存展示订单。进入、刷新或切换用户后均使用当前身份令牌调用 `GET /api/orders/{orderID}`；服务端按 `id` 与 `merchant_id` 同时查询，跨商户请求返回 `404`。页面只显示统一的不存在或无权访问提示，不把 URL 中的订单 ID 当作权限依据。自动化测试覆盖商户 B 访问商户 A 的详情 URL。

## 验证与可观测性

`backend/internal/refunds/http_test.go` 覆盖全额退款、同键重放与冲突、十个不同键并发请求、未支付与只读拒绝、商户隔离、不确定结果查证、终态保护以及数据库重开后的恢复。在 `backend` 目录运行 `go test ./...`；测试使用临时数据库，不重置 `backend/refunddesk.db`。

演示渠道日志包含渠道、请求 ID、提交次数和受理记录数。`go run ./cmd/provider-stats` 可只读检查这些计数。

## 生产化待决事项

- 明确真实渠道的超时、重试与查询语义；保留请求 ID 幂等，不把超时等同于拒绝。
- 为长期停留在 `PROCESSING` 或 `UNKNOWN` 的预占记录设置监控与人工升级路径。
- 在处理真实资金前替换演示令牌映射和本地 SQLite 部署假设。
