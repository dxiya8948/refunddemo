export type RefundStatus = 'PROCESSING' | 'UNKNOWN' | 'SUCCEEDED' | 'FAILED' | ''

export type Order = {
  id: string
  merchant_id: string
  amount_cents: number
  currency: string
  status: string
  provider_channel: string
  refund_id?: string
  refund_status?: RefundStatus
  refund_reason?: string
  refund_message?: string
  refund_updated_at?: string
  provider_request_id?: string
}

export function orderStatusLabel(status: string) {
  return status === 'PAID' ? '已支付' : status === 'UNPAID' ? '未支付' : '未知'
}

export function paymentStatus(order: Order) {
  return order.status === 'PAID' || order.status === 'UNPAID' ? order.status : 'UNKNOWN'
}

export function refundStatusLabel(status: RefundStatus | string) {
  switch (status) {
    case 'PROCESSING': return '处理中'
    case 'UNKNOWN': return '结果待确认'
    case 'SUCCEEDED': return '退款成功'
    case 'FAILED': return '退款失败'
    default: return '未发起'
  }
}

export function refundMessage(status: RefundStatus | undefined, message?: string) {
  if (!message) return ''
  if (status === 'SUCCEEDED') return '退款已成功完成。'
  if (status === 'PROCESSING') return '退款正在处理中。'
  if (status === 'UNKNOWN') return '渠道结果尚未确认，请查询原退款请求。'
  if (status === 'FAILED') {
    const reason = message.toLowerCase()
    if (reason.includes('insufficient funds')) return '退款失败：商户退款余额不足，请补足余额后联系支持人员。'
    if (reason.includes('invalid account')) return '退款失败：收款账户无效，请核对账户信息。'
    if (reason.includes('risk review')) return '退款失败：渠道风控拒绝，请联系支持人员核实。'
    return '退款失败：支付渠道拒绝退款，请联系支持人员核实。'
  }
  return /[\u3400-\u9fff]/u.test(message) ? message : '退款状态暂不可用。'
}

export function formatMoney(cents: number, currency: string) {
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency }).format(cents / 100)
}
