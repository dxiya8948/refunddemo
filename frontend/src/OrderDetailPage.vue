<script setup lang="ts">
import type { Order } from './order'
import { formatMoney, orderStatusLabel, paymentChannelStatusLabel, paymentStatus, refundMessage, refundStatusLabel } from './order'

defineProps<{ orderId: string; order: Order | null; loading: boolean; error: string; actionError: string; canReconcile: boolean; reconciling: boolean }>()
defineEmits<{ back: []; reconcile: [order: Order] }>()
</script>

<template>
  <section data-testid="order-detail-page" class="detail-page" aria-labelledby="order-page-title">
    <a class="detail-back" href="/" @click.prevent="$emit('back')">← 返回订单列表</a>
    <div class="detail-page-heading">
      <p class="eyebrow">订单详情</p>
      <h1 id="order-page-title">订单 {{ orderId }}</h1>
      <p class="subheading">订单和退款信息仅按当前商户身份读取。</p>
    </div>
    <div v-if="loading" class="detail-page-card dialog-feedback" role="status">正在加载订单详情…</div>
    <div v-else-if="error" class="detail-page-card detail-page-error">
      <h2>无法查看此订单</h2>
      <p role="alert">{{ error }}</p>
      <p class="muted">请确认订单编号及当前用户的数据范围。</p>
    </div>
    <div v-else-if="order" class="detail-page-card">
      <dl class="detail-grid">
        <div><dt>商户</dt><dd>{{ order.merchant_id }}</dd></div>
        <div><dt>订单状态</dt><dd><span class="status-pill" :class="`order-${order.status.toLowerCase()}`">{{ orderStatusLabel(order.status) }}</span></dd></div>
        <div><dt>支付状态</dt><dd><span class="status-pill" :class="`payment-${paymentStatus(order).toLowerCase()}`">{{ orderStatusLabel(paymentStatus(order)) }}</span></dd></div>
        <div><dt>订单金额</dt><dd class="amount">{{ formatMoney(order.amount_cents, order.currency) }}</dd></div>
        <div><dt>支付渠道</dt><dd>{{ order.provider_channel }}</dd></div>
        <div v-if="order.payment_channel_status"><dt>支付渠道状态</dt><dd><span :data-testid="`detail-payment-channel-status-${order.id}`" class="status-pill" :class="`channel-${order.payment_channel_status.toLowerCase()}`">{{ paymentChannelStatusLabel(order.payment_channel_status) }}</span></dd></div>
      </dl>
      <div class="detail-refund">
        <h2>退款</h2>
        <p v-if="!order.refund_id" class="muted">尚未发起退款。</p>
        <dl v-else class="detail-grid">
          <div><dt>退款状态</dt><dd><span class="status-pill" :class="`refund-${order.refund_status?.toLowerCase()}`">{{ refundStatusLabel(order.refund_status || '') }}</span></dd></div>
          <div><dt>最近更新</dt><dd>{{ order.refund_updated_at ? new Date(order.refund_updated_at).toLocaleString('zh-CN') : '—' }}</dd></div>
          <div v-if="order.provider_request_id" class="detail-wide"><dt>渠道请求号</dt><dd>{{ order.provider_request_id }}</dd></div>
          <div class="detail-wide"><dt>退款原因</dt><dd>{{ order.refund_reason || '—' }}</dd></div>
          <div v-if="order.refund_message" class="detail-wide"><dt>处理结果</dt><dd>{{ refundMessage(order.refund_status, order.refund_message) }}</dd></div>
        </dl>
        <div v-if="canReconcile && (order.refund_status === 'UNKNOWN' || order.refund_status === 'PROCESSING')" class="detail-refund-actions">
          <button :data-testid="`detail-reconcile-${order.id}`" class="primary-button compact" type="button" :disabled="reconciling" @click="$emit('reconcile', order)">
            {{ reconciling ? '正在查询渠道结果…' : '查询处理结果' }}
          </button>
        </div>
        <p v-if="actionError" class="alert error detail-action-error" role="alert">{{ actionError }}</p>
      </div>
    </div>
  </section>
</template>
