import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import App from './App.vue'
import { readFileSync } from 'node:fs'

const styles = readFileSync('src/style.css', 'utf8')

const order = {
  id: 'A-1001',
  merchant_id: 'merchant-a',
  amount_cents: 19900,
  currency: 'CNY',
  status: 'PAID',
  provider_channel: 'mock-success',
}

function jsonResponse(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))
}

beforeEach(() => { window.history.replaceState({}, '', '/'); document.cookie = 'refunddesk_identity=merchant-a; Path=/' })
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); document.cookie = 'refunddesk_identity=; Max-Age=0; Path=/'; window.history.replaceState({}, '', '/') })

describe('refund desk', () => {
  it('syncs A-1002 unknown payment before allowing its refund', async () => {
    const pendingOrder = { ...order, id: 'A-1002', status: 'UNKNOWN', provider_channel: 'mock-unknown', payment_channel_status: 'UNKNOWN' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([pendingOrder])
      if (String(input) === '/api/orders/A-1002/sync-payment' && init?.method === 'POST') {
        return jsonResponse({ ...pendingOrder, status: 'PAID', payment_channel_status: 'SUCCEEDED' })
      }
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('[data-testid="payment-status-A-1002"]').text()).toBe('未知')
    expect(wrapper.get('[data-testid="payment-channel-status-A-1002"]').text()).toContain('待确认')
    expect(wrapper.find('[data-testid="refund-A-1002"]').exists()).toBe(false)
    await wrapper.get('[data-testid="sync-payment-A-1002"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="payment-status-A-1002"]').text()).toBe('已支付')
    expect(wrapper.get('[data-testid="payment-channel-status-A-1002"]').text()).toContain('成功')
    expect(wrapper.get('[data-testid="payment-channel-status-A-1002"]').classes()).toContain('channel-succeeded')
    expect(wrapper.find('[data-testid="sync-payment-A-1002"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="refund-A-1002"]').attributes('disabled')).toBeUndefined()
    expect(fetchMock.mock.calls.some(([url, init]) => String(url) === '/api/orders/A-1002/sync-payment' && init?.method === 'POST')).toBe(true)
  })

  it('keeps A-1002 unrefundable when payment sync is still unknown', async () => {
    const pendingOrder = { ...order, id: 'A-1002', status: 'UNKNOWN', provider_channel: 'mock-unknown', payment_channel_status: 'UNKNOWN' }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([pendingOrder])
      if (String(input) === '/api/orders/A-1002/sync-payment' && init?.method === 'POST') return jsonResponse(pendingOrder)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="sync-payment-A-1002"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="payment-status-A-1002"]').text()).toBe('未知')
    expect(wrapper.get('[data-testid="payment-channel-status-A-1002"]').text()).toContain('待确认')
    expect(wrapper.get('[data-testid="sync-payment-A-1002"]').text()).toBe('同步状态')
    expect(wrapper.find('[data-testid="refund-A-1002"]').exists()).toBe(false)
    expect(wrapper.get('[role="status"]').text()).toContain('支付状态仍待确认')
  })

  it('shows the saved payment channel status on the order detail page', async () => {
    window.history.replaceState({}, '', '/orders/A-1002')
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders/A-1002') return jsonResponse({ ...order, id: 'A-1002', provider_channel: 'mock-unknown', payment_channel_status: 'SUCCEEDED' })
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('[data-testid="detail-payment-channel-status-A-1002"]').text()).toBe('成功')
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('mock-unknown')
  })

  it('marks the current user only while choosing a replacement identity', async () => {
    document.cookie = 'refunddesk_identity=; Max-Age=0; Path=/'
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse([order]))
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.find('[data-testid="current-user-tag"]').exists()).toBe(false)
    await wrapper.get('[data-testid="select-merchant-b"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    expect(wrapper.get('[data-testid="select-merchant-b"] [data-testid="current-user-tag"]').text()).toBe('当前用户')
    expect(wrapper.find('[data-testid="select-merchant-a"] [data-testid="current-user-tag"]').exists()).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-testid="select-merchant-a"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    expect(wrapper.get('[data-testid="select-merchant-a"] [data-testid="current-user-tag"]').text()).toBe('当前用户')
    expect(wrapper.find('[data-testid="select-merchant-b"] [data-testid="current-user-tag"]').exists()).toBe(false)
  })

  it('provides a detail action in the list alongside the order link', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (String(input) === '/api/orders/A-1001') return jsonResponse(order)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    const action = wrapper.get('[data-testid="action-details-A-1001"]')
    expect(action.text()).toBe('详情')
    expect(action.attributes('href')).toBe('/orders/A-1001')
    await action.trigger('click')
    await flushPromises()
    expect(window.location.pathname).toBe('/orders/A-1001')
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('¥199.00')
  })

  it('opens a dedicated order route instead of a detail dialog', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (String(input) === '/api/orders/A-1001') return jsonResponse(order)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="details-A-1001"]').trigger('click')
    await flushPromises()
    expect(window.location.pathname).toBe('/orders/A-1001')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('¥199.00')
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('尚未发起退款。')
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).not.toContain('渠道请求号')
  })

  it('denies a direct A-order URL to merchant B without showing A data', async () => {
    document.cookie = 'refunddesk_identity=merchant-b; Path=/'
    window.history.replaceState({}, '', '/orders/A-1001')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      expect(String(input)).toBe('/api/orders/A-1001')
      expect((init?.headers as Record<string, string>).Authorization).toBe('Bearer demo-b-write')
      return jsonResponse({ code: 'not_found', message: 'order not found' }, 404)
    })
    const wrapper = mount(App)
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('无法查看此订单')
    expect(wrapper.text()).not.toContain('merchant-a')
    expect(wrapper.text()).not.toContain('¥199.00')
  })

  it('tracks browser history and does not reuse details from another route', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (String(input) === '/api/orders/A-1001') return jsonResponse(order)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="details-A-1001"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('¥199.00')
    window.history.replaceState({}, '', '/')
    window.dispatchEvent(new PopStateEvent('popstate'))
    await flushPromises()
    expect(wrapper.find('[data-testid="order-detail-page"]').exists()).toBe(false)
    expect(wrapper.get('[aria-label="订单列表"]').text()).toContain('订单 A-1001')
  })

  it('rejects foreign merchant data even if a detail endpoint misbehaves', async () => {
    document.cookie = 'refunddesk_identity=merchant-b; Path=/'
    window.history.replaceState({}, '', '/orders/A-1001')
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse(order))
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('[data-testid="order-detail-page"] [role="alert"]').text()).toContain('无权访问')
    expect(wrapper.text()).not.toContain('merchant-a')
    expect(wrapper.text()).not.toContain('¥199.00')
  })

  it('shows a top toast for two seconds without shifting the order list', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([{ ...order, refund_id: 'refund-7', refund_status: 'SUCCEEDED' }])
      if (String(input) === '/api/refunds/refund-7') return jsonResponse({
        id: 'refund-7', order_id: order.id, amount_cents: order.amount_cents,
        currency: order.currency, status: 'SUCCEEDED', reason: '客户取消', message: 'Refund completed successfully.',
      })
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    vi.useFakeTimers()
    await wrapper.get('[data-testid="query-status-A-1001"]').trigger('click')
    await flushPromises()
    const toast = wrapper.get('[data-testid="notice-toast"]')
    expect(toast.text()).toContain('订单 A-1001 的状态已更新')
    expect(wrapper.find('main .alert.success').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(1999)
    expect(wrapper.find('[data-testid="notice-toast"]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(1)
    expect(wrapper.find('[data-testid="notice-toast"]').exists()).toBe(false)
  })

  it('keeps refund dialog dimensions fixed across animated stages', () => {
    expect(styles).toMatch(/\.refund-dialog\s*\{[^}]*height:\s*min\(/)
    expect(styles).toMatch(/\.refund-dialog\s*\{[^}]*overflow:\s*hidden/)
    expect(styles).toMatch(/\.refund-stage-slot\s*\{[^}]*height:/)
    expect(styles).toMatch(/\.refund-step-leave-active\s*\{[^}]*position:\s*absolute/)
  })

  it('requires selecting a user before requesting merchant orders', async () => {
    document.cookie = 'refunddesk_identity=; Max-Age=0; Path=/'
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse([order]))
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('选择用户')
    expect(fetchMock).not.toHaveBeenCalled()
    expect(wrapper.find('[aria-label="订单列表"]').exists()).toBe(false)
    const avatar = wrapper.get('[data-testid="avatar-merchant-a"]')
    expect(avatar.text()).toBe('A')
    expect(avatar.classes()).toContain('user-avatar')
    await wrapper.get('[data-testid="select-merchant-b"]').trigger('click')
    await flushPromises()
    expect(fetchMock.mock.calls[0]?.[1]?.headers).toMatchObject({ Authorization: 'Bearer demo-b-write' })
    expect(document.cookie).toContain('refunddesk_identity=merchant-b')
    expect(wrapper.text()).toContain('订单与退款')
  })

  it('ignores an invalid identity cookie and restores a valid selection', async () => {
    document.cookie = 'refunddesk_identity=unknown; Path=/'
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse([order]))
    const first = mount(App)
    await flushPromises()
    expect(first.get('h1').text()).toBe('选择用户')
    expect(fetchMock).not.toHaveBeenCalled()
    first.unmount()
    document.cookie = 'refunddesk_identity=readonly; Path=/'
    const second = mount(App)
    await flushPromises()
    expect(second.get('h1').text()).toBe('订单与退款')
    expect(fetchMock.mock.calls[0]?.[1]?.headers).toMatchObject({ Authorization: 'Bearer demo-a-read' })
  })

  it('drops an earlier order response after switching back to the same user', async () => {
    let finishOld!: (response: Response) => void
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { finishOld = resolve }))
      .mockImplementationOnce(() => jsonResponse([]))
    const wrapper = mount(App)
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    expect(document.cookie).not.toContain('refunddesk_identity=')
    await wrapper.get('[data-testid="select-merchant-a"]').trigger('click')
    await flushPromises()
    finishOld(new Response(JSON.stringify([order]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).not.toContain('订单 A-1001')
    expect(wrapper.text()).toContain('此商户暂无订单。')
  })

  it('shows the seeded channel failure without a request-level outcome selector', async () => {
    const failedOrder = { ...order, id: 'A-1003', amount_cents: 5600, provider_channel: 'mock-fail' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([failedOrder])
      if (init?.method === 'POST') return jsonResponse({
        id: 'refund-failed', order_id: failedOrder.id, amount_cents: failedOrder.amount_cents,
        currency: failedOrder.currency, status: 'FAILED', reason: '客户取消',
        failure_reason: 'channel rejected', message: 'channel rejected',
        provider_channel: 'mock-fail', provider_request_id: 'provider-failed',
      }, 201)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1003"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1003 的退款原因"]').setValue('客户取消')
    expect(wrapper.find('[data-testid="demo-outcome"]').exists()).toBe(false)
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')?.[1]?.headers).not.toHaveProperty('X-Demo-Outcome')
    expect(wrapper.get('[data-testid="refund-failure"]').text()).toContain('渠道拒绝')
    expect(wrapper.get('[data-testid="refund-failure"]').text()).toContain('无法再次发起')
    expect(wrapper.find('[data-testid="refund-chain"]').exists()).toBe(false)
    await wrapper.get('[data-testid="refund-failure"] button').trigger('click')
    expect(wrapper.get('[data-testid="refund-status-A-1003"]').text()).toBe('退款失败')
    expect(wrapper.text()).toContain('渠道拒绝')
    expect(wrapper.find('[data-testid="refund-A-1003"]').exists()).toBe(false)
  })

  it('renders Chinese labels and localizes backend status messages', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([{
        ...order, refund_id: 'refund-7', refund_status: 'UNKNOWN',
        refund_message: 'Channel processing result is pending confirmation. Query the original refund.',
      }])
      if (String(input) === '/api/orders/A-1001') return jsonResponse(order)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('订单与退款')
    expect(wrapper.get('[data-testid="order-status-A-1001"]').text()).toBe('已支付')
    expect(wrapper.get('[data-testid="refund-status-A-1001"]').text()).toBe('结果待确认')
    expect(wrapper.text()).toContain('请查询原退款请求')
    await wrapper.get('[data-testid="details-A-1001"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('订单 A-1001')
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('支付状态')
  })

  it('holds loading for one second and replaces the refund flow with success feedback', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (init?.method === 'POST') return jsonResponse({
        id: 'refund-1', order_id: order.id, amount_cents: order.amount_cents,
        currency: order.currency, status: 'SUCCEEDED', reason: 'customer_cancelled',
        message: 'Refund completed successfully.', provider_channel: 'mock-success',
        provider_request_id: 'provider-1',
      }, 201)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1001"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1001 的退款原因"]').setValue('customer_cancelled')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="refund-spinner"]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(999)
    expect(wrapper.find('[data-testid="refund-success"]').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    const success = wrapper.get('[data-testid="refund-success"]')
    expect(success.text()).toContain('退款已成功完成。')
    expect(success.find('[aria-hidden="true"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="refund-chain"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="refund-dialog"]').text()).not.toContain('全额退款金额')
  })

  it('distinguishes order, payment, and refund states with semantic colors', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse([
      { ...order, refund_id: 'r1', refund_status: 'SUCCEEDED' },
      { ...order, id: 'A-1002', refund_id: 'r2', refund_status: 'UNKNOWN' },
      { ...order, id: 'A-1003', refund_id: 'r3', refund_status: 'FAILED' },
      { ...order, id: 'A-1004', status: 'UNPAID' },
    ]))
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('[data-testid="order-status-A-1001"]').classes()).toContain('order-paid')
    expect(wrapper.get('[data-testid="payment-status-A-1001"]').classes()).toContain('payment-paid')
    expect(wrapper.get('[data-testid="refund-status-A-1001"]').classes()).toContain('refund-succeeded')
    expect(wrapper.get('[data-testid="refund-status-A-1002"]').classes()).toContain('refund-unknown')
    expect(wrapper.get('[data-testid="refund-status-A-1003"]').classes()).toContain('refund-failed')
    expect(wrapper.get('[data-testid="payment-status-A-1004"]').classes()).toContain('payment-unpaid')
  })

  it('requires an amount review before sending the refund request', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (init?.method === 'POST') return jsonResponse({
        id: 'refund-1', order_id: 'A-1001', amount_cents: 19900, currency: 'CNY',
        status: 'SUCCEEDED', reason: 'customer_cancelled', message: 'Refund completed successfully.',
        provider_channel: 'mock-success', provider_request_id: 'provider-1',
      }, 201)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1001"]').trigger('click')
    expect(wrapper.get('[data-testid="refund-dialog"]').text()).toContain('¥199.00')
    expect(wrapper.get('[data-testid="refund-chain"]').text()).toContain('渠道处理')
    await wrapper.get('[aria-label="订单 A-1001 的退款原因"]').setValue('customer_cancelled')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    expect(wrapper.get('[data-testid="refund-dialog"]').text()).toContain('确认全额退款')
    expect(wrapper.get('[data-testid="refund-dialog"]').text()).toContain('¥199.00')
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(0)
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1)
    expect(wrapper.get('[data-testid="refund-success"]').text()).toContain('退款已成功完成。')
  })

  it('shows the active refund chain while the channel request is pending', async () => {
    let finish!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (init?.method === 'POST') return new Promise<Response>((resolve) => { finish = resolve })
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1001"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1001 的退款原因"]').setValue('customer_cancelled')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    expect(wrapper.find('[data-testid="refund-spinner"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="refund-dialog"]').text()).toContain('正在提交退款')
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.find('[data-testid="refund-spinner"]').exists()).toBe(true)
    finish(new Response(JSON.stringify({
      id: 'refund-2', order_id: 'A-1001', amount_cents: 19900, currency: 'CNY',
      status: 'UNKNOWN', reason: 'customer_cancelled', message: 'Query the original refund.',
      provider_channel: 'mock-unknown', provider_request_id: 'provider-2',
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
    expect(wrapper.find('[data-testid="refund-spinner"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="refund-chain"]').text()).toContain('结果待确认')
    expect(wrapper.find('[data-testid="refund-success"]').exists()).toBe(false)
  })

  it('opens order details on a route from the scoped detail API', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (String(input) === '/api/orders/A-1001') return jsonResponse({
        ...order, refund_id: 'refund-1', refund_status: 'SUCCEEDED',
        refund_reason: 'customer_cancelled', refund_message: 'Refund completed successfully.',
        refund_updated_at: '2026-10-27T06:00:00Z',
        provider_request_id: 'provider-1',
      })
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="details-A-1001"]').trigger('click')
    await flushPromises()

    const page = wrapper.get('[data-testid="order-detail-page"]')
    expect(window.location.pathname).toBe('/orders/A-1001')
    expect(page.text()).toContain('订单 A-1001')
    expect(page.text()).toContain('¥199.00')
    expect(page.text()).toContain('customer_cancelled')
    expect(page.text()).toContain('退款已成功完成。')
    expect(page.text()).toContain('渠道请求号')
    expect(page.text()).toContain('provider-1')
    expect(fetchMock.mock.calls.at(-1)?.[0]).toBe('/api/orders/A-1001')
    await page.get('.detail-back').trigger('click')
    await flushPromises()
    expect(window.location.pathname).toBe('/')
    expect(wrapper.find('[data-testid="order-detail-page"]').exists()).toBe(false)
  })

  it('shows detail loading and request failure on the page', async () => {
    let finish!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      return new Promise<Response>((resolve) => { finish = resolve })
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="details-A-1001"]').trigger('click')
    expect(wrapper.get('[data-testid="order-detail-page"]').text()).toContain('正在加载订单详情')
    finish(new Response(JSON.stringify({ message: 'Order detail unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
    expect(wrapper.get('[data-testid="order-detail-page"] [role="alert"]').text()).toContain('请求失败（HTTP 503）')
  })

  it('rechecks the same order route under a new merchant and ignores the old response', async () => {
    let finish!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const authorization = (init?.headers as Record<string, string>)?.Authorization
      if (String(input) === '/api/orders' && authorization === 'Bearer demo-a-write') return jsonResponse([order])
      if (String(input) === '/api/orders/A-1001' && authorization === 'Bearer demo-a-write') return new Promise<Response>((resolve) => { finish = resolve })
      if (String(input) === '/api/orders/A-1001' && authorization === 'Bearer demo-b-write') return jsonResponse({ code: 'not_found' }, 404)
      if (String(input) === '/api/orders' && authorization === 'Bearer demo-b-write') return jsonResponse([{ ...order, id: 'B-2001', merchant_id: 'merchant-b' }])
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="details-A-1001"]').trigger('click')
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    expect(wrapper.get('h1').text()).toBe('选择用户')
    expect(wrapper.text()).not.toContain('订单 A-1001')
    await wrapper.get('[data-testid="select-merchant-b"]').trigger('click')
    await flushPromises()
    finish(new Response(JSON.stringify(order), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="order-detail-page"] [role="alert"]').text()).toContain('无权访问')
    expect(wrapper.text()).not.toContain('merchant-a')
    expect(wrapper.text()).not.toContain('¥199.00')
    await wrapper.get('.detail-back').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('订单 B-2001')
  })

  it('shows the persisted processing time for an existing refund', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse([{
      ...order, refund_id: 'refund-7', refund_status: 'PROCESSING',
      refund_updated_at: '2026-10-27T06:00:00Z',
      refund_message: 'Refund is processing.',
    }]))
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.text()).toContain('处理中')
    expect(wrapper.text()).toContain('更新于')
    expect(wrapper.get('[data-testid="refund-time-A-1001"]').attributes('title')).toBe('2026-10-27T06:00:00Z')
  })

  it('shows an API failure instead of an empty-list message', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => jsonResponse({ message: 'Orders unavailable' }, 503))
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('请求失败（HTTP 503）')
    expect(wrapper.text()).not.toContain('此商户暂无订单。')
  })

  it('removes the previous merchant orders while the new identity loads', async () => {
    let finish!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => {
      const authorization = (init?.headers as Record<string, string>)?.Authorization
      if (authorization === 'Bearer demo-a-write') return jsonResponse([order])
      return new Promise<Response>((resolve) => { finish = resolve })
    })
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.text()).toContain('订单 A-1001')
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    expect(wrapper.text()).not.toContain('订单 A-1001')
    await wrapper.get('[data-testid="select-merchant-b"]').trigger('click')
    expect(wrapper.text()).not.toContain('订单 A-1001')
    expect(wrapper.text()).toContain('正在加载订单')
    finish(new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
  })

  it('shows loading before an empty order result', async () => {
    let finish!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve }))
    const wrapper = mount(App)
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('正在加载订单')
    finish(new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
    expect(wrapper.text()).toContain('此商户暂无订单。')
    expect(wrapper.text()).not.toContain('正在加载订单')
  })

  it('keeps a failed refund submission visible after refreshing orders', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      if (init?.method === 'POST') return jsonResponse({ message: 'Channel temporarily unavailable' }, 503)
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1001"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1001 的退款原因"]').setValue('customer_cancelled')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[role="alert"]').text()).toContain('请求失败（HTTP 503）')
  })

  it('shows reconciliation in progress until the channel query completes', async () => {
    let finish!: (response: Response) => void
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders') return jsonResponse([{ ...order, refund_id: 'refund-7', refund_status: 'UNKNOWN' }])
      if (init?.method === 'POST') return new Promise<Response>((resolve) => { finish = resolve })
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="query-status-A-1001"]').trigger('click')
    expect(wrapper.text()).toContain('正在查询渠道结果')
    finish(new Response(JSON.stringify({ id: 'refund-7', order_id: 'A-1001', status: 'UNKNOWN', message: 'Still pending' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()
    expect(wrapper.text()).toContain('渠道结果尚未确认')
  })

  it('loads scoped orders and submits a reason with an idempotency header', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/orders') return jsonResponse([order])
      if (url.endsWith('/refunds') && init?.method === 'POST') {
        return jsonResponse({
          id: 'refund-1', order_id: 'A-1001', amount_cents: 19900, currency: 'CNY',
          status: 'SUCCEEDED', reason: 'customer_cancelled', message: 'Refund completed successfully.',
          provider_channel: 'mock-success', provider_request_id: 'provider-1',
        }, 201)
      }
      throw new Error(`Unexpected request: ${url}`)
    })
    const wrapper = mount(App)
    await flushPromises()

    expect(wrapper.text()).toContain('订单 A-1001')
    expect(fetchMock.mock.calls[0][1]?.headers).toMatchObject({ Authorization: 'Bearer demo-a-write' })
    await wrapper.get('[data-testid="refund-A-1001"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1001 的退款原因"]').setValue('customer_cancelled')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)

    const createCall = fetchMock.mock.calls.find(([url, init]) => String(url).endsWith('/refunds') && init?.method === 'POST')
    expect(createCall).toBeDefined()
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({ reason: 'customer_cancelled' })
    expect(createCall?.[1]?.headers).toMatchObject({ 'Idempotency-Key': expect.any(String) })
    expect(wrapper.text()).toContain('退款成功')
  })

  it('keeps the readonly identity unable to submit refunds', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/orders') return jsonResponse([order])
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    await wrapper.get('[data-testid="select-readonly"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="refund-A-1001"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('只读')
    expect(fetchMock.mock.calls.some(([, init]) => (init?.headers as Record<string, string>)?.Authorization === 'Bearer demo-a-read')).toBe(true)
  })

  it('reconciles an unknown refund through the reconcile endpoint', async () => {
    const pendingOrder = { ...order, refund_id: 'refund-7', refund_status: 'UNKNOWN' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/orders') return jsonResponse([pendingOrder])
      if (url.endsWith('/refunds/refund-7/reconcile') && init?.method === 'POST') {
        return jsonResponse({
          id: 'refund-7', order_id: 'A-1001', amount_cents: 19900, currency: 'CNY',
          status: 'SUCCEEDED', reason: 'customer_cancelled', message: 'Refund completed successfully.',
          provider_channel: 'mock-unknown', provider_request_id: 'provider-7',
        })
      }
      throw new Error(`Unexpected request: ${url}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="query-status-A-1001"]').trigger('click')
    await flushPromises()

    expect(fetchMock.mock.calls.some(([url, init]) => String(url).endsWith('/refunds/refund-7/reconcile') && init?.method === 'POST')).toBe(true)
    expect(wrapper.text()).toContain('退款成功')
  })

  it('refreshes an unknown refund without reconciling the provider', async () => {
    const pendingOrder = { ...order, refund_id: 'refund-7', refund_status: 'UNKNOWN' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/orders') return jsonResponse([pendingOrder])
      if (url === '/api/refunds/refund-7') return jsonResponse({
        id: 'refund-7', order_id: 'A-1001', amount_cents: 19900, currency: 'CNY',
        status: 'UNKNOWN', reason: '客户取消', message: 'Channel response was lost.',
        provider_channel: 'mock-unknown', provider_request_id: 'provider-7',
      })
      throw new Error(`Unexpected request: ${url}`)
    })
    const wrapper = mount(App)
    await flushPromises()

    const refresh = wrapper.get('[data-testid="refresh-status-A-1001"]')
    expect(refresh.attributes('aria-label')).toBe('刷新订单 A-1001 的退款状态')
    await refresh.trigger('click')
    await flushPromises()

    expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/refunds/refund-7')).toBe(true)
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
    expect(wrapper.get('[data-testid="refund-status-A-1001"]').text()).toBe('结果待确认')
    expect(wrapper.get('[data-testid="query-status-A-1001"]').text()).toBe('查询处理结果')
  })

  it('recovers A-1002 from the single query action after an unknown submission', async () => {
    const pendingOrder = { ...order, id: 'A-1002', amount_cents: 8800, provider_channel: 'mock-unknown' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/orders') return jsonResponse([pendingOrder])
      if (url === '/api/orders/A-1002/refunds' && init?.method === 'POST') return jsonResponse({
        id: 'refund-2', order_id: 'A-1002', amount_cents: 8800, currency: 'CNY',
        status: 'UNKNOWN', reason: '客户取消', message: 'Channel response was lost.',
        provider_channel: 'mock-unknown', provider_request_id: 'stable-2',
      }, 201)
      if (url === '/api/refunds/refund-2/reconcile' && init?.method === 'POST') return jsonResponse({
        id: 'refund-2', order_id: 'A-1002', amount_cents: 8800, currency: 'CNY',
        status: 'SUCCEEDED', reason: '客户取消', message: 'Refund completed successfully.',
        provider_channel: 'mock-unknown', provider_request_id: 'stable-2',
      })
      throw new Error(`Unexpected request: ${url}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1002"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1002 的退款原因"]').setValue('客户取消')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[data-testid="refund-status-A-1002"]').text()).toBe('结果待确认')
    await wrapper.get('[data-testid="refund-dialog"] button').trigger('click')
    const query = wrapper.get('[data-testid="query-status-A-1002"]')
    expect(query.text()).toBe('查询处理结果')
    expect(query.classes()).toContain('primary-button')
    expect(wrapper.find('[data-testid="reconcile-A-1002"]').exists()).toBe(false)
    await query.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="refund-status-A-1002"]').text()).toBe('退款成功')
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(2)
    expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/refunds/refund-2')).toBe(false)
  })

  it('recovers an unknown A-1002 directly inside the refund dialog', async () => {
    const pendingOrder = { ...order, id: 'A-1002', amount_cents: 8800, provider_channel: 'mock-unknown' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/orders') return jsonResponse([pendingOrder])
      if (url === '/api/orders/A-1002/refunds' && init?.method === 'POST') return jsonResponse({
        id: 'refund-2', order_id: 'A-1002', amount_cents: 8800, currency: 'CNY',
        status: 'UNKNOWN', reason: '客户取消', message: 'Channel response was lost.',
        provider_channel: 'mock-unknown', provider_request_id: 'stable-2',
      }, 201)
      if (url === '/api/refunds/refund-2/reconcile' && init?.method === 'POST') return jsonResponse({
        id: 'refund-2', order_id: 'A-1002', amount_cents: 8800, currency: 'CNY',
        status: 'SUCCEEDED', reason: '客户取消', message: 'Refund completed successfully.',
        provider_channel: 'mock-unknown', provider_request_id: 'stable-2',
      })
      throw new Error(`Unexpected request: ${url}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="refund-A-1002"]').trigger('click')
    await wrapper.get('[aria-label="订单 A-1002 的退款原因"]').setValue('客户取消')
    await wrapper.get('[data-testid="refund-continue"]').trigger('click')
    vi.useFakeTimers()
    await wrapper.get('[data-testid="refund-confirm"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[data-testid="refund-chain"]').text()).toContain('结果待确认')
    await wrapper.get('[data-testid="refund-dialog"]').get('button:not(.dialog-close)').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="refund-success"]').text()).toContain('退款已成功完成')
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(2)
  })

  it('offers original-request recovery on an unknown order detail page', async () => {
    window.history.replaceState({}, '', '/orders/A-1002')
    const pendingOrder = { ...order, id: 'A-1002', amount_cents: 8800, provider_channel: 'mock-unknown',
      refund_id: 'refund-2', refund_status: 'UNKNOWN' as const, provider_request_id: 'stable-2' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/orders/A-1002') return jsonResponse(pendingOrder)
      if (String(input) === '/api/refunds/refund-2/reconcile' && init?.method === 'POST') return jsonResponse({
        id: 'refund-2', order_id: 'A-1002', amount_cents: 8800, currency: 'CNY',
        status: 'SUCCEEDED', reason: '客户取消', message: 'Refund completed successfully.',
        provider_channel: 'mock-unknown', provider_request_id: 'stable-2',
      })
      throw new Error(`Unexpected request: ${String(input)}`)
    })
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('[data-testid="detail-reconcile-A-1002"]').trigger('click')
    await flushPromises()
    const page = wrapper.get('[data-testid="order-detail-page"]')
    expect(page.text()).toContain('退款成功')
    expect(page.text()).toContain('stable-2')
    expect(fetchMock.mock.calls.some(([url, init]) => String(url) === '/api/refunds/refund-2/reconcile' && init?.method === 'POST')).toBe(true)
  })

  it('does not let a stale merchant response replace the current identity data', async () => {
    let resolveMerchantA!: (response: Response) => void
    const merchantAResponse = new Promise<Response>((resolve) => { resolveMerchantA = resolve })
    const merchantBOrder = { ...order, id: 'B-2001', merchant_id: 'merchant-b' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => {
      const authorization = (init?.headers as Record<string, string>)?.Authorization
      if (authorization === 'Bearer demo-a-write') return merchantAResponse
      if (authorization === 'Bearer demo-b-write') return jsonResponse([merchantBOrder])
      throw new Error(`Unexpected identity: ${authorization}`)
    })
    const wrapper = mount(App)
    await wrapper.get('[data-testid="switch-user"]').trigger('click')
    await wrapper.get('[data-testid="select-merchant-b"]').trigger('click')
    await flushPromises()
    resolveMerchantA(new Response(JSON.stringify([order]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await flushPromises()

    expect(wrapper.text()).toContain('订单 B-2001')
    expect(wrapper.text()).not.toContain('订单 A-1001')
    expect(fetchMock.mock.calls).toHaveLength(2)
  })
})
