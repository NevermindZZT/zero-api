import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as vue from 'vue'
import { parse, compileScript } from '@vue/compiler-sfc'

async function loadModule(source, imports) {
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText
  const module = { exports: {} }
  new Function('require', 'module', 'exports', code)(name => {
    if (!(name in imports)) throw new Error(`Unexpected dependency: ${name}`)
    return imports[name]
  }, module, module.exports)
  return module.exports
}
const source = await readFile(new URL('../src/views/dashboardData.ts', import.meta.url), 'utf8')
const helpers = await loadModule(source, {})
const { apiKeyOptionLabel, createLatestRequestGate, fetchDashboardData } = helpers
const defer = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

test('safe option labels identify disabled and deleted keys without displaying secrets', () => {
  assert.equal(apiKeyOptionLabel({ id: 1, name: 'client', enabled: true, deleted: false }), 'client (#1)')
  assert.equal(apiKeyOptionLabel({ id: 2, name: 'client', enabled: false, deleted: false }), 'client (#2)（已禁用）')
  assert.equal(apiKeyOptionLabel({ id: 3, name: '', enabled: false, deleted: true }), '已删除密钥 #3')
})

test('one captured filter is sent to all statistics including yearly heatmap', async () => {
  const calls = []
  const api = Object.fromEntries(['overview', 'daily', 'records', 'byModel', 'latency', 'yearHeatmap'].map(name => [name, async (...args) => { calls.push([name, args]); return { data: name } }]))
  const result = await fetchDashboardData(api, { apiKeyId: 17, start: '2026-01-01', end: '2026-01-02', granularity: 'hour', tzOffset: 480 })
  assert.deepEqual(calls, [
    ['overview', [17, '2026-01-01', '2026-01-02', 480]],
    ['daily', ['2026-01-01', '2026-01-02', 17, 'hour', 480]],
    ['records', [17, '2026-01-01', '2026-01-02', 200, 480]],
    ['byModel', ['2026-01-01', '2026-01-02', 17, 480]],
    ['latency', ['2026-01-01', '2026-01-02', 17, 'hour', 480]],
    ['yearHeatmap', [480, 17]],
  ])
  assert.equal(result.heatmap, 'yearHeatmap')
  calls.length = 0
  await fetchDashboardData(api, { start: '2026-01-01', end: '2026-01-01', granularity: 'day', tzOffset: 0 })
  assert.equal(calls[0][1][0], undefined)
  assert.equal(calls[5][1][1], undefined)
})

test('request gate rejects stale responses and invalidation', () => {
  const gate = createLatestRequestGate()
  const first = gate.issue()
  assert.equal(first(), true)
  const second = gate.issue()
  assert.equal(first(), false)
  assert.equal(second(), true)
  gate.invalidate()
  assert.equal(second(), false)
})

