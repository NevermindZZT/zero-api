<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed } from 'vue'
import {
  NCard, NDataTable, NSpin, NSpace, NDatePicker, NButton, NStatistic, NGrid, NGi, NIcon, NSelect,
} from 'naive-ui'
import { TrendingUpSharp, RefreshSharp, KeySharp } from '@vicons/ionicons5'
import { usageApi } from '@/api'
import api from '@/api'

const loading = ref(true)
const refreshing = ref(false)
const overview = ref<any>({})
const dailyStats = ref<any[]>([])
const records = ref<any[]>([])
const dateRange = ref<[number, number]>([Date.now() - 7 * 86400000, Date.now()])
const apiKeys = ref<any[]>([])
const selectedApiKeyId = ref<number | null>(null)
let refreshTimer: ReturnType<typeof setInterval> | null = null

const apiKeyOptions = computed(() => [
  { label: '全部密钥', value: null },
  ...apiKeys.value.map((k: any) => ({ label: `${k.name} (${k.key?.substring(0, 12)}...)`, value: k.id })),
])

const dailyColumns = [
  { title: '日期', key: 'date' },
  { title: '请求数', key: 'requests' },
  { title: '输入 Tokens', key: 'prompt_tokens', render: (r: any) => r.prompt_tokens?.toLocaleString() },
  { title: '输出 Tokens', key: 'completion_tokens', render: (r: any) => r.completion_tokens?.toLocaleString() },
  { title: '缓存命中', key: 'cache_hit_tokens', render: (r: any) => r.cache_hit_tokens?.toLocaleString() || '-' },
  { title: '总 Tokens', key: 'total_tokens', render: (r: any) => r.total_tokens?.toLocaleString() },
  { title: '费用 ($)', key: 'cost', render: (r: any) => r.cost?.toFixed(6) },
]

const recordColumns = [
  { title: '模型', key: 'request_model' },
  { title: '输入', key: 'prompt_tokens' },
  { title: '输出', key: 'completion_tokens' },
  { title: '缓存命中', key: 'cache_hit_tokens', render: (r: any) => r.cache_hit_tokens?.toLocaleString() || '-' },
  { title: '总 Tokens', key: 'total_tokens' },
  { title: '延迟 (ms)', key: 'latency_ms' },
  { title: '费用 ($)', key: 'cost', render: (r: any) => r.cost?.toFixed(6) },
  { title: '时间', key: 'created_at' },
]

onMounted(async () => {
  try {
    const keysRes = await api.get('/api-keys')
    apiKeys.value = keysRes.data
  } catch {}
  loadData()
  refreshTimer = setInterval(loadData, 15000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})

async function loadData() {
  loading.value = true
  const ak = selectedApiKeyId.value || undefined
  try {
    const [overviewRes, dailyRes, recordsRes] = await Promise.all([
      usageApi.overview(ak),
      usageApi.daily(undefined, undefined, ak),
      usageApi.records(ak),
    ])
    overview.value = overviewRes.data
    dailyStats.value = dailyRes.data
    records.value = recordsRes.data
  } finally {
    loading.value = false
  }
}

function formatDate(ts: number) {
  const d = new Date(ts)
  return d.toISOString().split('T')[0]
}

async function refresh() {
  if (refreshing.value) return
  refreshing.value = true
  const start = formatDate(dateRange.value[0])
  const end = formatDate(dateRange.value[1])
  const ak = selectedApiKeyId.value || undefined
  try {
    const [overviewRes, dailyRes, recordsRes] = await Promise.all([
      usageApi.overview(ak),
      usageApi.daily(start, end, ak),
      usageApi.records(ak),
    ])
    overview.value = overviewRes.data
    dailyStats.value = dailyRes.data
    records.value = recordsRes.data
  } finally {
    refreshing.value = false
  }
}

function onApiKeyChange() {
  loadData()
}

function now() {
  const d = new Date()
  return `${d.getHours().toString().padStart(2,'0')}:${d.getMinutes().toString().padStart(2,'0')}:${d.getSeconds().toString().padStart(2,'0')}`
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header" style="display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:8px">
        <div>
          <h2>
            <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><TrendingUpSharp /></NIcon>
            使用统计
          </h2>
        </div>
        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-top:4px">
          <div style="display:flex;align-items:center;gap:4px">
            <NIcon size="14" color="#94a3b8"><KeySharp /></NIcon>
            <NSelect
              v-model:value="selectedApiKeyId"
              :options="apiKeyOptions"
              placeholder="全部密钥"
              size="tiny"
              style="min-width:180px"
              clearable
              @update:value="onApiKeyChange"
            />
          </div>
          <span style="font-size:12px;color:#64748b">● 15s 自动刷新</span>
          <NButton size="tiny" :loading="refreshing" @click="refresh">
            <template #icon><NIcon size="14"><RefreshSharp /></NIcon></template>
            刷新
          </NButton>
        </div>
      </div>

      <NGrid :x-gap="16" :y-gap="16" :cols="2" narrow>
        <NGi>
          <NCard title="总请求数" hoverable>
            <NStatistic :value="overview.total_requests || 0" />
          </NCard>
        </NGi>
        <NGi>
          <NCard title="今日请求" hoverable>
            <NStatistic :value="overview.today_requests || 0" />
          </NCard>
        </NGi>
      </NGrid>
      <NGrid :x-gap="16" :y-gap="16" :cols="4">
        <NGi>
          <NCard title="今日 Tokens" hoverable>
            <NStatistic :value="overview.today_tokens?.toLocaleString() || '0'" />
          </NCard>
        </NGi>
        <NGi>
          <NCard title="总 Tokens" hoverable>
            <NStatistic :value="overview.total_tokens?.toLocaleString() || '0'" />
          </NCard>
        </NGi>
        <NGi>
          <NCard title="缓存命中" hoverable>
            <NStatistic :value="overview.total_cache_hits?.toLocaleString() || '0'" />
          </NCard>
        </NGi>
        <NGi>
          <NCard title="缓存命中率" hoverable>
            <NStatistic :value="(overview.cache_hit_rate?.toFixed(1) || '0') + '%'" />
          </NCard>
        </NGi>
      </NGrid>

      <div style="display:flex;align-items:center;gap:12px">
        <NDatePicker v-model:value="dateRange" type="daterange" clearable />
        <NButton type="primary" @click="refresh">刷新</NButton>
      </div>

      <NCard title="按日统计">
        <NDataTable :columns="dailyColumns" :data="dailyStats" :bordered="false" size="small" />
      </NCard>

      <NCard title="最近使用记录">
        <NDataTable :columns="recordColumns" :data="records" :bordered="false" size="small" :max-height="500" />
      </NCard>
    </NSpace>
  </NSpin>
</template>
