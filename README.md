# 退款工作台

这是一个基于 Go、SQLite、Vue 3 和 TypeScript 的退款运营示例，展示全额退款幂等、商户隔离，以及渠道响应不确定时的原请求查证。

## 环境要求

- Go 1.17 或更高版本（当前在 Go 1.17.3 验证）
- Node.js 22.22.2+（22.x）或 24.15.0+（24.x），以及随 Node 安装的 npm；锁文件中的部分依赖不支持 Node 25，虽然当前环境测试和构建可通过，但 `npm ci` 会给出 `EBADENGINE` 警告

Windows 上如 Go 安装在 `E:\ProgramFile\go`，请确保 `E:\ProgramFile\go\bin` 在 `PATH`。已配置后需打开新终端才能让当前会话识别 `go`；也可在当前 PowerShell 会话执行 `$env:Path += ';E:\ProgramFile\go\bin'`，再运行 `go version` 检查。

## 本地运行

首次初始化确定性的演示订单。该命令可重复运行，不会删除已有退款记录：

```powershell
cd backend
go run ./cmd/seed-demo
```

在一个终端启动 API：

```powershell
cd backend
go run ./cmd/server
```

在另一终端启动前端：

```powershell
cd frontend
npm install
npm run dev
```

打开 Vite 输出的地址。API 默认监听 `http://localhost:8080`；SQLite 数据默认存放于 `backend/refunddesk.db`。可通过 `DATABASE_PATH` 和 `ADDR` 修改这两个默认值。

## 演示身份

界面提供三个预置身份。API 在服务端根据 Bearer 令牌确定商户和角色，客户端不能通过提交商户 ID 选择数据范围。

| 身份 | Bearer 令牌 | 权限 |
| --- | --- | --- |
| 商户 A | `demo-a-write` | 查看商户 A 订单、创建退款、查证退款 |
| 商户 B | `demo-b-write` | 查看商户 B 订单、创建退款、查证退款 |
| 商户 A（只读） | `demo-a-read` | 查看商户 A 订单及退款状态 |

非演示环境需通过 `TOKEN_MERCHANT_A_WRITE`、`TOKEN_MERCHANT_B_WRITE`、`TOKEN_MERCHANT_A_READ` 提供安全令牌，替换内置默认值。

## 测试与构建

```powershell
cd backend
go test ./...
```

```powershell
cd frontend
npm test
npm run build
```

## 演示场景

注意：A-1002 初始支付状态改为 `UNKNOWN` 是本项目的新增演示要求，偏离公开验收清单中该订单初始已支付的种子数据。验证公开验收时需单独说明这一差异。

- 点击订单号进入 `/orders/{orderID}` 详情页；可复制 URL、刷新或使用浏览器回退。详情页直接请求按商户隔离的订单接口；以商户 B 打开商户 A 的详情 URL 应返回 `404` 并显示无权访问，不显示 A 的订单数据。静态部署时需将前端路由回退到 `index.html`。
- `A-1002` 初始支付状态为 `UNKNOWN`，不可退款；在列表操作栏点“同步状态”后查询模拟渠道，确认支付成功才变为 `PAID` 并允许退款；若查询仍未知，则保持不可退款。该订单退款首次返回 `UNKNOWN`；退款弹窗、列表或订单详情中的“查询处理结果”按原渠道请求号查证为 `SUCCEEDED`，退款 `UNKNOWN` 行的“刷新”则只读取本地已保存状态。`A-1001` 退款立即成功；`A-1003` 被渠道明确拒绝；`A-1004` 未支付且不支持同步，不会调用退款渠道。普通“查询状态”只用于读取退款记录，不触发渠道查证。
- 退款结果由初始化订单的原渠道决定，不由退款申请选择：`A-1001` 成功、`A-1002` 响应丢失后可查证、`A-1003` 明确失败。失败结果显示原因和不可重复发起的提示。
- 商户 B 只能看到 `B-2001`。
- 每笔订单最多创建一条退款。幂等键按商户隔离；同一键更换订单或原因返回 `409`。

