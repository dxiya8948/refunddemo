<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import OrderDetailPage from './OrderDetailPage.vue'
import type { Order, RefundStatus } from './order'
import { formatMoney, orderStatusLabel, paymentStatus, refundMessage, refundStatusLabel } from './order'
import { orderPath, useAppRoute } from './router'

type Refund = {
  id: string
  order_id: string
  amount_cents: number
  currency: string
  status: RefundStatus
  reason: string
  message: string
  provider_channel: string
  provider_request_id: string
  failure_reason?: string
  updated_at?: string
}

const identities = {
  'merchant-a': { label: '商户 A', token: 'demo-a-write', merchant: 'merchant-a', readOnly: false },
  'merchant-b': { label: '商户 B', token: 'demo-b-write', merchant: 'merchant-b', readOnly: false },
  readonly: { label: '商户 A · 只读', token: 'demo-a-read', merchant: 'merchant-a', readOnly: true },
} as const

type IdentityKey = keyof typeof identities
const cookieName = 'refunddesk_identity'

function savedIdentity(): IdentityKey | null {
  const value = document.cookie.split('; ').find((item) => item.startsWith(`${cookieName}=`))?.slice(cookieName.length + 1)
  return value && Object.prototype.hasOwnProperty.call(identities, value) ? value as IdentityKey : null
}

const initialIdentity = savedIdentity()
const loggedIn = ref(initialIdentity !== null)
const identityKey = ref<IdentityKey>(initialIdentity || 'merchant-a')
const switchingFrom = ref<IdentityKey | null>(null)
const identity = computed(() => identities[identityKey.value])
const { route, navigate } = useAppRoute()
const orders = ref<Order[]>([])
const loading = ref(false)
const busyOrder = ref('')
const busyRefund = ref('')
const refundDialogOrder = ref<Order | null>(null)
const refundStep = ref<'reason' | 'confirm' | 'submitting' | 'result'>('reason')
const refundResult = ref<Refund | null>(null)
const refundError = ref('')
const reasonDraft = ref('')
const errorMessage = ref('')
const notice = ref('')
let noticeTimer: ReturnType<typeof setTimeout> | undefined

function clearNotice() {
  if (noticeTimer) clearTimeout(noticeTimer)
  noticeTimer = undefined
  notice.value = ''
}

function showNotice(message: string) {
  clearNotice()
  notice.value = message
  noticeTimer = setTimeout(() => { notice.value = ''; noticeTimer = undefined }, 2000)
}
const detailOrder = ref<Order | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const refundReasonInput = ref<HTMLInputElement | null>(null)
const refundDialogElement = ref<HTMLElement | null>(null)
let refundTrigger: HTMLElement | null = null
let detailRequestVersion = 0
let orderLoadVersion = 0
let sessionVersion = 0

function defaultOutcomeHint(channel: string) {
  if (channel === 'mock-fail') return '原渠道默认返回失败。'
  if (channel === 'mock-unknown') return '原渠道默认返回待确认，查证后可确认成功。'
  return '原渠道默认返回成功。'
}

function apiErrorMessage(code: string | undefined, status: number) {
  switch (code) {
    case 'unauthorized': return '身份验证失败，请检查访问凭证。'
    case 'forbidden': return '当前身份为只读，无法执行此操作。'
    case 'invalid_request': return '请求参数无效，请检查输入后重试。'
    case 'idempotency_conflict': return '该幂等键已用于其他退款请求。'
    case 'refund_exists': return '此订单已有退款记录，请查询原退款。'
    case 'order_not_refundable': return '只有已支付订单可以退款。'
    case 'not_recoverable': return '当前退款状态无需查证。'
    case 'provider_unavailable': return '支付渠道暂不可用。'
    case 'not_found': return '订单不存在或无权访问。'
    default: return `请求失败（HTTP ${status}），请稍后重试。`
  }
}

