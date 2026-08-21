<script setup lang="ts">
import { h, onMounted, ref, computed } from 'vue'
import {
  NButton, NCard, NDataTable, NModal, NForm, NFormItem,
  NInput, NInputNumber, NSelect, NSpace, NTag, NPopconfirm, NMessageProvider,
  useMessage, NSpin, NIcon, NSwitch, NDescriptions, NDescriptionsItem, NPopover, NTooltip, NDivider,
} from 'naive-ui'
import { GlobeSharp, RefreshSharp, WalletSharp } from '@vicons/ionicons5'
import api from '@/api'
import { channelApi, balanceApi } from '@/api'

const message = useMessage()
const loading = ref(true)
const channels = ref<any[]>([])
const showModal = ref(false)
const editing = ref<any>(null)
const syncingId = ref<number | null>(null)
const refreshingId = ref<number | null>(null)
const refreshingAll = ref(false)
const balances = ref<Record<number, any>>({}) // channel_id -> balance
const showBalanceModal = ref(false)
const balanceDetail = ref<any>(null)
const balanceProviders = ref<{ name: string }[]>([])
// 手动余额维护状态
const manualBalanceVisible = ref(false)
const manualBalanceVal = ref(0)
const manualCurrency = ref('USD')
const savingManualBalance = ref(false)
const form = ref({ name: '', type: 'openai', protocols: ['openai'], base_url: '', api_key: '', status: 'active', priority: 99, use_proxy: false, failover_enabled: true, test_model: '', balance_api: 'auto' })

const channelTypes = [
  { label: 'OpenAI 兼容 (chat/completions)', value: 'openai' },
  { label: 'Anthropic (messages)', value: 'anthropic' },
  { label: 'Google Gemini (generateContent)', value: 'gemini' },
  { label: 'OpenAI Responses (responses)', value: 'responses' },
]

const protocolOptions = [
  { label: 'OpenAI Chat', value: 'openai' },
  { label: 'OpenAI Responses', value: 'responses' },
  { label: 'Anthropic Messages', value: 'anthropic' },
  { label: 'Google Gemini', value: 'gemini' },
]

// 余额查询方式选项（auto=按域名推断，其余为具体适配器）
const balanceAPIOptions = computed(() => {
  const options = [
    { label: '自动检测 (auto)', value: 'auto' },
    { label: '不查询 (none)', value: 'none' },
  ]
  for (const p of balanceProviders.value) {
    if (p.name === 'auto' || p.name === 'none') continue
    options.push({ label: p.name, value: p.name })
  }
  return options
})

// 规范化 Base URL（与后端 normalizeBaseURL 保持一致）
function normalizeBaseURL(url: string) {
  if (!url) return ''
  let u = url.trim().replace(/\/+$/, '')
  if (u.endsWith('/v1beta')) u = u.slice(0, -7)
  else if (u.endsWith('/v1')) u = u.slice(0, -3)
  return u
}

function defaultProtocols(type: string): string[] {
  return type ? [type] : ['openai']
}

// 根据接口类型 + Base URL 计算完整的上游请求 URL
const fullURL = computed(() => {
  const base = normalizeBaseURL(form.value.base_url)
  if (!base) return ''
  switch (form.value.type) {
    case 'anthropic':
      return `${base}/v1/messages`
    case 'gemini':
      // 实际请求会拼接模型名，流式使用 streamGenerateContent
      return `${base}/v1beta/models/{model}:generateContent`
    case 'responses':
      return `${base}/v1/responses`
    default:
      return `${base}/v1/chat/completions`
  }
})

