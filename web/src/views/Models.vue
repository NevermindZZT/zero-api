<script setup lang="ts">
import { onMounted, ref, h, computed, type VNode } from 'vue'
import {
  NButton, NCard, NDataTable, NSpace, NTag, NModal, NPopconfirm,
  NForm, NFormItem, NInput, NInputNumber, NSwitch, NDivider, NSelect,
  useMessage, NSpin, NIcon,
} from 'naive-ui'
import { HardwareChipSharp } from '@vicons/ionicons5'
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
})

// 批量操作
const checkedRowKeys = ref<DataTableRowKey[]>([])
const batchDisabled = computed(() => checkedRowKeys.value.length === 0)
const statusFilter = ref<'all' | 'active' | 'inactive'>('all')
const channelFilter = ref<number | null>(null)

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
  { title: '渠道', key: 'channel_name', width: 100 },
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
    title: '价格', key: 'pricing', width: 120,
    render: (r: any) => {
      const fmt = (v: number) => `$${(v || 0).toFixed(4)}/M`
      return h('div', { style: 'line-height:1.7;font-size:12px' }, [
        h('div', {}, `输入 ${fmt(r.pricing_input)}`),
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
    title: '操作', key: 'actions', width: 160, fixed: 'right' as const,
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
            <NInputNumber v-model:value="form.pricing_input" :precision="4" :step="0.01" style="width:100%" />
          </NFormItem>
          <NFormItem label="输出价格 ($/1M)">
            <NInputNumber v-model:value="form.pricing_output" :precision="4" :step="0.01" style="width:100%" />
          </NFormItem>
          <NFormItem label="缓存读取 ($/1M)">
            <NInputNumber v-model:value="form.pricing_cache_read" :precision="4" :step="0.01" style="width:100%" />
          </NFormItem>
          <NFormItem label="缓存写入 ($/1M)">
            <NInputNumber v-model:value="form.pricing_cache_write" :precision="4" :step="0.01" style="width:100%" />
          </NFormItem>
        </NForm>
        <template #footer>
          <NSpace justify="end">
            <NButton @click="showModal = false">取消</NButton>
            <NButton type="primary" @click="saveModel">保存</NButton>
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
