<script setup lang="ts">
import { h, onMounted, onUnmounted, ref, computed, watch, nextTick } from 'vue'
import { NCard, NGrid, NGi, NStatistic, NDataTable, NSpin, NSpace, NProgress, NIcon, NButton, NButtonGroup, NTag } from 'naive-ui'
import {
  StatsChartSharp,
  SendSharp,
  DocumentTextSharp,
  CashSharp,
  CubeSharp,
  PieChartSharp,
  TrendingUpSharp,
  ClipboardSharp,
  RefreshSharp,
} from '@vicons/ionicons5'
import { usageApi } from '@/api'
import { formatDateTime } from '@/utils/format'
import * as echarts from 'echarts'

const loading = ref(true)
const refreshing = ref(false)
const overview = ref<any>({})
const dailyStats = ref<any[]>([])
const recentRecords = ref<any[]>([])
const modelStats = ref<any[]>([])
const chartContainer = ref<HTMLElement>()
const pieContainer = ref<HTMLElement>()
const lastUpdated = ref('')
let refreshTimer: ReturnType<typeof setInterval> | null = null
let trendChart: echarts.ECharts | null = null
let pieChart: echarts.ECharts | null = null

// 响应式列数
const statCols = ref(4)
const chartCols = ref(2)
let mqListener: (() => void) | null = null

const recordColumns = [
  { title: '模型', key: 'request_model' },
  { title: '渠道', key: 'channel_name', render: (r: any) => r.channel_name || '-' },
  { title: '输入', key: 'prompt_tokens' },
  { title: '输出', key: 'completion_tokens' },
  { title: '缓存命中', key: 'cache_hit_tokens' },
  { title: '总 Tokens', key: 'total_tokens' },
  { title: '费用 ($)', key: 'cost', render: (r: any) => r.cost?.toFixed(6) },
  { title: '时间', key: 'created_at', render: (r: any) => formatDateTime(r.created_at) },
]

const statCards = [
  { label: '请求数', value: 'total_requests', icon: SendSharp, color: '#667eea', bg: 'rgba(102,126,234,0.15)' },
  { label: 'Tokens', value: 'total_tokens', icon: DocumentTextSharp, color: '#22c55e', bg: 'rgba(34,197,94,0.15)', format: 'tokens' },
  { label: '费用', value: 'total_cost', icon: CashSharp, color: '#eab308', bg: 'rgba(250,204,21,0.15)', format: 'cost' },
  { label: '活跃渠道', value: 'active_channels', icon: CubeSharp, color: '#a855f7', bg: 'rgba(168,85,247,0.15)' },
]