const columns = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '名称', key: 'name' },
  {
    title: '类型',
    key: 'type',
    render: (r: any) => {
      const t = channelTypes.find((t) => t.value === r.type)
      return t?.label || r.type
    },
  },
  { title: 'Base URL', key: 'base_url', ellipsis: true },
  {
    title: '余额/订阅',
    key: 'balance',
    width: 140,
    render: (r: any) => renderBalance(r),
  },
  { title: '优先级', key: 'priority', width: 90, sorter: 'default' as const, defaultSortOrder: 'ascend' as const },
  {
    title: '状态',
    key: 'status',
    render: (r: any) => (
      r.status === 'active'
        ? h(NTag, { type: 'success', size: 'small' }, () => '启用')
        : h(NTag, { type: 'default', size: 'small' }, () => '禁用')
    ),
  },
  {
    title: '代理',
    key: 'use_proxy',
    width: 80,
    render: (r: any) => (
      r.use_proxy
        ? h(NTag, { type: 'warning', size: 'small' }, () => '代理')
        : h(NTag, { type: 'default', size: 'small' }, () => '直连')
    ),
  },
  {
    title: '熔断',
    key: 'failover_enabled',
    width: 80,
    render: (r: any) => (
      r.failover_enabled === false
        ? h(NTag, { type: 'default', size: 'small' }, () => '关闭')
        : h(NTag, { type: 'success', size: 'small' }, () => '开启')
    ),
  },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    render: (r: any) =>
      h('div', { style: 'display:flex;gap:4px;flex-wrap:nowrap;align-items:center' }, [
        h(NButton, {
          size: 'tiny', onClick: () => editChannel(r),
        }, () => '编辑'),
        h(NButton, {
          size: 'tiny',
          onClick: () => toggleChannel(r),
          type: r.status === 'active' ? 'warning' : 'success',
        }, () => r.status === 'active' ? '禁用' : '启用'),
        h(NButton, {
          size: 'tiny',
          loading: syncingId.value === r.id,
          disabled: syncingId.value !== null,
          onClick: () => syncModels(r.id),
        }, () => '同步'),
        h(NPopconfirm, {
          onPositiveClick: () => deleteChannel(r.id),
        }, {
          default: () => '确认删除？',
          trigger: () => h(NButton, { size: 'tiny', type: 'error' }, () => '删除'),
        }),
      ]),
  },
]

onMounted(() => {
  loadChannels()
  loadBalances()
  loadBalanceProviders()
})

// 加载渠道列表
async function loadChannels() {
  loading.value = true
  try {
    const res = await channelApi.list()
    channels.value = res.data
  } finally {
    loading.value = false
  }
}

// 加载余额列表
async function loadBalances() {
  try {
    const res = await balanceApi.list()
    const map: Record<number, any> = {}
    for (const b of res.data || []) map[b.channel_id] = b
    balances.value = map
  } catch { /* 余额查询失败不影响渠道列表 */ }
}

// 加载可用适配器
async function loadBalanceProviders() {
  try {
    const res = await balanceApi.providers()
    balanceProviders.value = res.data || []
  } catch { /* ignore */ }
}

// 刷新单个渠道余额
async function refreshBalance(id: number) {
  refreshingId.value = id
  try {
    const res = await balanceApi.refresh(id)
    balances.value = { ...balances.value, [id]: res.data }
    message.success('余额已刷新')
  } catch (e: any) {
    message.error(e.response?.data?.error || '刷新失败')
  } finally {
    refreshingId.value = null
  }
}

// 批量刷新所有余额
async function refreshAllBalances() {
  refreshingAll.value = true
  try {
    const res = await balanceApi.refreshAll()
    const map: Record<number, any> = {}
    for (const b of res.data || []) map[b.channel_id] = b
    balances.value = { ...balances.value, ...map }
    message.success('已批量刷新')
  } catch (e: any) {
    message.error(e.response?.data?.error || '批量刷新失败')
  } finally {
    refreshingAll.value = false
  }
}

// 打开余额详情
function openBalanceDetail(ch: any) {
  const b = balances.value[ch.id]
  if (!b) {
    message.info('该渠道暂无余额记录，请先刷新')
    return
  }
  balanceDetail.value = b
  manualBalanceVisible.value = false
  manualBalanceVal.value = b.balance || 0
  manualCurrency.value = b.currency || 'USD'
  showBalanceModal.value = true
}

