import axios from 'axios'
import { reactive, readonly } from 'vue'

export interface SessionResponse {
  authenticated: true
  auth_enabled: boolean
  user: string
  expires_at?: string
  expires_in?: number
}

interface AuthClient {
  get(url: string): Promise<{ data: unknown }>
  post(url: string, data?: unknown): Promise<{ data: unknown }>
}

export function authErrorMessage(error: unknown, action = '验证登录状态'): string {
  const status = (error as any)?.response?.status
  if (status === 503) return `${action}失败：认证服务暂时不可用，请稍后重试`
  if (status === 403) return `${action}失败：请求来源被拒绝，请使用本站页面重试`
  if (!(error as any)?.response && (error as any)?.isAxiosError) {
    return `${action}失败：无法连接服务器，请检查网络后重试`
  }
  return `${action}失败，请重试`
}

// Legacy admin tokens are deliberately discarded on upgrade. No credentials are
// persisted by this module, and the HttpOnly cookie is never read by JavaScript.
export function clearLegacyCredentials(storage: Pick<Storage, 'removeItem'>) {
  try {
    storage.removeItem('token')
    storage.removeItem('user')
  } catch { /* Storage can be disabled; it is not used for authentication. */ }
}

export function safeAuthRedirect(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return '/dashboard'
  if (value.split(/[?#]/, 1)[0] === '/login') return '/dashboard'
  return value
}

export function createAuthSession(client: AuthClient, now: () => number = Date.now) {
  const state = reactive({
    authenticated: false,
    checked: false,
    authEnabled: true,
    user: '',
    expiresAt: null as string | null,
    error: null as string | null,
  })
  let cacheUntil = 0
  let revision = 0
  let pending: Promise<boolean> | undefined
  let onUnauthorized: (() => void) | undefined

  function clear() {
    revision++
    pending = undefined
    cacheUntil = now() + 60_000
    Object.assign(state, { authenticated: false, checked: true, authEnabled: true, user: '', expiresAt: null, error: null })
  }

  function accept(data: unknown) {
    const session = data as SessionResponse
    if (!session || session.authenticated !== true || typeof session.auth_enabled !== 'boolean' || typeof session.user !== 'string') {
      throw new Error('Invalid session response')
    }
    const expiry = session.auth_enabled ? Date.parse(session.expires_at || '') : Infinity
    if (session.auth_enabled && (!Number.isFinite(expiry) || expiry <= now())) {
      throw new Error('Invalid session expiry')
    }
    revision++
    pending = undefined
    // A sliding renewal from any other API may extend the cookie. Recheck on
    // navigation rather than assuming that extension or running a heartbeat.
    cacheUntil = Math.min(now() + 60_000, expiry)
    Object.assign(state, {
      authenticated: true, checked: true, authEnabled: session.auth_enabled,
      user: session.user, expiresAt: session.auth_enabled ? session.expires_at! : null, error: null,
    })
  }

  function ensureSession(force = false): Promise<boolean> {
    if (pending) return pending
    if (!force && state.checked && now() < cacheUntil) return Promise.resolve(state.authenticated)
    const started = revision
    let request!: Promise<boolean>
    request = (async () => {
      try {
        const response = await client.get('/auth/session')
        if (started !== revision) return state.authenticated
        accept(response.data)
        return true
      } catch (error: any) {
        if (started !== revision) return state.authenticated
        if (error.response?.status === 401) {
          clear()
          return false
        }
        // A database outage/network failure says nothing about cookie validity.
        // Keep the last known identity, but never use its cache to bypass retry.
        cacheUntil = 0
        state.error = authErrorMessage(error)
        throw error
      } finally {
        if (pending === request) pending = undefined
      }
    })()
    pending = request
    return request
  }

  async function login(username: string, password: string) {
    revision++
    pending = undefined
    const response = await client.post('/auth/login', { username, password })
    accept(response.data)
  }

  async function logout() {
    const response = await client.post('/auth/logout')
    if ((response.data as any)?.ok !== true) throw new Error('Logout not confirmed')
    clear()
  }

  function handleUnauthorized(url: string | undefined, expectedRevision: number | undefined) {
    // Login 401 is a credential error; logout failures must not look successful.
    if (/^(?:\/api)?\/auth\/(?:login|session|logout)(?:[?#]|$)/.test(url || '')) return
    if (expectedRevision !== revision) return // Ignore a response from a previous login/session.
    clear()
    onUnauthorized?.()
  }

  return {
    state: readonly(state), ensureSession, login, logout,
    getRevision: () => revision,
    handleUnauthorized,
    onUnauthorized: (handler: () => void) => { onUnauthorized = handler },
  }
}

if (typeof window !== 'undefined') {
  try { clearLegacyCredentials(window.localStorage) } catch { /* Disabled storage. */ }
}

export const auth = createAuthSession(axios.create({
  baseURL: '/api', timeout: 30_000, withCredentials: true,
}))
export const authState = auth.state