创建退款时需在 HTTP 请求头中提供 `Idempotency-Key`，并提交形如 `{"reason":"customer_cancelled"}` 的 JSON。退款原因会持久化并参与幂等校验，但不会决定模拟渠道的处理结果。

订单和退款 API 的 `amount_cents` 均为整数分；演示订单使用 `CNY`，页面按人民币元展示，例如 `19900` 分为 `¥199.00`。重复初始化会将匹配原始测试订单 ID、商户、金额和渠道的旧 USD 演示记录（包括关联退款）更正为 CNY，不改变金额、状态或退款 ID；其他订单不变。若需清空演示数据，须显式运行 `go run ./cmd/reset-demo`。退款申请中的 `X-Demo-Outcome` 覆盖头会被拒绝。

模拟渠道结果与业务退款分开存储于 SQLite，因此 `A-1002` 的渠道结果在服务重启后仍可查。可在 `backend` 目录运行 `go run ./cmd/provider-stats` 查看请求 ID、提交次数和渠道受理记录数。只有显式执行 `go run ./cmd/reset-demo` 才会清除演示订单、退款和模拟渠道结果；正常启动与 `seed-demo` 均不清理退款历史。

## 验收验证

在 `backend` 目录运行 `go test ./...`，再在 `frontend` 目录运行 `npm test` 和 `npm run build`。后端测试使用临时 SQLite 数据库，不修改 `backend/refunddesk.db`。

| 验收项 | 可重复验证的测试 |
| --- | --- |
| 正常全额退款，业务与渠道记录一致 | `TestSuccessfulRefundMatchesBusinessAndProviderLedgers`；前端提交测试见 `App.test.ts` |
| 同键重放及订单/原因变更冲突 | `TestRefundIdempotencyAndPayloadConflict`、`TestRefundReasonIsRequiredAndPartOfIdempotencyPayload` |
| 不同键的十个并发请求 | `TestTenConcurrentRefundRequestsHaveNoServerErrors`：验证一条业务退款、一条渠道受理记录、一次提交且无 500 |
| 支付未知同步及退款不确定结果查证 | `TestUnknownPaymentMustSyncBeforeRefund`、`TestUnknownRefundRecoversByPersistedProviderRequest`；`App.test.ts` 覆盖 A-1002 先同步支付、再退款和查证 |
| 重启后恢复 | `TestUnknownRefundCanReconcileAfterDatabaseReopen`：重新创建数据库连接、处理器及渠道模拟器后查证 |
| 明确失败及未支付拒绝 | `TestExplicitProviderFailureAndUnpaidOrderDoNotCreateAcceptance` |
| 商户隔离与只读权限 | `TestMerchantIsolationAndReadonlyRole`、`TestOrderDetailAndReconcileContract` |
| 加载、空列表、错误、处理中、身份切换 | `App.test.ts` 覆盖各状态及旧商户迟到响应 |
| 可重复初始化且保留退款 | `TestContractSeedDataIsRepeatableAndPreservesRefunds` |

手动演示 A-1002：初始化后确认 `GET /api/orders/A-1002` 为 `UNKNOWN`；退款请求应返回 `409 order_not_refundable`。用商户 A 写令牌调用 `POST /api/orders/A-1002/sync-payment` 后，订单变为 `PAID`；随后创建退款，首次结果为 `UNKNOWN`。停服并以相同数据库重启，调用 `/api/refunds/{id}/reconcile`；确认结果为 `SUCCEEDED` 且渠道请求号不变。支付同步不重复运行初始化回滚；普通启动和重复 `seed-demo` 保留已同步状态与退款历史。自动化测试使用临时数据库，不触碰默认数据库。

架构与前端设计系统见 [DESIGN.md](DESIGN.md)，后端 SDD 规范见 [.openspec/specs/refund-backend/spec.md](.openspec/specs/refund-backend/spec.md)，技术设计见 [.openspec/changes/refund-backend/design.md](.openspec/changes/refund-backend/design.md)，开发辅助说明见 [AI_USAGE.md](AI_USAGE.md)。