// 保存手动余额
async function saveManualBalance() {
  savingManualBalance.value = true
  try {
    const res = await balanceApi.setManual(balanceDetail.value.channel_id, manualBalanceVal.value, manualCurrency.value)
    balances.value = { ...balances.value, [balanceDetail.value.channel_id]: res.data }
    balanceDetail.value = res.data
    manualBalanceVisible.value = false
    message.success('余额已手动更新')
  } catch (e: any) {
    message.error(e.response?.data?.error || '保存失败')
  } finally {
    savingManualBalance.value = false
  }
}

// 格式化金额
function formatCurrency(v: number | undefined, cur: string | undefined) {
  if (v === undefined || v === null) return '-'
  const symbol = cur === 'CNY' ? '¥' : cur === 'USD' ? '$' : ''
  return `${symbol}${v.toFixed(4)}`
}

// 渲染余额单元格
function renderBalance(r: any) {
  const b = balances.value[r.id]
  if (!b) {
    return h('div', { style: 'display:flex;align-items:center;gap:6px' }, [
      h('span', { style: 'color:#64748b;font-size:12px' }, '未查询'),
      h(NButton, { size: 'tiny', quaternary: true, loading: refreshingId.value === r.id, disabled: refreshingId.value !== null, onClick: () => refreshBalance(r.id) }, { icon: () => h(NIcon, null, { default: () => h(RefreshSharp) }) }),
    ])
  }

  let content: any
  const color = b.status === 'error' ? '#f87171' : b.status === 'warning' ? '#fbbf24' : b.status === 'unsupported' ? '#94a3b8' : '#22c55e'
  if (b.status === 'error') {
    content = h(NTooltip, { trigger: 'hover' }, {
      trigger: () => h('span', { style: 'color:#f87171;font-size:12px' }, '查询失败'),
      default: () => b.error_msg || '查询失败',
    })
  } else if (b.status === 'unsupported') {
    content = h(NTooltip, { trigger: 'hover' }, {
      trigger: () => h('span', { style: 'color:#94a3b8;font-size:12px' }, '手动维护'),
      default: () => '该供应商无公开余额接口，点击查看详情可手动维护余额',
    })
  } else if (b.status === 'manual') {
    content = h('span', { style: 'font-size:12px;color:#94a3b8' }, `${formatCurrency(b.balance, b.currency)} (手动)`)
  } else if (b.plan_type) {
    content = h('span', { style: 'font-size:12px' }, `${b.plan_type} · ${b.plan_status || ''}`)
  } else if (b.balance !== undefined && b.balance !== null) {
    content = h('span', { style: 'font-size:12px' }, formatCurrency(b.balance, b.currency))
  } else {
    content = h('span', { style: 'color:#64748b;font-size:12px' }, '—')
  }

  return h('div', { style: 'display:flex;align-items:center;gap:6px;cursor:pointer' }, [
    h('div', { onClick: () => openBalanceDetail(r) }, [content]),
    h(NButton, { size: 'tiny', quaternary: true, loading: refreshingId.value === r.id, disabled: refreshingId.value !== null, onClick: () => refreshBalance(r.id) }, { icon: () => h(NIcon, null, { default: () => h(RefreshSharp) }) }),
  ])
}

function openCreate() {
  editing.value = null
  form.value = { name: '', type: 'openai', protocols: ['openai'], base_url: '', api_key: '', status: 'active', priority: 99, use_proxy: false, failover_enabled: true, test_model: '', balance_api: 'auto' }
  showModal.value = true
}

function editChannel(ch: any) {
  editing.value = ch
  form.value = { ...ch, protocols: ch.protocols?.length ? ch.protocols : defaultProtocols(ch.type) }
  showModal.value = true
}

async function save() {
  try {
    if (editing.value) {
      await channelApi.update(editing.value.id, form.value)
      message.success('渠道已更新')
    } else {
      await channelApi.create(form.value)
      message.success('渠道已创建')
    }
    showModal.value = false
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '操作失败')
  }
}

async function deleteChannel(id: number) {
  try {
    await channelApi.delete(id)
    message.success('渠道已删除')
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '删除失败')
  }
}

