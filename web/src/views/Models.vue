<script setup lang="ts">
import { onMounted, ref, h, computed, type VNode } from 'vue'
import {
  NButton, NCard, NDataTable, NSpace, NTag, NModal, NPopconfirm,
  NForm, NFormItem, NInput, NInputNumber, NSwitch, NDivider, NSelect,
  useMessage, NSpin, NIcon,
} from 'naive-ui'
import { HardwareChipSharp, CloudDownloadSharp, CloudUploadSharp } from '@vicons/ionicons5'
import { modelApi, channelApi } from '@/api'
import type { DataTableRowKey } from 'naive-ui'

const message = useMessage()
const loading = ref(true)
const models = ref<any[]>([])
const channels = ref<any[]>([])
const showModal = ref(false)
const editing = ref<any>(null)
const form = ref({
  display_name: '', context_window: 0, max_output_tokens: 0,
  supports_vision: false, supports_thinking: false, supports_tools: false,
  pricing_input: 0, pricing_output: 0,
  pricing_cache_read: 0, pricing_cache_write: 0, status: 'active',
  pricing_rules: '[]',
})

// 定价规则管理
const weekDayOptions = [
  { label: '周一', value: 'mon' },
  { label: '周二', value: 'tue' },
  { label: '周三', value: 'wed' },
  { label: '周四', value: 'thu' },
  { label: '周五', value: 'fri' },
  { label: '周六', value: 'sat' },
  { label: '周日', value: 'sun' },
]

function getParsedRules(): any[] {
  try {
    return JSON.parse(form.value.pricing_rules || '[]')
  } catch {
    return []
  }
}

function setParsedRules(rules: any[]) {
  form.value.pricing_rules = JSON.stringify(rules)
}

function addRule() {
  const rules = getParsedRules()
  const id = 'rule_' + Date.now()
  rules.push({
    id,
    type: 'time_range',
    enabled: true,
    name: '',
    days: ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'],
    start_time: '00:00',
    end_time: '08:00',
    pricing_input: 0,
    pricing_output: 0,
    pricing_cache_read: 0,
    pricing_cache_write: 0,
  })
  setParsedRules(rules)
}

function removeRule(index: number) {
  const rules = getParsedRules()
  rules.splice(index, 1)
  setParsedRules(rules)
}

function moveRuleUp(index: number) {
  if (index <= 0) return
  const rules = getParsedRules()
  ;[rules[index - 1], rules[index]] = [rules[index], rules[index - 1]]
  setParsedRules(rules)
}

function moveRuleDown(index: number) {
  const rules = getParsedRules()
  if (index >= rules.length - 1) return
  ;[rules[index], rules[index + 1]] = [rules[index + 1], rules[index]]
  setParsedRules(rules)
}

function updateRule(index: number, rule: any) {
  const rules = getParsedRules()
  rules[index] = rule
  setParsedRules(rules)
}

// 批量操作
const checkedRowKeys = ref<DataTableRowKey[]>([])
const batchDisabled = computed(() => checkedRowKeys.value.length === 0)
const statusFilter = ref<'all' | 'active' | 'inactive'>('all')
const channelFilter = ref<number | null>(null)

// 批量编辑对话框
const showBatchEditModal = ref(false)
const batchEditForm = ref({
  pricing_input: null as number | null,
  pricing_output: null as number | null,
  pricing_cache_read: null as number | null,
  pricing_cache_write: null as number | null,
  context_window: null as number | null,
  max_output_tokens: null as number | null,
  supports_vision: null as boolean | null,
  supports_thinking: null as boolean | null,
  supports_tools: null as boolean | null,
})
const batchEditTriState = (field: keyof typeof batchEditForm.value): boolean | null => batchEditForm.value[field] as any

function openBatchEdit() {
  batchEditForm.value = {
    pricing_input: null, pricing_output: null, pricing_cache_read: null, pricing_cache_write: null,
    context_window: null, max_output_tokens: null,
    supports_vision: null, supports_thinking: null, supports_tools: null,
  }
  showBatchEditModal.value = true
}

// 三态开关：未设置(null) → 是(true) → 否(false) → 未设置(null)
function toggleTriState(field: keyof typeof batchEditForm.value) {
  const v = batchEditForm.value[field] as boolean | null
  if (v === null) { batchEditForm.value[field] = true as any }
  else if (v === true) { batchEditForm.value[field] = false as any }
  else { batchEditForm.value[field] = null as any }
}