function formatLocalDate(date: Date) {
  const year = date.getFullYear()
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${year}-${month}-${day}`
}

function now() {
  const d = new Date()
  return `${d.getHours().toString().padStart(2,'0')}:${d.getMinutes().toString().padStart(2,'0')}:${d.getSeconds().toString().padStart(2,'0')}`
}

// 时间范围切换
const timeRange = ref<'total' | 'month' | 'week' | 'today'>('total')
const timeRangeOptions: { label: string; value: typeof timeRange.value }[] = [
  { label: '全部', value: 'total' },
  { label: '本月', value: 'month' },
  { label: '本周', value: 'week' },
  { label: '今日', value: 'today' },
]

// 将时间范围转为 start/end 参数
function getRangeParams(range: typeof timeRange.value) {
  const today = new Date()
  const end = formatLocalDate(today)
  if (range === 'total') return { start: undefined, end: undefined }
  if (range === 'today') return { start: end, end }
  if (range === 'month') {
    const start = formatLocalDate(new Date(today.getFullYear(), today.getMonth(), 1))
    return { start, end }
  }
  if (range === 'week') {
    const day = today.getDay()
    const diff = day === 0 ? 6 : day - 1
    const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate())
    monday.setDate(monday.getDate() - diff)
    return { start: formatLocalDate(monday), end }
  }
  const start = formatLocalDate(new Date(today.getFullYear(), today.getMonth(), today.getDate() - 90))
  return { start, end }
}

const rangeLabel = computed(() => {
  const opt = timeRangeOptions.find(o => o.value === timeRange.value)
  return opt?.label || '全部'
})

// 时间范围切换时重新加载数据
watch(timeRange, async () => {
  await loadData()
  nextTick(renderCharts)
})

async function loadData() {
  try {
    const { start, end } = getRangeParams(timeRange.value)
    const [overviewRes, dailyRes, recordsRes, modelRes] = await Promise.all([
      usageApi.overview(),
      usageApi.daily(start, end),
      usageApi.records(undefined, start, end, 200),
      usageApi.byModel(start, end),
    ])
    overview.value = overviewRes.data
    dailyStats.value = dailyRes.data
    recentRecords.value = recordsRes.data
    modelStats.value = modelRes.data
    lastUpdated.value = now()
  } catch (e) {
    console.error(e)
  }
}

// 智能轮询：每 15s 只刷新 overview 卡片数值，不重载图表
async function smartPoll() {
  try {
    const res = await usageApi.overview()
    const newTotal = res.data?.total_requests || 0
    const oldTotal = overview.value?.total_requests
    if (oldTotal === undefined || newTotal !== oldTotal) {
      overview.value = res.data
      lastUpdated.value = now()
    }
  } catch (e) {
    console.error(e)
  }
}

async function refresh() {
  if (refreshing.value) return
  refreshing.value = true
  await loadData()
  nextTick(() => {
    renderCharts()
    refreshing.value = false
  })
}

onMounted(async () => {
  // 响应式列数
  function updateCols() {
    const w = window.innerWidth
    statCols.value = w < 480 ? 1 : w < 768 ? 2 : 4
    chartCols.value = w < 768 ? 1 : 2
  }
  updateCols()
  window.addEventListener('resize', updateCols)
  mqListener = () => window.removeEventListener('resize', updateCols)

  await loadData()
  loading.value = false
  nextTick(renderCharts)
  // 智能轮询：每 15s 检查是否有新数据，有变化才全量刷新
  refreshTimer = setInterval(smartPoll, 15000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  trendChart?.dispose()
  pieChart?.dispose()
})

function renderCharts() {
  renderTrendChart()
  renderPieChart()
}

function renderTrendChart() {
  // 服务端已根据时间范围过滤，直接使用 dailyStats
  const stats = dailyStats.value
  if (trendChart) {
    trendChart.dispose()
    trendChart = null
  }
  if (!chartContainer.value || !stats || stats.length === 0) return

  trendChart = echarts.init(chartContainer.value)
  const dates = stats.map((d: any) => d.date).reverse()
  const tokens = stats.map((d: any) => d.total_tokens).reverse()
  const requests = stats.map((d: any) => d.requests).reverse()

  trendChart.setOption({
    tooltip: { trigger: 'axis' },
    grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
    xAxis: { type: 'category', data: dates, axisLabel: { color: '#94a3b8' } },
    yAxis: [
      { type: 'value', name: 'Tokens', nameTextStyle: { color: '#94a3b8' }, axisLabel: { color: '#94a3b8' } },
      { type: 'value', name: '请求数', nameTextStyle: { color: '#94a3b8' }, axisLabel: { color: '#94a3b8' } },
    ],
    series: [
      {
        name: 'Tokens', type: 'bar', data: tokens,
        itemStyle: { borderRadius: [4, 4, 0, 0], color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [{ offset: 0, color: '#667eea' }, { offset: 1, color: '#764ba2' }] } },
      },
      {
        name: '请求数', type: 'line', yAxisIndex: 1, data: requests,
        lineStyle: { color: '#22c55e' }, itemStyle: { color: '#22c55e' },
      },
    ],
  })
}

function renderPieChart() {
  // 使用服务端聚合数据，无需前端 groupBy
  const stats = modelStats.value
  if (pieChart) {
    pieChart.dispose()
    pieChart = null
  }
  if (!pieContainer.value || !stats || stats.length === 0) return

  pieChart = echarts.init(pieContainer.value)
  const data = stats.map((s: any) => ({ name: s.request_model, value: s.total_tokens }))
  pieChart.setOption({
    tooltip: { trigger: 'item', formatter: '{b}: {c} tokens ({d}%)' },
    series: [{
      type: 'pie', radius: ['40%', '70%'],
      label: { show: true, formatter: '{b}', color: '#e2e8f0', fontSize: 11 },
      data, itemStyle: { borderRadius: 6, borderColor: '#0f172a', borderWidth: 2 },
    }],
  })
}

function formatTokens(n: number) {
  if (!n) return '0'
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return n.toString()
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header" style="display:flex;justify-content:space-between;align-items:flex-start">
        <div>
          <h2>
            <NIcon size="22" color="#667eea" style="vertical-align:-3px;margin-right:6px"><StatsChartSharp /></NIcon>
            仪表盘
          </h2>
          <p class="page-subtitle">zero-api 全局概览</p>
        </div>
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
        <NButtonGroup size="tiny">
          <NButton v-for="opt in timeRangeOptions" :key="opt.value" :type="timeRange === opt.value ? 'primary' : 'default'" @click="timeRange = opt.value">{{ opt.label }}</NButton>
        </NButtonGroup>
        <span v-if="lastUpdated" style="font-size:12px;color:#64748b">
          上次更新: {{ lastUpdated }}
          <span style="color:#22c55e;margin-left:4px">● 15s 自动刷新</span>
        </span>
        <NButton size="tiny" :loading="refreshing" @click="refresh">
          <template #icon><NIcon size="14"><RefreshSharp /></NIcon></template>
          刷新
        </NButton>
      </div>
      </div>

      <NGrid :x-gap="16" :y-gap="16" :cols="statCols">
        <NGi v-for="card in statCards" :key="card.label">
          <NCard class="stat-card" hoverable>
            <div class="stat-icon" :style="{ background: card.bg, color: card.color }">
              <NIcon :size="20"><component :is="card.icon" /></NIcon>
            </div>
            <p class="stat-label">{{ card.label }}</p>
            <p class="stat-value">
              <template v-if="card.format === 'tokens'">{{ formatTokens(overview[card.value]) }}</template>
              <template v-else-if="card.format === 'cost'">${{ (overview[card.value] || 0).toFixed(6) }}</template>
              <template v-else>{{ overview[card.value] || 0 }}</template>
            </p>
          </NCard>
        </NGi>
      </NGrid>

      <NGrid :x-gap="16" :y-gap="16" :cols="chartCols">
        <NGi>
          <NCard class="chart-card">
            <template #header>
              <div class="card-header-with-icon">
                <NIcon size="18" color="#22c55e"><CubeSharp /></NIcon>
                <span>缓存命中率</span>
              </div>
            </template>
            <div class="chart-card-body">
              <div style="display:flex;align-items:center;gap:32px;padding:8px">
                <div style="position:relative;width:120px;height:120px">
                  <NProgress type="circle" :percentage="Math.round(overview.cache_hit_rate || 0)" :stroke-width="10" :size="120">
                    <div style="text-align:center;white-space:nowrap"><span style="font-size:24px;font-weight:700">{{ (overview.cache_hit_rate || 0).toFixed(1) }}%</span></div>
                  </NProgress>
                </div>
                <div>
                  <div class="cache-stat-row">
                    <span class="cache-stat-label">缓存命中</span>
                    <span class="cache-stat-value">{{ formatTokens(overview.total_cache_hits) }} tokens</span>
                  </div>
                  <div class="cache-stat-row">
                    <span class="cache-stat-label">今日请求</span>
                    <span class="cache-stat-value">{{ overview.today_requests || 0 }}</span>
                  </div>
                  <p style="color:#64748b;font-size:12px;margin-top:12px">缓存命中率 = 缓存命中 Tokens / 总 Tokens</p>
                </div>
              </div>
            </div>
          </NCard>
        </NGi>
        <NGi>
          <NCard class="chart-card">
            <template #header>
              <div class="card-header-with-icon">
                <NIcon size="18" color="#a855f7"><PieChartSharp /></NIcon>
                <span>模型用量分布</span>
              </div>
            </template>
            <div class="chart-card-body" style="flex-direction:column">
              <div ref="pieContainer" style="height:200px;width:100%"></div>
              <p v-if="recentRecords.length === 0" style="color:#94a3b8;text-align:center;padding:40px">暂无数据</p>
              <div v-else style="display:flex;gap:16px;font-size:12px;color:#94a3b8;padding:4px 0 0">
                <span>范围: <b style="color:#e2e8f0">{{ rangeLabel }}</b></span>
                <span>统计模型: <b style="color:#e2e8f0">{{ modelStats.length }}</b> 个</span>
              </div>
            </div>
          </NCard>
        </NGi>
      </NGrid>

      <NCard class="chart-card">
        <template #header>
          <div class="card-header-with-icon">
            <NIcon size="18" color="#667eea"><TrendingUpSharp /></NIcon>
            <span>使用趋势</span>
          </div>
        </template>
        <div ref="chartContainer" style="height:300px"></div>
        <p v-if="dailyStats.length === 0" style="color:#94a3b8;text-align:center;padding:60px">暂无使用数据</p>
      </NCard>

      <NCard class="chart-card">
        <template #header>
          <div class="card-header-with-icon">
            <NIcon size="18" color="#f97316"><ClipboardSharp /></NIcon>
            <span>最近请求</span>
          </div>
        </template>
        <NDataTable :columns="recordColumns" :data="recentRecords" :max-height="400" :bordered="false" size="small" :single-line="false" striped :scroll-x="800" />
      </NCard>
    </NSpace>
  </NSpin>
</template>

<style scoped>
/* 保持同行图表卡片等高 */
.n-card.chart-card {
  height: 100%;
}
.chart-card-body {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 160px;
}
.card-header-with-icon {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
}
.stat-card {
  padding: 8px;
  cursor: default;
  transition: transform 0.2s, box-shadow 0.2s;
  text-align: center;
}
.stat-card:hover {
  transform: translateY(-3px);
  box-shadow: 0 12px 30px -8px rgba(0,0,0,0.5) !important;
}
.stat-icon {
  width: 44px;
  height: 44px;
  border-radius: 14px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 14px;
}
.stat-label {
  color: #94a3b8;
  font-size: 13px;
  margin: 0 0 6px 0;
}
.stat-value {
  font-size: 26px;
  font-weight: 700;
  margin: 0;
  background: linear-gradient(135deg, #f1f5f9, #94a3b8);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}
.chart-card { min-height: 100px; }
.cache-stat-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 0;
  border-bottom: 1px solid rgba(255,255,255,0.04);
}
.cache-stat-label { color: #94a3b8; font-size: 13px; }
.cache-stat-value { color: #e2e8f0; font-size: 14px; font-weight: 600; }

/* 响应式 */
@media (max-width: 767px) {
  .stat-value { font-size: 22px; }
  .cache-stat-row { flex-direction: column; gap: 2px; }
}
</style>