async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${identity.value.token}`,
      'Content-Type': 'application/json',
      ...init.headers,
    },
  })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(apiErrorMessage(payload.code, response.status))
  return payload as T
}

async function loadOrders(preserveError = false) {
  const version = ++orderLoadVersion
  const requestedIdentity = identityKey.value
  const session = sessionVersion
  loading.value = true
  if (!preserveError) errorMessage.value = ''
  try {
    const result = await api<Order[]>('/orders')
    if (version === orderLoadVersion && session === sessionVersion && loggedIn.value && requestedIdentity === identityKey.value) orders.value = result
  } catch (error) {
    if (version === orderLoadVersion && session === sessionVersion && loggedIn.value && requestedIdentity === identityKey.value) {
      if (!preserveError) errorMessage.value = error instanceof Error ? error.message : '无法加载订单。'
    }
  } finally {
    if (version === orderLoadVersion && session === sessionVersion && loggedIn.value && requestedIdentity === identityKey.value) loading.value = false
  }
}

function applyRefund(refund: Refund) {
  const update = (order: Order | null) => {
    if (!order || order.id !== refund.order_id) return
    order.refund_id = refund.id
    order.refund_status = refund.status
    order.refund_reason = refund.reason
    order.refund_message = refund.message
    order.refund_updated_at = refund.updated_at
    if (refund.provider_request_id) order.provider_request_id = refund.provider_request_id
  }
  update(orders.value.find((item) => item.id === refund.order_id) || null)
  update(detailOrder.value)
  if (refundDialogOrder.value?.id === refund.order_id) refundResult.value = refund
}

function openOrderDetails(orderId: string, event: MouseEvent) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  navigate(orderPath(orderId))
}

async function loadOrderDetail(orderId: string) {
  const version = ++detailRequestVersion
  const requestedIdentity = identityKey.value
  const session = sessionVersion
  detailOrder.value = null
  detailError.value = ''
  detailLoading.value = true
  try {
    const result = await api<Order>(`/orders/${encodeURIComponent(orderId)}`)
    if (result.merchant_id !== identity.value.merchant) throw new Error('订单不存在或无权访问。')
    if (version === detailRequestVersion && session === sessionVersion && requestedIdentity === identityKey.value) detailOrder.value = result
  } catch (error) {
    if (version === detailRequestVersion && session === sessionVersion && requestedIdentity === identityKey.value) {
      detailError.value = error instanceof Error ? error.message : '无法加载订单详情。'
    }
  } finally {
    if (version === detailRequestVersion && session === sessionVersion && requestedIdentity === identityKey.value) detailLoading.value = false
  }
}

async function beginRefund(order: Order, event: MouseEvent) {
  if (identity.value.readOnly || order.status !== 'PAID' || order.refund_id) return
  refundTrigger = event.currentTarget as HTMLElement
  refundDialogOrder.value = order
  refundStep.value = 'reason'
  refundResult.value = null
  refundError.value = ''
  reasonDraft.value = ''
  errorMessage.value = ''
  clearNotice()
  await nextTick()
  refundReasonInput.value?.focus()
}

function closeRefundDialog(restoreFocus = true, force = false) {
  if (refundStep.value === 'submitting' && !force) return
  refundDialogOrder.value = null
  refundStep.value = 'reason'
  refundResult.value = null
  refundError.value = ''
  reasonDraft.value = ''
  const trigger = refundTrigger
  refundTrigger = null
  if (restoreFocus) nextTick(() => { if (trigger?.isConnected) trigger.focus() })
}

function continueRefund() {
  if (!reasonDraft.value.trim()) return
  refundStep.value = 'confirm'
}

function onRefundKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeRefundDialog()
  } else if (event.key === 'Tab') {
    const controls = refundDialogElement.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')
    if (!controls?.length) return
    const first = controls[0]
    const last = controls[controls.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }
}

async function refundOrder() {
  const order = refundDialogOrder.value
  if (!order || refundStep.value !== 'confirm') return
  const requestedIdentity = identityKey.value
  const session = sessionVersion
  const reason = reasonDraft.value.trim()
  if (!reason) return
  busyOrder.value = order.id
  refundStep.value = 'submitting'
  refundError.value = ''
  errorMessage.value = ''
  clearNotice()
  const minimumLoading = new Promise<void>((resolve) => setTimeout(resolve, 1000))
  try {
    const idempotencyKey = crypto.randomUUID()
    const refund = await api<Refund>(`/orders/${encodeURIComponent(order.id)}/refunds`, {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      body: JSON.stringify({ reason }),
    })
    await minimumLoading
    if (session !== sessionVersion || requestedIdentity !== identityKey.value || refundDialogOrder.value?.id !== order.id) return
    applyRefund(refund)
    refundStep.value = 'result'
    showNotice(refundMessage(refund.status, refund.message) || `订单 ${order.id} 的退款请求已记录。`)
  } catch (error) {
    await minimumLoading
    if (session === sessionVersion && requestedIdentity === identityKey.value && refundDialogOrder.value?.id === order.id) {
      refundError.value = error instanceof Error ? error.message : '暂时无法确认退款结果。'
      errorMessage.value = refundError.value
      await loadOrders(true)
      if (session === sessionVersion && requestedIdentity === identityKey.value && refundDialogOrder.value?.id === order.id) refundStep.value = 'result'
    }
  } finally {
    if (session === sessionVersion && requestedIdentity === identityKey.value) busyOrder.value = ''
  }
}

async function queryRefund(order: Order) {
  if (!order.refund_id) return
  if (!identity.value.readOnly && (order.refund_status === 'UNKNOWN' || order.refund_status === 'PROCESSING')) {
    await recoverRefund(order)
    return
  }
  const requestedIdentity = identityKey.value
  const session = sessionVersion
  busyRefund.value = order.refund_id
  errorMessage.value = ''
  try {
    const refund = await api<Refund>(`/refunds/${encodeURIComponent(order.refund_id)}`)
    if (session !== sessionVersion || requestedIdentity !== identityKey.value) return
    applyRefund(refund)
    showNotice(`订单 ${order.id} 的状态已更新。`)
  } catch (error) {
    if (session === sessionVersion && requestedIdentity === identityKey.value) errorMessage.value = error instanceof Error ? error.message : '状态查询失败。'
  } finally {
    if (session === sessionVersion && requestedIdentity === identityKey.value) busyRefund.value = ''
  }
}

async function recoverRefund(order: Order) {
  if (!order.refund_id) return
  const requestedIdentity = identityKey.value
  const session = sessionVersion
  busyRefund.value = order.refund_id
  errorMessage.value = ''
  try {
    const refund = await api<Refund>(`/refunds/${encodeURIComponent(order.refund_id)}/reconcile`, { method: 'POST' })
    if (session !== sessionVersion || requestedIdentity !== identityKey.value) return
    applyRefund(refund)
    showNotice(`订单 ${order.id} 的渠道结果已查证。`)
  } catch (error) {
    if (session === sessionVersion && requestedIdentity === identityKey.value) errorMessage.value = error instanceof Error ? error.message : '查证失败。'
  } finally {
    if (session === sessionVersion && requestedIdentity === identityKey.value) busyRefund.value = ''
  }
}

function loadCurrentRoute() {
  if (!loggedIn.value) return
  if (route.value.kind === 'order') loadOrderDetail(route.value.orderId)
  if (route.value.kind === 'orders') loadOrders()
}

watch(route, () => {
  detailRequestVersion++
  detailOrder.value = null
  detailError.value = ''
  detailLoading.value = false
  orderLoadVersion++
  orders.value = []
  loading.value = false
  errorMessage.value = ''
  closeRefundDialog(false, true)
  loadCurrentRoute()
})

function clearSession() {
  sessionVersion++
  detailRequestVersion++
  detailOrder.value = null
  detailError.value = ''
  detailLoading.value = false
  closeRefundDialog(false, true)
  orderLoadVersion++
  orders.value = []
  busyOrder.value = ''
  busyRefund.value = ''
  reasonDraft.value = ''
  errorMessage.value = ''
  clearNotice()
  loading.value = false
}

function chooseIdentity(key: IdentityKey) {
  clearSession()
  identityKey.value = key
  switchingFrom.value = null
  loggedIn.value = true
  document.cookie = `${cookieName}=${key}; Path=/; SameSite=Lax`
  loadCurrentRoute()
}

function switchIdentity() {
  switchingFrom.value = identityKey.value
  clearSession()
  loggedIn.value = false
  document.cookie = `${cookieName}=; Max-Age=0; Path=/; SameSite=Lax`
}

onMounted(loadCurrentRoute)
onUnmounted(clearNotice)
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <a class="brand" href="/" aria-label="退款工作台首页" @click.prevent="navigate('/')">
        <span class="brand-mark" aria-hidden="true">退</span>
        <span>退款工作台</span>
      </a>
      <div v-if="loggedIn" class="account-control">
        <span class="user-avatar" :class="`avatar-${identityKey}`" aria-hidden="true">{{ identityKey === 'merchant-b' ? 'B' : 'A' }}</span>
        <span class="account-name">{{ identity.label }}</span>
        <span class="identity-badge" :class="{ readonly: identity.readOnly }">
          {{ identity.readOnly ? '只读' : identity.merchant }}
        </span>
        <button data-testid="switch-user" class="secondary-button" type="button" @click="switchIdentity">切换用户</button>
      </div>
    </header>

    <main v-if="!loggedIn" class="login-main">
      <section class="login-panel" aria-labelledby="login-title">
        <p class="eyebrow">支付运营</p>
        <h1 id="login-title">选择用户</h1>
        <p class="subheading">选择演示身份进入退款工作台，无需输入密码。</p>
        <div class="user-list">
          <button v-for="(user, key) in identities" :key="key" class="user-option" type="button" :data-testid="`select-${key}`" @click="chooseIdentity(key)">
            <span class="user-avatar" :class="`avatar-${key}`" :data-testid="`avatar-${key}`" aria-hidden="true">{{ key === 'merchant-b' ? 'B' : 'A' }}</span>
            <span class="user-option-copy"><span class="user-option-name"><strong>{{ user.label }}</strong><span v-if="switchingFrom === key" data-testid="current-user-tag" class="user-current-tag">当前用户</span></span><small>{{ user.readOnly ? '仅可查看订单和退款' : '可查看订单并发起退款' }}</small></span>
            <span class="user-option-arrow" aria-hidden="true">→</span>
          </button>
        </div>
        <p class="login-hint">仅用于本地演示。用户选择不代替后端身份验证。</p>
      </section>
    </main>

    <main v-else-if="route.kind === 'order'" class="detail-main">
      <OrderDetailPage :order-id="route.orderId" :order="detailOrder" :loading="detailLoading" :error="detailError" :action-error="errorMessage" :can-reconcile="!identity.readOnly" :reconciling="!!detailOrder?.refund_id && busyRefund === detailOrder.refund_id" @back="navigate('/')" @reconcile="recoverRefund" />
    </main>

    <main v-else-if="route.kind === 'not-found'" class="detail-main">
      <section class="detail-page detail-page-error">
        <h1>页面不存在</h1>
        <a class="detail-back" href="/" @click.prevent="navigate('/')">返回订单列表</a>
      </section>
    </main>

    <main v-else>
      <section class="page-heading">
        <div>
          <p class="eyebrow">支付运营</p>
          <h1>订单与退款</h1>
          <p class="subheading">查看已支付订单，跟踪渠道退款结果。</p>
        </div>
        <button class="secondary-button" type="button" :disabled="loading" @click="loadOrders()">
          <span aria-hidden="true">↻</span> 刷新
        </button>
      </section>

      <div v-if="errorMessage" class="alert error" role="alert">{{ errorMessage }}</div>

      <section class="table-section" aria-label="订单列表">
        <div class="table-toolbar">
          <div>
            <h2>最近订单</h2>
            <span class="table-count">{{ loading ? '正在加载订单…' : `共 ${orders.length} 笔订单` }}</span>
          </div>
          <span class="scope-label"><span class="scope-dot"></span>{{ identity.merchant }} 数据范围</span>
        </div>

        <div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>订单</th>
                <th>订单状态</th>
                <th>支付状态</th>
                <th>金额</th>
                <th>支付渠道</th>
                <th>退款状态</th>
                <th class="actions-heading">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="order in orders" :key="order.id">
                <td>
                  <a :data-testid="`details-${order.id}`" class="order-id order-detail-button" :href="orderPath(order.id)" @click="openOrderDetails(order.id, $event)">
                    订单 {{ order.id }}
                  </a>
                  <small>{{ order.merchant_id }}</small>
                </td>
                <td><span :data-testid="`order-status-${order.id}`" class="status-pill" :class="`order-${order.status.toLowerCase()}`">{{ orderStatusLabel(order.status) }}</span></td>
                <td><span :data-testid="`payment-status-${order.id}`" class="status-pill" :class="`payment-${paymentStatus(order).toLowerCase()}`">{{ orderStatusLabel(paymentStatus(order)) }}</span></td>
                <td class="amount">{{ formatMoney(order.amount_cents, order.currency) }}</td>
                <td><span class="channel-dot"></span>{{ order.provider_channel }}</td>
                <td>
                  <span v-if="order.refund_status" :data-testid="`refund-status-${order.id}`" class="status-pill" :class="`refund-${order.refund_status.toLowerCase()}`">
                    {{ refundStatusLabel(order.refund_status) }}
                  </span>
                  <span v-else class="status-pill refund-none">未发起</span>
                  <small v-if="order.refund_message" class="refund-message">
                    {{ order.refund_reason ? `${order.refund_reason} · ` : '' }}{{ refundMessage(order.refund_status, order.refund_message) }}
                  </small>
                  <small v-if="order.refund_updated_at" :data-testid="`refund-time-${order.id}`" :title="order.refund_updated_at" class="refund-message">
                    更新于 {{ new Date(order.refund_updated_at).toLocaleString('zh-CN') }}
                  </small>
                </td>
                <td class="actions-cell">
                  <a :data-testid="`action-details-${order.id}`" class="text-button" :href="orderPath(order.id)" @click="openOrderDetails(order.id, $event)">详情</a>
                  <template v-if="order.refund_id">
                    <button :data-testid="`query-status-${order.id}`" :class="!identity.readOnly && (order.refund_status === 'UNKNOWN' || order.refund_status === 'PROCESSING') ? 'primary-button compact' : 'text-button'" type="button" :disabled="busyRefund === order.refund_id" @click="queryRefund(order)">
                      {{ order.refund_status === 'UNKNOWN' || order.refund_status === 'PROCESSING'
                        ? (identity.readOnly ? (busyRefund === order.refund_id ? '正在查询状态…' : '查询状态') : (busyRefund === order.refund_id ? '正在查询渠道结果…' : '查询处理结果'))
                        : (busyRefund === order.refund_id ? '正在查询状态…' : '查询状态') }}
                    </button>
                  </template>
                  <button
                    v-else-if="!identity.readOnly"
                    :data-testid="`refund-${order.id}`"
                    class="primary-button"
                    type="button"
                    :disabled="busyOrder === order.id || order.status !== 'PAID'"
                    @click="beginRefund(order, $event)"
                  >
                    退款
                  </button>
                  <span v-else class="muted">仅可查看</span>
                </td>
              </tr>
              <tr v-if="loading && orders.length === 0">
                <td colspan="7" class="empty-state" role="status">正在加载订单…</td>
              </tr>
              <tr v-else-if="!errorMessage && orders.length === 0">
                <td colspan="7" class="empty-state">此商户暂无订单。</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="table-footnote">退款沿用原支付渠道；结果待确认时可查询原退款请求。</div>
      </section>
    </main>
    <Transition name="toast"><div v-if="notice" data-testid="notice-toast" class="notice-toast" role="status" aria-live="polite">{{ notice }}</div></Transition>
    <div v-if="refundDialogOrder" class="modal-backdrop" @click.self="closeRefundDialog()">
      <div ref="refundDialogElement" data-testid="refund-dialog" class="order-dialog refund-dialog" role="dialog" aria-modal="true" aria-labelledby="refund-dialog-title" @keydown="onRefundKeydown">
        <div v-if="refundStep !== 'result' || (refundResult?.status !== 'SUCCEEDED' && refundResult?.status !== 'FAILED') || refundError" class="dialog-header">
          <div>
            <p class="eyebrow">全额退款</p>
            <h2 id="refund-dialog-title">退款订单 {{ refundDialogOrder.id }}</h2>
          </div>
          <button class="dialog-close" type="button" aria-label="关闭退款弹窗" :disabled="refundStep === 'submitting'" @click="closeRefundDialog()">×</button>
        </div>

        <Transition name="refund-view">
        <div v-if="refundStep === 'result' && refundResult?.status === 'SUCCEEDED' && !refundError" data-testid="refund-success" class="refund-outcome refund-outcome-success" role="status" aria-live="polite">
          <span class="refund-outcome-icon" aria-hidden="true">✓</span>
          <h2 id="refund-dialog-title">退款成功</h2>
          <p>{{ refundMessage(refundResult.status, refundResult.message) || '全额退款已完成。' }}</p>
          <button class="primary-button" type="button" @click="closeRefundDialog()">关闭</button>
        </div>
        <div v-else-if="refundStep === 'result' && refundResult?.status === 'FAILED' && !refundError" data-testid="refund-failure" class="refund-outcome refund-outcome-failure" role="alert">
          <span class="refund-outcome-icon" aria-hidden="true">×</span>
          <h2 id="refund-dialog-title">退款失败</h2>
          <p>{{ refundMessage(refundResult.status, refundResult.failure_reason || refundResult.message) }}</p>
          <p class="refund-result-guidance">本订单已有退款记录，无法再次发起；请先联系支持人员核实处理。</p>
          <button class="primary-button" type="button" @click="closeRefundDialog()">关闭</button>
        </div>
        <div v-else class="refund-dialog-body">
          <div class="refund-amount">
            <span>全额退款金额</span>
            <strong>{{ formatMoney(refundDialogOrder.amount_cents, refundDialogOrder.currency) }}</strong>
          </div>
          <p class="refund-explanation">将通过原支付渠道 {{ refundDialogOrder.provider_channel }} 退还订单 {{ refundDialogOrder.id }} 的全部已支付金额。发起后不可为此订单再次创建退款。</p>

          <div class="refund-stage-slot"><Transition name="refund-step">
          <div v-if="refundStep === 'reason'" class="refund-stage">
            <label :for="`refund-reason-${refundDialogOrder.id}`">退款原因</label>
            <input ref="refundReasonInput" :id="`refund-reason-${refundDialogOrder.id}`" v-model="reasonDraft" :aria-label="`订单 ${refundDialogOrder.id} 的退款原因`" maxlength="500" placeholder="请输入退款原因" type="text" @keyup.enter="continueRefund">
            <p class="demo-outcome-help">{{ defaultOutcomeHint(refundDialogOrder.provider_channel) }}</p>
            <div class="dialog-actions">
              <button class="secondary-button" type="button" @click="closeRefundDialog()">取消</button>
              <button data-testid="refund-continue" class="primary-button" type="button" :disabled="!reasonDraft.trim()" @click="continueRefund">核对退款</button>
            </div>
          </div>

          <div v-else-if="refundStep === 'confirm'" class="refund-stage">
            <h3>确认全额退款</h3>
            <p>确认向订单 <strong>{{ refundDialogOrder.id }}</strong> 退款 <strong>{{ formatMoney(refundDialogOrder.amount_cents, refundDialogOrder.currency) }}</strong>？</p>
            <p class="refund-review-reason">退款原因：{{ reasonDraft.trim() }}</p>
            <p class="refund-review-reason">渠道规则：{{ defaultOutcomeHint(refundDialogOrder.provider_channel) }}</p>
            <div class="dialog-actions">
              <button class="secondary-button" type="button" @click="refundStep = 'reason'">返回修改</button>
              <button data-testid="refund-confirm" class="primary-button" type="button" @click="refundOrder">确认全额退款</button>
            </div>
          </div>

          <div v-else-if="refundStep === 'submitting'" class="refund-stage" role="status">
            <p class="refund-progress"><span data-testid="refund-spinner" class="spinner" aria-hidden="true"></span>正在提交退款并等待渠道结果…</p>
          </div>

          <div v-else class="refund-stage">
            <div v-if="refundError" class="alert error" role="alert">{{ refundError }} 结果可能尚未确认，请先查询此订单的退款记录，不要直接重试。</div>
            <p v-else-if="refundResult" class="refund-result-text">{{ refundMessage(refundResult.status, refundResult.message) }}</p>
            <div class="dialog-actions">
              <button v-if="refundDialogOrder.refund_id && (refundResult?.status === 'UNKNOWN' || refundResult?.status === 'PROCESSING')" class="secondary-button" type="button" :disabled="busyRefund === refundDialogOrder.refund_id" @click="recoverRefund(refundDialogOrder)">
                {{ busyRefund === refundDialogOrder.refund_id ? '正在查询渠道结果…' : '查询处理结果' }}
              </button>
              <button class="primary-button" type="button" @click="closeRefundDialog()">关闭</button>
            </div>
          </div>
          </Transition></div>

          <ol data-testid="refund-chain" class="refund-chain" aria-label="退款处理链路">
            <li class="complete"><span class="chain-marker">1</span><span>订单已支付</span></li>
            <li :class="{ complete: !!refundResult, active: refundStep === 'submitting', uncertain: !!refundError }">
              <span class="chain-marker">2</span><span>{{ refundStep === 'submitting' ? '正在提交退款' : refundError ? '请求待确认' : '发起退款' }}</span>
            </li>
            <li :class="{ complete: refundResult?.status === 'SUCCEEDED' || refundResult?.status === 'FAILED', active: refundResult?.status === 'PROCESSING' || !!(refundDialogOrder.refund_id && busyRefund === refundDialogOrder.refund_id), uncertain: refundResult?.status === 'UNKNOWN' || !!refundError }">
              <span class="chain-marker">3</span><span>渠道处理</span>
              <span v-if="refundDialogOrder.refund_id && busyRefund === refundDialogOrder.refund_id" class="spinner" aria-hidden="true"></span>
            </li>
            <li :class="{ complete: refundResult?.status === 'SUCCEEDED', failed: refundResult?.status === 'FAILED', uncertain: refundResult?.status === 'UNKNOWN' || !!refundError }">
              <span class="chain-marker">4</span><span>{{ refundResult ? refundStatusLabel(refundResult.status) : (refundError ? '结果待确认' : '等待结果') }}</span>
            </li>
          </ol>
        </div>
        </Transition>
      </div>
    </div>
    <footer>退款工作台 <span>·</span> 演示环境</footer>
  </div>
</template>