async function syncModels(id: number) {
  try {
    syncingId.value = id
    message.info('正在同步模型列表...')
    const res = await channelApi.syncModels(id)
    message.success(`同步完成，共 ${res.data.count} 个模型`)
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '同步失败')
  } finally {
    syncingId.value = null
  }
}

async function toggleChannel(ch: any) {
  try {
    const label = ch.status === 'active' ? '禁用' : '启用'
    await api.post(`/channels/${ch.id}/toggle`)
    message.success(`渠道已${label}`)
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '操作失败')
  }
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
        <div class="page-header" style="display:flex;justify-content:space-between;align-items:center">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><GlobeSharp /></NIcon>
          渠道管理
        </h2>
        <NSpace>
          <NButton size="small" :loading="refreshingAll" @click="refreshAllBalances">
            <template #icon><NIcon size="14"><RefreshSharp /></NIcon></template>
            刷新全部余额
          </NButton>
          <NButton type="primary" @click="openCreate">添加渠道</NButton>
        </NSpace>
      </div>
      <NCard style="width:100%">
        <NDataTable :columns="columns" :data="channels" :bordered="false" :scroll-x="1000" />
      </NCard>

      <NModal v-model:show="showModal" title="渠道" preset="card" style="width:520px">
        <NForm :model="form" label-placement="left" label-width="80">
          <NFormItem label="名称">
            <NInput v-model:value="form.name" placeholder="例如: DeepSeek" />
          </NFormItem>
          <NFormItem label="类型" label-description="作为默认接口；模型支持的接口优先">
            <NSelect v-model:value="form.type" :options="channelTypes" />
          </NFormItem>
          <NFormItem label="支持接口" label-description="模型和请求协议都匹配时原样透传">
            <NSelect v-model:value="form.protocols" multiple :options="protocolOptions" placeholder="留空则使用默认接口" />
          </NFormItem>
          <NFormItem label="Base URL">
            <NInput v-model:value="form.base_url" placeholder="https://api.deepseek.com" />
          </NFormItem>
          <NFormItem v-if="fullURL" label="完整 URL" label-description="将请求到的上游地址">
            <NInput :value="fullURL" readonly :bordered="false" style="color:#22c55e;font-size:13px" />
          </NFormItem>
          <NFormItem label="API Key">
            <NInput
              v-model:value="form.api_key"
              type="password"
              show-password-on="click"
              placeholder="可选，留空则使用请求中的 Authorization"
            />
          </NFormItem>
          <NFormItem label="优先级" label-description="数值越小优先级越高，0 最高">
            <NInputNumber v-model:value="form.priority" :min="0" :max="999" style="width:100%" />
          </NFormItem>
          <NFormItem label="出站代理">
            <NSwitch v-model:value="form.use_proxy" />
            <span style="color:#94a3b8;font-size:13px;margin-left:8px">
              启用后此渠道的请求通过全局代理转发
            </span>
          </NFormItem>
          <NFormItem label="熔断回落">
            <NSwitch v-model:value="form.failover_enabled" />
            <span style="color:#94a3b8;font-size:13px;margin-left:8px">
              启用后，渠道失败会触发熔断，自动回落到其他渠道
            </span>
          </NFormItem>
          <NFormItem label="测试模型" label-description="熔断探测用模型，留空则使用该渠道最高优先级的活跃模型">
            <NInput v-model:value="form.test_model" placeholder="留空自动选取" />
          </NFormItem>
          <NFormItem label="余额查询" label-description="自动=按域名识别供应商，或手动指定">
            <NSelect v-model:value="form.balance_api" :options="balanceAPIOptions" />
          </NFormItem>
        </NForm>
        <template #footer>
          <NSpace justify="end">
            <NButton @click="showModal = false">取消</NButton>
            <NButton type="primary" @click="save">保存</NButton>
          </NSpace>
        </template>
      </NModal>

      <!-- 余额详情弹窗 -->
      <NModal v-model:show="showBalanceModal" title="余额/订阅详情" preset="card" style="width:520px;max-width:calc(100vw - 32px);">
        <template v-if="balanceDetail">
          <NDescriptions :column="2" bordered size="small" label-placement="left">
            <NDescriptionsItem label="状态">
              <NTag :type="balanceDetail.status === 'error' ? 'error' : balanceDetail.status === 'warning' ? 'warning' : balanceDetail.status === 'unsupported' ? 'default' : 'success'" size="small">
                {{ balanceDetail.status === 'manual' ? '手动维护' : balanceDetail.status === 'unsupported' ? '不支持查询' : balanceDetail.status }}
              </NTag>
            </NDescriptionsItem>
            <NDescriptionsItem label="适配器">{{ balanceDetail.provider || '-' }}</NDescriptionsItem>
            <template v-if="balanceDetail.balance !== undefined && balanceDetail.balance !== null && balanceDetail.balance > 0">
              <NDescriptionsItem label="可用余额">{{ formatCurrency(balanceDetail.balance, balanceDetail.currency) }}</NDescriptionsItem>
              <NDescriptionsItem label="已使用">{{ formatCurrency(balanceDetail.used_amount, balanceDetail.currency) }}</NDescriptionsItem>
            </template>
            <template v-if="balanceDetail.plan_type">
              <NDescriptionsItem label="订阅计划">{{ balanceDetail.plan_type }}</NDescriptionsItem>
              <NDescriptionsItem label="订阅状态">{{ balanceDetail.plan_status || '-' }}</NDescriptionsItem>
              <NDescriptionsItem v-if="balanceDetail.renews_at" label="续费时间">{{ balanceDetail.renews_at }}</NDescriptionsItem>
              <NDescriptionsItem v-if="balanceDetail.expires_at" label="到期时间">{{ balanceDetail.expires_at }}</NDescriptionsItem>
            </template>
            <template v-if="balanceDetail.token_quota > 0">
              <NDescriptionsItem label="Token 配额">{{ balanceDetail.token_quota }}</NDescriptionsItem>
              <NDescriptionsItem label="Token 剩余">{{ balanceDetail.token_remaining }}</NDescriptionsItem>
            </template>
            <NDescriptionsItem v-if="balanceDetail.error_msg" label="说明" :span="2">
              <span style="color:#94a3b8;word-break:break-all">{{ balanceDetail.error_msg }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="查询时间" :span="2">{{ balanceDetail.last_checked_at || '-' }}</NDescriptionsItem>
            <NDescriptionsItem v-if="balanceDetail.raw_data && balanceDetail.raw_data !== '{}'" label="原始响应" :span="2">
              <pre style="white-space:pre-wrap;font-size:12px;color:#94a3b8;max-height:200px;overflow:auto">{{ balanceDetail.raw_data }}</pre>
            </NDescriptionsItem>
          </NDescriptions>

          <!-- 手动维护余额（用于无公开余额 API 的供应商，如 OpenCode） -->
          <NDivider style="margin:12px 0" />
          <div v-if="!manualBalanceVisible" style="display:flex;justify-content:flex-end;gap:8px">
            <NButton size="small" @click="manualBalanceVisible = true">手动维护余额</NButton>
            <NButton size="small" @click="refreshBalance(balanceDetail.channel_id)">
              <template #icon><NIcon size="14"><RefreshSharp /></NIcon></template>
              重新刷新
            </NButton>
          </div>
          <div v-else style="display:flex;flex-direction:column;gap:8px">
            <div style="display:flex;align-items:center;gap:8px">
              <span style="font-size:13px;color:#94a3b8;width:60px">余额</span>
              <NInputNumber v-model:value="manualBalanceVal" :min="0" style="flex:1" placeholder="当前余额" />
              <NSelect v-model:value="manualCurrency" :options="[{label:'USD',value:'USD'},{label:'CNY',value:'CNY'}]" style="width:90px" />
            </div>
            <div style="display:flex;justify-content:flex-end;gap:8px">
              <NButton size="small" :loading="savingManualBalance" type="primary" @click="saveManualBalance">保存</NButton>
              <NButton size="small" @click="manualBalanceVisible = false">取消</NButton>
            </div>
          </div>
        </template>
      </NModal>
    </NSpace>
  </NSpin>
</template>