function triStateLabel(v: boolean | null) {
  if (v === null) return '未设置'
  return v ? '是' : '否'
}

async function submitBatchEdit() {
  const ids = checkedRowKeys.value.map(Number)
  if (!ids.length) return
  // 过滤掉 null 值
  const payload: any = {}
  for (const [k, v] of Object.entries(batchEditForm.value)) {
    if (v !== null) payload[k] = v
  }
  try {
    await modelApi.batch('batch_edit', ids, payload)
    message.success(`已编辑 ${ids.length} 个模型`)
    showBatchEditModal.value = false
    checkedRowKeys.value = []
    loadModels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '批量编辑失败')
  }
}

// 导出
async function exportModels() {
  try {
    const res = await modelApi.export()
    const blob = res.data as Blob
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `models-export-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
    message.success('导出成功')
  } catch (e: any) {
    message.error('导出失败')
  }
}

// 导入
const importing = ref(false)
const importFileRef = ref<HTMLInputElement | null>(null)
function triggerImport() {
  importFileRef.value?.click()
}
async function handleImportFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  importing.value = true
  try {
    const text = await file.text()
    const data = JSON.parse(text)
    const res = await modelApi.import(data)
    message.success(`导入成功，共处理 ${res.data?.imported ?? 0} 个模型`)
    loadModels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '导入失败，请检查文件格式')
  } finally {
    importing.value = false
    if (importFileRef.value) importFileRef.value.value = ''
  }
}

// 重置为默认（清除 user_modified）
async function batchReset() {
  const ids = checkedRowKeys.value.map(Number)
  if (!ids.length) return
  try {
    await modelApi.batch('reset', ids)
    message.success(`已重置 ${ids.length} 个模型，下次同步将更新为上游数据`)
    checkedRowKeys.value = []
    loadModels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '重置失败')
  }
}

const totalModels = computed(() => models.value.length)
const activeModels = computed(() => models.value.filter(m => m.status === 'active').length)
const inactiveModels = computed(() => totalModels.value - activeModels.value)
const filteredModels = computed(() => {
  let list = models.value
  if (statusFilter.value !== 'all') {
    list = list.filter((model) => model.status === statusFilter.value)
  }
  if (channelFilter.value !== null && channelFilter.value !== undefined) {
    list = list.filter((model) => model.channel_id === channelFilter.value)
  }
  return list
})

const columns = [
  { type: 'selection' as const },
  { title: '模型 ID', key: 'model_id', ellipsis: true, width: 180 },
  { title: '显示名称', key: 'display_name', width: 140 },
  {
    title: '渠道', key: 'channel_name', width: 110,
    render: (r: any) => {
      if (!r.channel_name) return '-'
      if (r.channel_status === 'inactive') {
        return h('div', { style: 'display:flex;align-items:center;gap:4px' }, [
          h(NTag, { size: 'tiny', type: 'error', bordered: false }, () => '禁用'),
          h('span', { style: 'color:#94a3b8;text-decoration:line-through' }, r.channel_name),
        ])
      }
      return r.channel_name
    },
  },
  { title: '上下文', key: 'context_window', width: 80, render: (r: any) => {
    const v = r.context_window
    if (!v || v === 0) return '-'
    if (v >= 1000000) return (v / 1000000).toFixed(v % 1000000 === 0 ? 0 : 1) + 'M'
    if (v >= 1000) return (v / 1000).toFixed(0) + 'K'
    return String(v)
  }},
  { title: '最大输出', key: 'max_output_tokens', width: 80, render: (r: any) => {
    const v = r.max_output_tokens
    if (!v || v === 0) return '-'
    if (v >= 1000000) return (v / 1000000).toFixed(v % 1000000 === 0 ? 0 : 1) + 'M'
    if (v >= 1000) return (v / 1000).toFixed(0) + 'K'
    return String(v)
  }},
  {
    title: '特性', key: 'features', width: 60,
    render: (r: any) => {
      const tags: VNode[] = []
      if (r.supports_vision) tags.push(h(NTag, { size: 'tiny', type: 'info', bordered: false }, () => '视觉'))
      if (r.supports_thinking) tags.push(h(NTag, { size: 'tiny', type: 'warning', bordered: false }, () => '思考'))
      if (r.supports_tools) tags.push(h(NTag, { size: 'tiny', type: 'success', bordered: false }, () => '工具'))
      return tags.length
        ? h('div', { style: 'display:flex;flex-direction:column;gap:2px' }, tags)
        : '-'
    },
  },
  {
    title: '价格', key: 'pricing', width: 140,
    render: (r: any) => {
      const fmt = (v: number) => `$${(v || 0).toFixed(6)}/M`
      const hasRules = r.pricing_rules && r.pricing_rules !== '[]'
      return h('div', { style: 'line-height:1.7;font-size:12px' }, [
        h('div', {}, [
          `输入 ${fmt(r.pricing_input)}`,
          hasRules ? h(NTag, { size: 'tiny', type: 'info', bordered: false, style: 'margin-left:6px' }, () => '多段定价') : null,
        ]),
        h('div', {}, `输出 ${fmt(r.pricing_output)}`),
        h('div', { style: 'color:#888' }, `缓存读 ${fmt(r.pricing_cache_read)}`),
        h('div', { style: 'color:#888' }, `缓存写 ${fmt(r.pricing_cache_write)}`),
      ])
    },
  },
  {
    title: '状态', key: 'status', width: 70,
    render: (r: any) =>
      h(NTag, { type: r.status === 'active' ? 'success' : 'default', size: 'small', bordered: false }, () =>
        r.status === 'active' ? '启用' : '禁用'
      ),
  },
  {
    title: '操作', key: 'actions', width: 160,
    render: (r: any) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => editModel(r) }, () => '编辑'),
        h(NButton, { size: 'tiny', onClick: () => toggleModel(r) }, () =>
          r.status === 'active' ? '禁用' : '启用'
        ),
        h(NPopconfirm, {
          onPositiveClick: () => deleteModel(r.id),
        }, {
          default: () => '确认删除该模型？',
          trigger: () => h(NButton, { size: 'tiny', type: 'error' }, () => '删除'),
        }),
      ]),
  },
]

onMounted(async () => {
  await Promise.all([loadModels(), loadChannels()])
})

async function loadModels() {
  loading.value = true
  try {
    const res = await modelApi.list()
    models.value = res.data
  } finally {
    loading.value = false
  }
}

async function loadChannels() {
  try {
    const res = await channelApi.list()
    channels.value = res.data || []
  } catch {
    // 静默失败
  }
}

function editModel(m: any) {
  editing.value = m
  form.value = {
    display_name: m.display_name,
    context_window: m.context_window || 0,
    max_output_tokens: m.max_output_tokens || 0,
    supports_vision: !!m.supports_vision,
    supports_thinking: !!m.supports_thinking,
    supports_tools: !!m.supports_tools,
    pricing_input: m.pricing_input,
    pricing_output: m.pricing_output,
    pricing_cache_read: m.pricing_cache_read || 0,
    pricing_cache_write: m.pricing_cache_write || 0,
    status: m.status,
    pricing_rules: m.pricing_rules || '[]',
  }
  showModal.value = true
}

async function saveModel() {
  try {
    await modelApi.update(editing.value.id, form.value)
    message.success('模型已更新')
    showModal.value = false
    loadModels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '更新失败')
  }
}

async function toggleModel(m: any) {
  try {
    await modelApi.toggle(m.id)
    loadModels()
  } catch (e: any) {
    message.error('操作失败')
  }
}

async function deleteModel(id: number) {
  try {
    await modelApi.delete(id)
    message.success('模型已删除')
    loadModels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '删除失败')
  }
}

async function batchAction(action: string) {
  const ids = checkedRowKeys.value.map(Number)
  if (!ids.length) return
  try {
    await modelApi.batch(action, ids)
    const labels: Record<string, string> = { enable: '已启用', disable: '已禁用', delete: '已删除' }
    message.success(`${labels[action]} ${ids.length} 个模型`)
    checkedRowKeys.value = []
    loadModels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '操作失败')
  }
}
</script>

<template>
  <NSpin :show="loading" style="display:flex;flex-direction:column;flex:1;min-height:0">
    <div style="display:flex;flex-direction:column;flex:1;min-height:0;gap:16px;padding-bottom:16px">
      <div class="page-header">
        <div>
          <h2 style="margin:0">
            <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><HardwareChipSharp /></NIcon>
            模型管理
          </h2>
          <p style="color:#94a3b8;font-size:13px;margin-top:4px">
            共 <b style="color:#e2e8f0">{{ totalModels }}</b> 个模型，
            <span style="color:#22c55e">{{ activeModels }} 启用</span>，
            <span style="color:#94a3b8">{{ inactiveModels }} 禁用</span>
          </p>
        </div>
        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
          <NButton size="small" quaternary @click="exportModels" :loading="false">
            <template #icon><NIcon size="16"><CloudDownloadSharp /></NIcon></template>
            导出
          </NButton>
          <input ref="importFileRef" type="file" accept=".json" style="display:none" @change="handleImportFile" />
          <NButton size="small" quaternary @click="triggerImport" :loading="importing">
            <template #icon><NIcon size="16"><CloudUploadSharp /></NIcon></template>
            导入
          </NButton>
          <span style="color:#94a3b8;font-size:13px">状态过滤</span>
          <NSelect
            v-model:value="statusFilter"
            size="small"
            style="width:140px"
            :options="[
              { label: '全部模型', value: 'all' },
              { label: '仅启用', value: 'active' },
              { label: '仅禁用', value: 'inactive' },
            ]"
          />
          <span style="color:#94a3b8;font-size:13px;margin-left:8px">渠道过滤</span>
          <NSelect
            v-model:value="channelFilter"
            size="small"
            style="width:160px"
            clearable
            placeholder="全部渠道"
            :options="channels.map((ch: any) => ({ label: ch.name, value: ch.id }))"
          />
        </div>
      </div>

      <!-- 批量操作栏 -->
      <div v-if="checkedRowKeys.length > 0" class="batch-bar">
        <span style="color:#e2e8f0">已选 <b>{{ checkedRowKeys.length }}</b> 个模型</span>
        <NSpace>
          <NButton size="small" type="success" @click="batchAction('enable')">批量启用</NButton>
          <NButton size="small" @click="batchAction('disable')">批量禁用</NButton>
          <NButton size="small" type="info" @click="openBatchEdit">批量编辑</NButton>
          <NPopconfirm @positive-click="batchReset">
            <template #trigger>
              <NButton size="small" type="warning">重置为默认</NButton>
            </template>
            将清除 {{ checkedRowKeys.length }} 个模型的"手动编辑"标记，下次同步时上游数据会覆盖这些模型。确认？
          </NPopconfirm>
          <NPopconfirm @positive-click="batchAction('delete')">
            <template #trigger>
              <NButton size="small" type="error">批量删除</NButton>
            </template>
            确认删除选中的 {{ checkedRowKeys.length }} 个模型？
          </NPopconfirm>
          <NButton size="small" quaternary @click="checkedRowKeys = []">取消选择</NButton>
        </NSpace>
      </div>

      <NCard style="flex:1;min-height:0">
        <NDataTable
          :columns="columns"
          :data="filteredModels"
          :bordered="false"
          :row-key="(row: any) => row.id"
          v-model:checked-row-keys="checkedRowKeys"
          :scroll-x="1200"
          size="small"
        />
      </NCard>

      <NModal v-model:show="showModal" title="编辑模型" preset="card" style="width:560px">
        <NForm :model="form" label-placement="left" label-width="120">
          <NFormItem label="显示名称">
            <NInput v-model:value="form.display_name" placeholder="模型显示名称" />
          </NFormItem>

          <NFormItem label="上下文窗口">
            <NInputNumber v-model:value="form.context_window" :min="0" :step="1024" style="width:100%" placeholder="0=使用默认" />
          </NFormItem>
          <NFormItem label="最大输出 Tokens">
            <NInputNumber v-model:value="form.max_output_tokens" :min="0" :step="1024" style="width:100%" placeholder="0=使用默认" />
          </NFormItem>

          <NFormItem label="支持视觉">
            <NSwitch v-model:value="form.supports_vision" />
          </NFormItem>
          <NFormItem label="支持思考">
            <NSwitch v-model:value="form.supports_thinking" />
          </NFormItem>
          <NFormItem label="支持工具">
            <NSwitch v-model:value="form.supports_tools" />
          </NFormItem>

          <NDivider />

          <NFormItem label="输入价格 ($/1M)">
            <NInputNumber v-model:value="form.pricing_input" :precision="6" :step="0.000001" style="width:100%" />
          </NFormItem>
          <NFormItem label="输出价格 ($/1M)">
            <NInputNumber v-model:value="form.pricing_output" :precision="6" :step="0.000001" style="width:100%" />
          </NFormItem>
          <NFormItem label="缓存读取 ($/1M)">
            <NInputNumber v-model:value="form.pricing_cache_read" :precision="6" :step="0.000001" style="width:100%" />
          </NFormItem>
          <NFormItem label="缓存写入 ($/1M)">
            <NInputNumber v-model:value="form.pricing_cache_write" :precision="6" :step="0.000001" style="width:100%" />
          </NFormItem>

          <NDivider />
          <NFormItem label="高级定价" style="align-items:flex-start">
            <div style="width:100%">
              <div v-if="getParsedRules().length === 0" style="color:#94a3b8;font-size:13px;padding:8px 0">
                未设置定价规则，将使用固定定价
              </div>
              <div v-for="(rule, idx) in getParsedRules()" :key="rule.id" style="background:rgba(30,41,59,0.5);border:1px solid rgba(102,126,234,0.2);border-radius:8px;padding:12px;margin-bottom:8px">
                <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
                  <div style="display:flex;align-items:center;gap:8px">
                    <NSwitch size="small" :value="rule.enabled" @update:value="v => { rule.enabled = v; updateRule(idx, {...rule}) }" />
                    <NInput size="small" style="width:160px" :value="rule.name" placeholder="规则名称" @update:value="v => { rule.name = v; updateRule(idx, {...rule}) }" />
                    <NSelect size="small" style="width:120px" :value="rule.type" :options="[{label:'时间段',value:'time_range'},{label:'Token阶梯',value:'token_tier'}]" @update:value="v => { rule.type = v; updateRule(idx, {...rule}) }" />
                  </div>
                  <div style="display:flex;gap:4px">
                    <NButton size="tiny" quaternary circle :disabled="idx === 0" @click="moveRuleUp(idx)" title="上移">▲</NButton>
                    <NButton size="tiny" quaternary circle :disabled="idx >= getParsedRules().length - 1" @click="moveRuleDown(idx)" title="下移">▼</NButton>
                    <NButton size="tiny" quaternary circle type="error" @click="removeRule(idx)" title="删除">✕</NButton>
                  </div>
                </div>

                <!-- 时间段条件 -->
                <div v-if="rule.type === 'time_range'" style="display:flex;gap:8px;margin-bottom:8px;flex-wrap:wrap">
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">时间</span>
                    <NInput size="small" style="width:68px" :value="rule.start_time" placeholder="00:00" @update:value="v => { rule.start_time = v; updateRule(idx, {...rule}) }" />
                    <span style="color:#94a3b8">—</span>
                    <NInput size="small" style="width:68px" :value="rule.end_time" placeholder="08:00" @update:value="v => { rule.end_time = v; updateRule(idx, {...rule}) }" />
                  </div>
                  <div style="display:flex;align-items:center;gap:4px;flex:1">
                    <span style="color:#94a3b8;font-size:12px">星期</span>
                    <NSelect size="small" multiple style="min-width:200px;flex:1" :value="rule.days || []" :options="weekDayOptions" @update:value="v => { rule.days = v; updateRule(idx, {...rule}) }" />
                  </div>
                </div>

                <!-- Token 阶梯条件 -->
                <div v-if="rule.type === 'token_tier'" style="display:flex;gap:12px;margin-bottom:8px">
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">Prompt ≤</span>
                    <NInputNumber size="small" style="width:120px" :min="0" :step="1024" placeholder="0=不限" :value="rule.prompt_max_tokens || 0" @update:value="v => { rule.prompt_max_tokens = v; updateRule(idx, {...rule}) }" />
                  </div>
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">Context ≤</span>
                    <NInputNumber size="small" style="width:120px" :min="0" :step="1024" placeholder="0=不限" :value="rule.context_max_tokens || 0" @update:value="v => { rule.context_max_tokens = v; updateRule(idx, {...rule}) }" />
                  </div>
                </div>

                <!-- 规则定价 -->
                <div style="display:flex;gap:8px;flex-wrap:wrap">
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">输入</span>
                    <NInputNumber size="small" style="width:90px" :precision="6" :step="0.000001" :value="rule.pricing_input" @update:value="v => { rule.pricing_input = v; updateRule(idx, {...rule}) }" />
                  </div>
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">输出</span>
                    <NInputNumber size="small" style="width:90px" :precision="6" :step="0.000001" :value="rule.pricing_output" @update:value="v => { rule.pricing_output = v; updateRule(idx, {...rule}) }" />
                  </div>
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">缓存读</span>
                    <NInputNumber size="small" style="width:90px" :precision="6" :step="0.000001" :value="rule.pricing_cache_read" @update:value="v => { rule.pricing_cache_read = v; updateRule(idx, {...rule}) }" />
                  </div>
                  <div style="display:flex;align-items:center;gap:4px">
                    <span style="color:#94a3b8;font-size:12px">缓存写</span>
                    <NInputNumber size="small" style="width:90px" :precision="6" :step="0.000001" :value="rule.pricing_cache_write" @update:value="v => { rule.pricing_cache_write = v; updateRule(idx, {...rule}) }" />
                  </div>
                </div>
              </div>
              <NButton size="small" @click="addRule" style="margin-top:4px">+ 添加定价规则</NButton>
            </div>
          </NFormItem>
        </NForm>
        <template #footer>
          <NSpace justify="end">
            <NButton @click="showModal = false">取消</NButton>
            <NButton type="primary" @click="saveModel">保存</NButton>
          </NSpace>
        </template>
      </NModal>
      <!-- 批量编辑对话框 -->
      <NModal v-model:show="showBatchEditModal" title="批量编辑" preset="card" style="width:500px">
        <div style="color:#94a3b8;font-size:13px;margin-bottom:16px">已选 <b style="color:#e2e8f0">{{ checkedRowKeys.length }}</b> 个模型，仅更新设置的字段，留空表示不修改。</div>
        <NForm label-placement="left" label-width="120">
          <NFormItem label="输入价格 ($/1M)">
            <NInputNumber v-model:value="batchEditForm.pricing_input" :precision="6" :step="0.000001" style="width:100%" placeholder="留空=不修改" :clearable="true" />
          </NFormItem>
          <NFormItem label="输出价格 ($/1M)">
            <NInputNumber v-model:value="batchEditForm.pricing_output" :precision="6" :step="0.000001" style="width:100%" placeholder="留空=不修改" :clearable="true" />
          </NFormItem>
          <NFormItem label="缓存读取 ($/1M)">
            <NInputNumber v-model:value="batchEditForm.pricing_cache_read" :precision="6" :step="0.000001" style="width:100%" placeholder="留空=不修改" :clearable="true" />
          </NFormItem>
          <NFormItem label="缓存写入 ($/1M)">
            <NInputNumber v-model:value="batchEditForm.pricing_cache_write" :precision="6" :step="0.000001" style="width:100%" placeholder="留空=不修改" :clearable="true" />
          </NFormItem>
          <NFormItem label="上下文窗口">
            <NInputNumber v-model:value="batchEditForm.context_window" :min="0" :step="1024" style="width:100%" placeholder="留空=不修改" :clearable="true" />
          </NFormItem>
          <NFormItem label="最大输出">
            <NInputNumber v-model:value="batchEditForm.max_output_tokens" :min="0" :step="1024" style="width:100%" placeholder="留空=不修改" :clearable="true" />
          </NFormItem>
          <NFormItem label="支持视觉">
            <NButton size="small" :type="batchEditForm.supports_vision === null ? 'default' : 'primary'" @click="toggleTriState('supports_vision')">{{ triStateLabel(batchEditForm.supports_vision) }}</NButton>
          </NFormItem>
          <NFormItem label="支持思考">
            <NButton size="small" :type="batchEditForm.supports_thinking === null ? 'default' : 'primary'" @click="toggleTriState('supports_thinking')">{{ triStateLabel(batchEditForm.supports_thinking) }}</NButton>
          </NFormItem>
          <NFormItem label="支持工具">
            <NButton size="small" :type="batchEditForm.supports_tools === null ? 'default' : 'primary'" @click="toggleTriState('supports_tools')">{{ triStateLabel(batchEditForm.supports_tools) }}</NButton>
          </NFormItem>
        </NForm>
        <template #footer>
          <NSpace justify="end">
            <NButton @click="showBatchEditModal = false">取消</NButton>
            <NButton type="primary" @click="submitBatchEdit">保存</NButton>
          </NSpace>
        </template>
      </NModal>
    </div>
  </NSpin>
</template>

<style scoped>
/* page-header styles are now global in App.vue */
.batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: rgba(102, 126, 234, 0.1);
  border: 1px solid rgba(102, 126, 234, 0.3);
  border-radius: 12px;
}
</style>
