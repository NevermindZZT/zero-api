<script setup lang="ts">
import { h, onMounted, onUnmounted, ref, nextTick } from 'vue'
import { NCard, NGrid, NGi, NStatistic, NDataTable, NSpin, NSpace, NProgress, NIcon, NButton, NTag } from 'naive-ui'
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
const chartContainer = ref<HTMLElement>()
const pieContainer = ref<HTMLElement>()
const lastUpdated = ref('')
let refreshTimer: ReturnType<typeof setInterval> | null = null
let trendChart: echarts.ECharts | null = null
let pieChart: echarts.ECharts | null = null

const recordColumns = [
  { title: '模型', key: 'request_model' },
  { title: '输入', key: 'prompt_tokens' },
  { title: '输出', key: 'completion_tokens' },
  { title: '缓存命中', key: 'cache_hit_tokens' },
  { title: '总 Tokens', key: 'total_tokens' },
  { title: '费用 ($)', key: 'cost', render: (r: any) => r.cost?.toFixed(6) },
  { title: '时间', key: 'created_at', render: (r: any) => formatDateTime(r.created_at) },
]

const statCards = [
  { label: '总请求数', value: 'overview.total_requests', icon: SendSharp, color: '#667eea', bg: 'rgba(102,126,234,0.15)' },
  { label: '总 Tokens', value: 'overview.total_tokens', icon: DocumentTextSharp, color: '#22c55e', bg: 'rgba(34,197,94,0.15)', format: 'tokens' },
  { label: '总费用', value: 'overview.total_cost', icon: CashSharp, color: '#eab308', bg: 'rgba(250,204,21,0.15)', format: 'cost' },
  { label: '活跃渠道', value: 'overview.active_channels', icon: CubeSharp, color: '#a855f7', bg: 'rgba(168,85,247,0.15)' },
]

function now() {
  const d = new Date()
  return `${d.getHours().toString().padStart(2,'0')}:${d.getMinutes().toString().padStart(2,'0')}:${d.getSeconds().toString().padStart(2,'0')}`
}

async function loadData() {
  try {
    const [overviewRes, dailyRes, recordsRes] = await Promise.all([
      usageApi.overview(),
      usageApi.daily(),
      usageApi.records(),
    ])
    overview.value = overviewRes.data
    dailyStats.value = dailyRes.data
    recentRecords.value = recordsRes.data
    lastUpdated.value = now()
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
  await loadData()
  loading.value = false
  nextTick(renderCharts)
  // Auto-poll every 15 seconds
  refreshTimer = setInterval(() => {
    loadData()
    nextTick(renderCharts)
  }, 15000)
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
  if (!chartContainer.value || dailyStats.value.length === 0) {
    trendChart?.dispose()
    trendChart = null
    return
  }
  if (!trendChart) {
    trendChart = echarts.init(chartContainer.value)
  }
  const dates = dailyStats.value.map((d: any) => d.date).reverse()
  const tokens = dailyStats.value.map((d: any) => d.total_tokens).reverse()
  const requests = dailyStats.value.map((d: any) => d.requests).reverse()

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
  if (!pieContainer.value || recentRecords.value.length === 0) {
    pieChart?.dispose()
    pieChart = null
    return
  }
  if (!pieChart) {
    pieChart = echarts.init(pieContainer.value)
  }
  const modelMap = new Map<string, number>()
  recentRecords.value.forEach((r: any) => {
    modelMap.set(r.request_model, (modelMap.get(r.request_model) || 0) + r.total_tokens)
  })
  const data = Array.from(modelMap.entries()).map(([name, value]) => ({ name, value }))
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
        <div style="display:flex;align-items:center;gap:8px;margin-top:4px">
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

      <NGrid :x-gap="16" :y-gap="16" :cols="4">
        <NGi v-for="card in statCards" :key="card.label">
          <NCard class="stat-card" hoverable>
            <div class="stat-icon" :style="{ background: card.bg, color: card.color }">
              <NIcon :size="20"><component :is="card.icon" /></NIcon>
            </div>
            <p class="stat-label">{{ card.label }}</p>
            <p class="stat-value">
              <template v-if="card.format === 'tokens'">{{ formatTokens(overview[card.value.split('.')[1]]) }}</template>
              <template v-else-if="card.format === 'cost'">${{ (overview[card.value.split('.')[1]] || 0).toFixed(6) }}</template>
              <template v-else>{{ overview[card.value.split('.')[1]] || 0 }}</template>
            </p>
          </NCard>
        </NGi>
      </NGrid>

      <NGrid :x-gap="16" :y-gap="16" :cols="2">
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
                    <div style="text-align:center"><span style="font-size:28px;font-weight:700">{{ (overview.cache_hit_rate || 0).toFixed(1) }}%</span></div>
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
            <div class="chart-card-body">
              <div ref="pieContainer" style="height:200px;width:100%"></div>
              <p v-if="recentRecords.length === 0" style="color:#94a3b8;text-align:center;padding:40px">暂无数据</p>
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
        <NDataTable :columns="recordColumns" :data="recentRecords" :max-height="400" :bordered="false" size="small" :single-line="false" striped />
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
</style>
