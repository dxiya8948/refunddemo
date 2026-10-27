import { onMounted, onUnmounted, ref } from 'vue'

export type AppRoute =
  | { kind: 'orders' }
  | { kind: 'order'; orderId: string }
  | { kind: 'not-found' }

function readRoute(pathname: string): AppRoute {
  if (pathname === '/') return { kind: 'orders' }
  const match = /^\/orders\/([^/]+)\/?$/.exec(pathname)
  if (!match) return { kind: 'not-found' }
  try {
    return { kind: 'order', orderId: decodeURIComponent(match[1]) }
  } catch {
    return { kind: 'not-found' }
  }
}

export function orderPath(orderId: string) {
  return `/orders/${encodeURIComponent(orderId)}`
}

export function useAppRoute() {
  const route = ref<AppRoute>(readRoute(window.location.pathname))
  const sync = () => { route.value = readRoute(window.location.pathname) }
  onMounted(() => window.addEventListener('popstate', sync))
  onUnmounted(() => window.removeEventListener('popstate', sync))

  function navigate(path: string) {
    if (window.location.pathname === path) return
    window.history.pushState({}, '', path)
    sync()
  }

  return { route, navigate }
}
