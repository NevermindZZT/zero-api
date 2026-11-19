import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as vue from 'vue'
import { parse, compileScript } from '@vue/compiler-sfc'

const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const tick = () => new Promise(resolve => setImmediate(resolve))
const fixture = () => ({ preview_id: 'preview-1', models: [{ id: 'one', name: 'One', existing: true }, { id: 'two', name: 'Two', existing: false }], stale_models: [{ model_id: 'old', display_name: 'Old', user_modified: true }] })

async function makeChannels() {
  const file = await readFile(new URL('../src/views/Channels.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(file)
  const script = compileScript(descriptor, { id: 'channels-sync-test' }).content
  const code = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText
  const calls = [], messages = [], unmounted = []
  let preview = async () => ({ data: fixture() })
  let confirm = async () => ({ data: { count: 0, deleted_count: 1 } })
  const channelApi = {
    previewSyncModels: async id => { calls.push(['preview', id]); return preview(id) },
    confirmSyncModels: async (...args) => { calls.push(['confirm', ...args]); return confirm(...args) },
    list: async () => ({ data: [{ id: 1, name: '测试渠道' }, { id: 2, name: '其他渠道' }] }),
  }
  const empty = new Proxy({}, { get: () => ({}) })
  const naive = new Proxy({ useMessage: () => ({ success: text => messages.push(text), error: text => messages.push(text), info: text => messages.push(text) }) }, { get: (target, key) => key in target ? target[key] : {} })
  const imports = {
    vue: { ...vue, onMounted() {}, onUnmounted: fn => unmounted.push(fn) },
    'naive-ui': naive,
    '@vicons/ionicons5': empty,
    '@/api': { __esModule: true, default: {}, channelApi, balanceApi: {} },
  }
  const mod = { exports: {} }
  new Function('require', 'module', 'exports', code)(name => { if (!(name in imports)) throw new Error(`Unexpected import ${name}`); return imports[name] }, mod, mod.exports)
  const bindings = mod.exports.default.setup({}, { expose() {} })
  bindings.channels.value = [{ id: 1, name: '测试渠道' }, { id: 2, name: '其他渠道' }]
  return { bindings, calls, messages, file, setPreview: fn => { preview = fn }, setConfirm: fn => { confirm = fn }, close: () => unmounted.forEach(fn => fn()) }
}

test('API wrapper uses preview and confirm routes with selected model IDs', async () => {
  const source = await readFile(new URL('../src/api/index.ts', import.meta.url), 'utf8')
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText
  const calls = []
  const instance = { interceptors: { request: { use() {} }, response: { use() {} } }, post: (...args) => { calls.push(args) } }
  const imports = { axios: { __esModule: true, default: { create: () => instance } }, './chatTest': {}, '../auth': { auth: {} } }
  const mod = { exports: {} }
  new Function('require', 'module', 'exports', code)(name => { if (!(name in imports)) throw new Error(`Unexpected import ${name}`); return imports[name] }, mod, mod.exports)
  mod.exports.channelApi.previewSyncModels(42)
  mod.exports.channelApi.confirmSyncModels(42, 'preview-x', [])
  assert.deepEqual(calls, [['/channels/42/sync/preview'], ['/channels/42/sync/confirm', { preview_id: 'preview-x', selected_model_ids: [] }]])
})

test('preview selects all, supports searching, and zero selection confirms cleanup', async () => {
  const c = await makeChannels()
  try {
    await c.bindings.syncModels(1)
    assert.deepEqual(c.calls[0], ['preview', 1])
    assert.deepEqual(c.bindings.selectedSyncModels.value, ['one', 'two'])
    assert.equal(c.bindings.syncPreview.value.stale_models[0].user_modified, true)
    c.bindings.syncSearch.value = 'TWO'
    assert.deepEqual(c.bindings.filteredSyncModels.value.map(m => m.id), ['two'])
    assert.deepEqual(c.bindings.selectedSyncModels.value, ['one', 'two'])
    c.bindings.selectedSyncModels.value = []
    assert.match(c.file, /当前未选择任何模型[\s\S]*仍会删除/)
    assert.match(c.file, /手动修改和配置也会丢失/)
    await c.bindings.confirmSyncModels()
    assert.deepEqual(c.calls.find(call => call[0] === 'confirm'), ['confirm', 1, 'preview-1', []])
    assert.equal(c.bindings.showSyncModal.value, false)
    assert.match(c.messages[0], /清理 1 个模型/)
  } finally { c.close() }
})

test('failed or malformed preview cannot be confirmed, retry starts fresh', async () => {
  const c = await makeChannels()
  try {
    c.setPreview(async () => { throw new Error('private upstream error') })
    await c.bindings.syncModels(1)
    assert.match(c.bindings.syncPreviewError.value, /无法安全同步或清理/)
    assert.equal(c.bindings.syncPreview.value, null)
    await c.bindings.confirmSyncModels()
    assert.equal(c.calls.filter(call => call[0] === 'confirm').length, 0)
    c.setPreview(async () => ({ data: { preview_id: 'x', models: [], stale_models: null } }))
    await c.bindings.loadSyncPreview()
    assert.equal(c.bindings.syncPreview.value, null)
    assert.match(c.bindings.syncPreviewError.value, /无法安全/)
    c.setPreview(async () => ({ data: fixture() }))
    await c.bindings.loadSyncPreview()
    assert.equal(c.bindings.syncPreviewError.value, '')
    assert.deepEqual(c.bindings.selectedSyncModels.value, ['one', 'two'])
  } finally { c.close() }
})

test('empty upstream models refuse cleanup even if stale models are present', async () => {
  const c = await makeChannels()
  try {
    for (const stale of [fixture().stale_models, []]) {
      c.setPreview(async () => ({ data: { preview_id: 'empty-preview', models: [], stale_models: stale } }))
      await c.bindings.syncModels(1)
      assert.equal(c.bindings.syncPreview.value, null)
      assert.deepEqual(c.bindings.selectedSyncModels.value, [])
      assert.match(c.bindings.syncPreviewError.value, /列表为空.*无法安全同步或清理/)
      await c.bindings.confirmSyncModels()
      assert.equal(c.calls.filter(call => call[0] === 'confirm').length, 0)
    }
    c.setPreview(async () => ({ data: fixture() }))
    await c.bindings.loadSyncPreview()
    c.bindings.selectedSyncModels.value = []
    await c.bindings.confirmSyncModels()
    assert.deepEqual(c.calls.find(call => call[0] === 'confirm'), ['confirm', 1, 'preview-1', []])
  } finally { c.close() }
})

test('cancel and switching channels discard stale preview responses', async () => {
  const c = await makeChannels()
  try {
    const first = deferred()
    c.setPreview(id => id === 1 ? first.promise : Promise.resolve({ data: { ...fixture(), preview_id: 'preview-2' } }))
    const old = c.bindings.syncModels(1)
    c.bindings.closeSyncModal()
    await c.bindings.syncModels(2)
    first.resolve({ data: fixture() })
    await old
    assert.equal(c.bindings.syncChannel.value.id, 2)
    assert.equal(c.bindings.syncPreview.value.preview_id, 'preview-2')
    assert.equal(c.calls.filter(call => call[0] === 'confirm').length, 0)
  } finally { c.close() }
})

test('expired and ambiguous confirmation failures force a new preview', async () => {
  const c = await makeChannels()
  try {
    await c.bindings.syncModels(1)
    c.setConfirm(async () => { throw { response: { status: 409 } } })
    await c.bindings.confirmSyncModels()
    assert.match(c.bindings.syncPreviewError.value, /已过期/)
    assert.equal(c.bindings.syncPreview.value, null)
    await c.bindings.confirmSyncModels()
    assert.equal(c.calls.filter(call => call[0] === 'confirm').length, 1)
    await c.bindings.loadSyncPreview()
    c.setConfirm(async () => { throw new Error('timeout') })
    await c.bindings.confirmSyncModels()
    assert.match(c.bindings.syncPreviewError.value, /结果未确认/)
    assert.equal(c.bindings.syncPreview.value, null)
  } finally { c.close() }
})

test('in-flight confirmation prevents duplicate submit and closing', async () => {
  const c = await makeChannels()
  try {
    await c.bindings.syncModels(1)
    const pending = deferred()
    c.setConfirm(() => pending.promise)
    const active = c.bindings.confirmSyncModels()
    c.bindings.closeSyncModal()
    assert.equal(c.bindings.showSyncModal.value, true)
    await c.bindings.confirmSyncModels()
    assert.equal(c.calls.filter(call => call[0] === 'confirm').length, 1)
    pending.resolve({ data: { count: 2, deleted_count: 1 } })
    await active
    await tick()
    assert.equal(c.bindings.showSyncModal.value, false)
  } finally { c.close() }
})