async function makeDashboard() {
  const file = await readFile(new URL('../src/views/Dashboard.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(file)
  const script = compileScript(descriptor, { id: 'dashboard-test' }).content
  const mounted = [], unmounted = [], calls = []
  let totalRequests = 1, todayRequests = 1
  const response = (method, args) => {
    calls.push({ method, args })
    return { data: method === 'overview' ? { total_requests: totalRequests, today_requests: todayRequests, selected: args[0] } : [] }
  }
  let delayedOverview
  const usageApi = Object.fromEntries(['overview', 'daily', 'records', 'byModel', 'latency', 'yearHeatmap', 'tps'].map(method => [method, async (...args) => {
    if (method === 'overview' && args[0] === 1 && delayedOverview) {
      calls.push({ method, args })
      return delayedOverview.promise
    }
    return response(method, args)
  }]))
  const fakeVue = { ...vue, onMounted: fn => mounted.push(fn), onUnmounted: fn => unmounted.push(fn) }
  const empty = new Proxy({}, { get: () => ({}) })
  const imports = {
    vue: fakeVue,
    'naive-ui': empty,
    '@vicons/ionicons5': empty,
    '@/api': { usageApi, apiKeyOptionsApi: { list: async () => ({ data: [{ id: 1, name: 'one', enabled: true, deleted: false }, { id: 2, name: '', enabled: false, deleted: true }] }) } },
    './dashboardData': helpers,
    '@/utils/format': { formatDateTime: x => x },
    '@/utils/theme': { isDarkTheme: vue.ref(false) },
    echarts: empty,
    '@silverwind/vue3-calendar-heatmap': empty,
    '@silverwind/vue3-calendar-heatmap/dist/style.css': {},
  }
  const component = (await loadModule(script, imports)).default
  globalThis.window = { innerWidth: 900, addEventListener() {}, removeEventListener() {} }
  const bindings = component.setup({}, { expose() {} })
  for (const fn of mounted) await fn()
  return { bindings, calls, delay: value => { delayedOverview = value }, counts: (total, today) => { totalRequests = total; todayRequests = today }, close: () => unmounted.forEach(fn => fn()) }
}

test('dashboard switches all data, handles custom edits and rejects old key responses', async () => {
  const d = await makeDashboard()
  try {
    assert.equal(d.bindings.apiKeyOptions.value[1].label, '已删除密钥 #2')
    const pending = defer()
    d.delay(pending)
    d.bindings.selectedApiKeyId.value = 1
    await vue.nextTick()
    d.bindings.selectedApiKeyId.value = 2
    await vue.nextTick()
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(d.bindings.overview.value.selected, 2)
    pending.resolve({ data: { total_requests: 7, selected: 1 } })
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(d.bindings.overview.value.selected, 2)
    assert.ok(d.calls.some(c => c.method === 'yearHeatmap' && c.args[1] === 2))
    d.calls.length = 0
    await d.bindings.smartPoll()
    assert.ok(d.calls.some(c => c.method === 'overview' && c.args[0] === 2))
    assert.ok(!d.calls.some(c => c.method === 'yearHeatmap'), 'unchanged polling must not query annual heatmap')
    assert.ok(d.calls.some(c => c.method === 'tps' && c.args.length === 2))
    d.calls.length = 0
    d.counts(1, 2)
    await d.bindings.smartPoll()
    assert.ok(d.calls.some(c => c.method === 'yearHeatmap' && c.args[1] === 2), 'today count change refreshes heatmap')
    d.calls.length = 0
    d.counts(2, 2)
    await d.bindings.smartPoll()
    assert.ok(d.calls.some(c => c.method === 'yearHeatmap' && c.args[1] === 2), 'range count change refreshes heatmap')
    d.calls.length = 0
    d.bindings.loadError.value = 'failed'
    await d.bindings.smartPoll()
    assert.ok(d.calls.some(c => c.method === 'yearHeatmap' && c.args[1] === 2), 'failed load is retried')
    d.calls.length = 0
    await d.bindings.refresh()
    for (const method of ['overview', 'daily', 'records', 'byModel', 'latency', 'yearHeatmap']) {
      assert.ok(d.calls.some(c => c.method === method && c.args.includes(2)), `${method} follows selection`)
    }
    const first = [new Date(2026, 0, 1).getTime(), new Date(2026, 0, 2).getTime()]
    const second = [new Date(2026, 0, 3).getTime(), new Date(2026, 0, 4).getTime()]
    d.bindings.onCustomRangeChange(first)
    await vue.nextTick()
    d.bindings.onCustomRangeChange(second)
    await vue.nextTick()
    assert.ok(d.calls.some(c => c.method === 'daily' && c.args[0] === '2026-01-03' && c.args[1] === '2026-01-04' && c.args[2] === 2))
    d.bindings.onCustomRangeChange(null)
    await vue.nextTick()
    assert.equal(d.bindings.timeRange.value, 'month')
    d.bindings.selectedApiKeyId.value = null
    await vue.nextTick()
    assert.ok(d.calls.some(c => c.method === 'yearHeatmap' && c.args[1] === undefined))
  } finally { d.close(); delete globalThis.window }
})


test('an old poll cannot reload data after the selected key changes', async () => {
  const d = await makeDashboard()
  try {
    d.bindings.selectedApiKeyId.value = 1
    await vue.nextTick()
    await new Promise(resolve => setImmediate(resolve))
    const pending = defer()
    d.delay(pending)
    const poll = d.bindings.smartPoll()
    d.bindings.selectedApiKeyId.value = 2
    await vue.nextTick()
    await new Promise(resolve => setImmediate(resolve))
    d.calls.length = 0
    pending.resolve({ data: { total_requests: 99, today_requests: 99, selected: 1 } })
    await poll
    assert.equal(d.bindings.overview.value.selected, 2)
    assert.equal(d.calls.length, 0)
  } finally { d.close(); delete globalThis.window }
})

test('usage daily cache ratio uses input tokens and avoids division by zero', async () => {
  const file = await readFile(new URL('../src/views/Usage.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(file)
  const script = compileScript(descriptor, { id: 'usage-test' }).content
  const fakeVue = { ...vue, onMounted() {}, onUnmounted() {} }
  const empty = new Proxy({}, { get: () => ({}) })
  const imports = {
    vue: fakeVue,
    'naive-ui': empty,
    '@vicons/ionicons5': empty,
    '@/api': { usageApi: {}, apiKeyOptionsApi: {} },
    './dashboardData': helpers,
    '@/utils/format': { formatDateTime: x => x },
  }
  const component = (await loadModule(script, imports)).default
  const bindings = component.setup({}, { expose() {} })
  const column = bindings.dailyColumns.find(c => c.key === 'cache_hit_rate')
  assert.equal(column.render({ cache_hit_tokens: 25, prompt_tokens: 100, total_tokens: 200 }), '25.0%')
  assert.equal(column.render({ cache_hit_tokens: 0, prompt_tokens: 0, total_tokens: 200 }), '-')
})
