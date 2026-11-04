import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as vue from 'vue'

async function loadTS(file, imports) {
  const source = await readFile(new URL(file, import.meta.url), 'utf8')
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText
  const module = { exports: {} }
  new Function('require', 'module', 'exports', code)(name => {
    if (!(name in imports)) throw new Error(`Unexpected dependency: ${name}`)
    return imports[name]
  }, module, module.exports)
  return module.exports
}
const noClient = { get: async () => { throw new Error('Unexpected request') }, post: async () => { throw new Error('Unexpected request') } }
const helpers = await loadTS('../src/auth.ts', { axios: { create: () => noClient }, vue })
const { createAuthSession, clearLegacyCredentials, safeAuthRedirect, authErrorMessage } = helpers
const epoch = Date.parse('2030-01-01T00:00:00Z')
const session = (expires = epoch + 30 * 86400_000) => ({ authenticated: true, auth_enabled: true, user: 'synthetic-admin', expires_at: new Date(expires).toISOString(), expires_in: (expires - epoch) / 1000 })
const httpError = status => ({ response: { status }, isAxiosError: true })
function deferred() { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

test('legacy credentials are removed, never read or persisted', () => {
  const removed = []
  clearLegacyCredentials({ removeItem: key => removed.push(key) })
  assert.deepEqual(removed, ['token', 'user'])
  assert.doesNotThrow(() => clearLegacyCredentials({ removeItem() { throw new Error('disabled storage') } }))
})

test('reload verifies session; navigation uses a 60 second TTL and deduplicates requests', async () => {
  let time = epoch, calls = 0
  const wait = deferred()
  const auth = createAuthSession({ ...noClient, get: () => { calls++; return wait.promise } }, () => time)
  const first = auth.ensureSession(), second = auth.ensureSession()
  assert.equal(first, second)
  wait.resolve({ data: session() })
  assert.equal(await first, true)
  assert.equal(auth.state.user, 'synthetic-admin')
  await auth.ensureSession()
  assert.equal(calls, 1)
  time += 60_000
  await auth.ensureSession()
  assert.equal(calls, 2)
})

test('session cache never outlives expires_at and accepts a renewed expiry', async () => {
  let time = epoch, calls = 0
  const auth = createAuthSession({ ...noClient, get: async () => ({ data: session(++calls === 1 ? epoch + 5000 : epoch + 86400_000) }) }, () => time)
  await auth.ensureSession()
  time += 5000
  await auth.ensureSession()
  assert.equal(calls, 2)
  assert.equal(auth.state.expiresAt, new Date(epoch + 86400_000).toISOString())
})

test('401 clears session, while 503/network errors preserve identity and allow immediate retry', async () => {
  let failure, calls = 0
  const auth = createAuthSession({ ...noClient, get: async () => { calls++; if (failure) throw failure; return { data: session() } } }, () => epoch)
  await auth.ensureSession()
  for (const error of [httpError(503), { isAxiosError: true }]) {
    failure = error
    await assert.rejects(auth.ensureSession(true))
    assert.equal(auth.state.authenticated, true)
    assert.equal(auth.state.user, 'synthetic-admin')
    assert.doesNotMatch(auth.state.error, /过期|失效/)
    failure = undefined
    assert.equal(await auth.ensureSession(), true)
  }
  failure = httpError(401)
  assert.equal(await auth.ensureSession(true), false)
  assert.equal(auth.state.authenticated, false)
  assert.equal(auth.state.user, '')
  assert.equal(calls, 6)
})

test('disabled auth requires server verification but needs no expiry or logout', async () => {
  const auth = createAuthSession({ ...noClient, get: async () => ({ data: { authenticated: true, auth_enabled: false, user: 'synthetic-config-user' } }) }, () => epoch)
  assert.equal(await auth.ensureSession(), true)
  assert.equal(auth.state.authEnabled, false)
  assert.equal(auth.state.expiresAt, null)
})

test('login posts synthetic credentials only to server, without token/storage state', async () => {
  const requests = []
  const auth = createAuthSession({ ...noClient, post: async (...args) => { requests.push(args); return { data: session() } } }, () => epoch)
  await auth.login('synthetic-user', 'synthetic-password')
  assert.deepEqual(requests, [['/auth/login', { username: 'synthetic-user', password: 'synthetic-password' }]])
  assert.equal(auth.state.authenticated, true)
  assert.equal('token' in auth.state, false)
})

test('logout clears only after confirmed server success, failures do not pretend revocation', async () => {
  let result = { data: session() }, failure
  const auth = createAuthSession({ ...noClient, post: async () => { if (failure) throw failure; return result } }, () => epoch)
  await auth.login('synthetic-user', 'synthetic-password')
  for (const status of [503, 403, 401]) {
    failure = httpError(status)
    await assert.rejects(auth.logout())
    assert.equal(auth.state.authenticated, true)
  }
  failure = undefined
  result = { data: { ok: false } }
  await assert.rejects(auth.logout())
  assert.equal(auth.state.authenticated, true)
  result = { data: { ok: true } }
  await auth.logout()
  assert.equal(auth.state.authenticated, false)
})

test('late session results cannot resurrect logout or overwrite a newer login', async () => {
  const wait = deferred()
  const auth = createAuthSession({ get: () => wait.promise, post: async url => ({ data: url === '/auth/logout' ? { ok: true } : session() }) }, () => epoch)
  const pending = auth.ensureSession()
  await auth.logout()
  wait.resolve({ data: session() })
  assert.equal(await pending, false)
  assert.equal(auth.state.authenticated, false)
})

test('late session responses cannot overwrite a newer login', async () => {
  const wait = deferred()
  const auth = createAuthSession({ get: () => wait.promise, post: async () => ({ data: session() }) }, () => epoch)
  const pending = auth.ensureSession()
  await auth.login('synthetic-user', 'synthetic-password')
  wait.reject(httpError(401))
  assert.equal(await pending, true)
  assert.equal(auth.state.user, 'synthetic-admin')
})

test('expired or malformed session responses are not cached as authenticated', async () => {
  for (const data of [session(epoch - 1000), { authenticated: true }, { ...session(), expires_at: 'invalid' }]) {
    const auth = createAuthSession({ ...noClient, get: async () => ({ data }) }, () => epoch)
    await assert.rejects(auth.ensureSession())
    assert.equal(auth.state.authenticated, false)
    assert.ok(auth.state.error)
  }
})

test('admin 401 excludes only auth endpoints and ignores stale identity generations', async () => {
  const auth = createAuthSession({ ...noClient, post: async () => ({ data: session() }) }, () => epoch)
  let navigations = 0
  auth.onUnauthorized(() => navigations++)
  await auth.login('synthetic-user', 'synthetic-password')
  const old = auth.getRevision()
  for (const url of ['/auth/login', '/auth/session', '/auth/logout', '/api/auth/login']) auth.handleUnauthorized(url, old)
  assert.equal(auth.state.authenticated, true)
  await auth.login('synthetic-user', 'synthetic-password')
  auth.handleUnauthorized('/channels', old)
  assert.equal(navigations, 0)
  auth.handleUnauthorized('/cpa/auth/login', auth.getRevision())
  assert.equal(navigations, 1)
  assert.equal(auth.state.authenticated, false)
})

test('management axios uses cookies and no admin Authorization; model API Bearer is preserved', async () => {
  const handlers = {}, calls = [], configurations = []
  const client = { interceptors: { request: { use: handler => { handlers.request = handler } }, response: { use: (success, error) => { handlers.error = error } } } }
  const unauthorized = []
  const axios = { create: config => { configurations.push(config); return client }, get: (...args) => calls.push(args), post: (...args) => calls.push(args) }
  const api = await loadTS('../src/api/index.ts', { axios, './chatTest': { buildChatRequest: () => ({ url: '/v1/responses', body: {} }), streamChatRequest: () => {} }, '../auth': { auth: { getRevision: () => 7, handleUnauthorized: (...args) => unauthorized.push(args) } } })
  assert.equal(configurations[0].withCredentials, true)
  const config = handlers.request({ headers: {} })
  assert.equal(config.headers.Authorization, undefined)
  assert.equal(config.authRevision, 7)
  const failure = { response: { status: 401 }, config: { url: '/channels', authRevision: 7 } }
  await assert.rejects(handlers.error(failure))
  assert.deepEqual(unauthorized, [['/channels', 7]])
  api.chatTestApi.models('synthetic-model-key')
  api.chatTestApi.chat('synthetic-model-key', 'test-model', 'hello', 'responses')
  assert.equal(calls[0][1].headers.Authorization, 'Bearer synthetic-model-key')
  assert.equal(calls[1][2].headers.Authorization, 'Bearer synthetic-model-key')
})

async function loadGuard(ensureSession) {
  let guard, callback
  const router = { currentRoute: { value: { path: '/channels', fullPath: '/channels?test=1' } }, beforeEach: fn => { guard = fn }, replace: destination => destination }
  await loadTS('../src/router/index.ts', { 'vue-router': { createRouter: () => router, createWebHistory: () => ({}) }, '../auth': { auth: { ensureSession, onUnauthorized: fn => { callback = fn } }, safeAuthRedirect } })
  return { guard, callback }
}

test('guard enters protected pages after reload and redirects genuine anonymous users', async () => {
  const protectedRoute = { path: '/channels', fullPath: '/channels', meta: { requiresAuth: true }, query: {} }
  const active = await loadGuard(async () => true)
  assert.equal(await active.guard(protectedRoute), true)
  assert.equal(await active.guard({ path: '/login', meta: {}, query: {} }), '/dashboard')
  const anonymous = await loadGuard(async () => false)
  assert.deepEqual(await anonymous.guard(protectedRoute), { path: '/login', query: { redirect: '/channels' } })
})

test('guard temporary failures land on retryable login without redirect loops', async () => {
  const { guard } = await loadGuard(async () => { throw httpError(503) })
  assert.deepEqual(await guard({ path: '/channels', fullPath: '/channels', meta: { requiresAuth: true }, query: {} }), { path: '/login', query: { redirect: '/channels' } })
  assert.equal(await guard({ path: '/login', meta: {}, query: {} }), true)
})

test('redirect destinations stay local and never loop back to login', () => {
  for (const value of ['https://example.invalid', '//example.invalid', '/\\example.invalid', '/login', '/login?redirect=/login', undefined]) assert.equal(safeAuthRedirect(value), '/dashboard')
  assert.equal(safeAuthRedirect('/channels?tab=test'), '/channels?tab=test')
  assert.match(authErrorMessage(httpError(503)), /暂时不可用/)
})

test('login/header show retry and error feedback and do not persist admin credentials', async () => {
  const login = await readFile(new URL('../src/views/Login.vue', import.meta.url), 'utf8')
  const header = await readFile(new URL('../src/components/HeaderBar.vue', import.meta.url), 'utf8')
  assert.match(login, /重试会话验证/)
  assert.match(login, /用户名或密码错误/)
  assert.match(header, /authState\.user/)
  assert.match(header, /authState\.authEnabled/)
  assert.match(header, /await auth\.logout\(\)/)
  assert.match(header, /未确认退出成功/)
  for (const text of [login, header]) assert.doesNotMatch(text, /localStorage|document\.cookie|window\.location/)
})
