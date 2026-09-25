<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed, watch, nextTick } from 'vue'
import { NCard, NGrid, NGi, NStatistic, NDataTable, NSpin, NSpace, NProgress, NIcon, NButton, NButtonGroup, NTag, NDatePicker } from 'naive-ui'
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
  GridSharp,
} from '@vicons/ionicons5'
import { usageApi } from '@/api'
import { formatDateTime } from '@/utils/format'
import { isDarkTheme } from '@/utils/theme'
import * as echarts from 'echarts'
import { CalendarHeatmap } from '@silverwind/vue3-calendar-heatmap'
import '@silverwind/vue3-calendar-heatmap/dist/style.css'

const loading = ref(true)
const refreshing = ref(false)
const overview = ref<any>({})
const dailyStats = ref<any[]>([])
const recentRecords = ref<any[]>([])
const modelStats = ref<any[]>([])
const tpsStats = ref<any[]>([])
const latencyStats = ref<any[]>([])
const trendMetric = ref<'tokens' | 'cost'>('tokens')
const modelMetric = ref<'tokens' | 'requests' | 'cost'>('tokens')
const latencyMetric = ref<'first' | 'total'>('first')
const yearHeatmap = ref<any[]>([])
const chartContainer = ref<HTMLElement>()
const modelChartContainer = ref<HTMLElement>()
const tpsChartContainer = ref<HTMLElement>()
const latencyChartContainer = ref<HTMLElement>()
const heatmapContainer = ref<HTMLElement>()
const lastUpdated = ref('')
let refreshTimer: ReturnType<typeof setInterval> | null = null
let trendChart: echarts.ECharts | null = null
let modelChart: echarts.ECharts | null = null
let tpsChart: echarts.ECharts | null = null
let latencyChart: echarts.ECharts | null = null

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
  { title: '延时/耗时', key: 'latency_ms', render: (r: any) => r.latency_ms != null ? `${r.latency_ms} / ${r.total_duration_ms || r.latency_ms} ms` : '-' },
  { title: '时间', key: 'created_at', render: (r: any) => formatDateTime(r.created_at) },
]

const trendMetricOptions = [
  { label: 'Tokens', value: 'tokens' as const },
  { label: '费用', value: 'cost' as const },
]
const modelMetricOptions = [
  { label: 'Tokens', value: 'tokens' as const },
  { label: '请求', value: 'requests' as const },
  { label: '费用', value: 'cost' as const },
]
const latencyMetricOptions = [
  { label: '首响应延迟', value: 'first' as const },
  { label: '总耗时', value: 'total' as const },
]

const statCards = [
  { label: '请求数', value: 'total_requests', icon: SendSharp, color: 'var(--accent-primary)', bg: 'var(--accent-soft)' },
  { label: 'Tokens', value: 'total_tokens', icon: DocumentTextSharp, color: '#22c55e', bg: 'rgba(34,197,94,0.15)', format: 'tokens', subLabel: '总 Tokens', subLeft: '入', subLeftKey: 'total_prompt_tokens', subRight: '出', subRightKey: 'total_output_tokens', subLeftColor: '#3b82f6', subRightColor: '#f97316' },
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
// 快捷按钮 + 自定义日期范围选择器（custom 时用 customRange 的起止日期）
const timeRange = ref<'total' | 'month' | 'prevMonth' | 'week' | 'prevWeek' | 'today' | 'yesterday' | 'custom'>('month')
const timeRangeOptions: { label: string; value: typeof timeRange.value }[] = [
  { label: '全部', value: 'total' },
  { label: '本月', value: 'month' },
  { label: '上月', value: 'prevMonth' },
  { label: '本周', value: 'week' },
  { label: '上周', value: 'prevWeek' },
  { label: '今日', value: 'today' },
  { label: '昨日', value: 'yesterday' },
]

// 自定义日期范围（时间戳对）
const customRange = ref<[number, number] | null>(null)

// 设置快捷范围时清除自定义范围
function setTimeRange(val: typeof timeRange.value) {
  customRange.value = null
  timeRange.value = val
}

// 自定义范围变更：切换到 custom 模式
function onCustomRangeChange(value: [number, number] | null) {
  if (value) {
    customRange.value = value
    timeRange.value = 'custom'
  }
}

// 根据起止日期推断图表粒度
function inferGranularity(startStr: string, endStr: string): 'hour' | 'day' | 'month' {
  const startT = new Date(startStr).getTime()
  const endT = new Date(endStr).getTime()
  const days = Math.round((endT - startT) / 86400000) + 1
  if (days <= 3) return 'hour'   // 3 天内：按小时
  if (days <= 62) return 'day'   // ~2 个月内：按天
  return 'month'                 // 更长：按月
}

// 客户端时区偏移（分钟），传给后端做时区转换
const tzOffset = -new Date().getTimezoneOffset()

// 将时间范围转为 start/end/granularity 参数
function getRangeParams(range: typeof timeRange.value) {
  const today = new Date()
  const end = formatLocalDate(today)
  if (range === 'custom' && customRange.value) {
    const startStr = formatLocalDate(new Date(customRange.value[0]))
    const endStr = formatLocalDate(new Date(customRange.value[1]))
    return { start: startStr, end: endStr, granularity: inferGranularity(startStr, endStr) }
  }
  switch (range) {
    case 'today':
      return { start: end, end, granularity: 'hour' as const }
    case 'yesterday': {
      const y = new Date(today)
      y.setDate(y.getDate() - 1)
      const ds = formatLocalDate(y)
      return { start: ds, end: ds, granularity: 'hour' as const }
    }
    case 'month': {
      const start = formatLocalDate(new Date(today.getFullYear(), today.getMonth(), 1))
      return { start, end, granularity: 'day' as const }
    }
    case 'prevMonth': {
      // 上月 1 日 = 本月 1 日减一个月；上月最后一天 = 本月 0 日
      const first = new Date(today.getFullYear(), today.getMonth() - 1, 1)
      const last = new Date(today.getFullYear(), today.getMonth(), 0)
      return { start: formatLocalDate(first), end: formatLocalDate(last), granularity: 'day' as const }
    }
    case 'week': {
      const day = today.getDay()
      const diff = day === 0 ? 6 : day - 1
      const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate())
      monday.setDate(monday.getDate() - diff)
      // 周日 = 周一 + 6
      const sunday = new Date(monday)
      sunday.setDate(monday.getDate() + 6)
      return { start: formatLocalDate(monday), end: formatLocalDate(sunday), granularity: 'day' as const }
    }
    case 'prevWeek': {
      const day = today.getDay()
      const diff = day === 0 ? 6 : day - 1
      // 本周一
      const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate())
      monday.setDate(monday.getDate() - diff)
      // 上周一 = 本周一 - 7
      const prevMonday = new Date(monday)
      prevMonday.setDate(monday.getDate() - 7)
      const prevSunday = new Date(prevMonday)
      prevSunday.setDate(prevMonday.getDate() + 6)
      return { start: formatLocalDate(prevMonday), end: formatLocalDate(prevSunday), granularity: 'day' as const }
    }
    default: {
      // total: 近 12 个月，按月统计
      const start = formatLocalDate(new Date(today.getFullYear(), today.getMonth() - 11, 1))
      return { start, end, granularity: 'month' as const }
    }
  }
}

const rangeLabel = computed(() => {
  if (timeRange.value === 'custom' && customRange.value) {
    const s = formatLocalDate(new Date(customRange.value[0]))
    const e = formatLocalDate(new Date(customRange.value[1]))
    return `${s} ~ ${e}`
  }
  const opt = timeRangeOptions.find(o => o.value === timeRange.value)
  return opt?.label || '全部'
})

// 时间范围切换时重新加载数据
watch(timeRange, async () => {
  await loadData()
  nextTick(renderCharts)
})

watch([isDarkTheme, trendMetric, modelMetric, latencyMetric], () => nextTick(renderCharts))

async function loadData() {
  try {
    const { start, end, granularity } = getRangeParams(timeRange.value)
    const [overviewRes, dailyRes, recordsRes, modelRes, latencyRes] = await Promise.all([
      usageApi.overview(undefined, start, end, tzOffset),
      usageApi.daily(start, end, undefined, granularity, tzOffset),
      usageApi.records(undefined, start, end, 200, tzOffset),
      usageApi.byModel(start, end, undefined, tzOffset),
      usageApi.latency(start, end, undefined, granularity, tzOffset),
    ])
    overview.value = overviewRes.data
    dailyStats.value = dailyRes.data
    recentRecords.value = recordsRes.data
    modelStats.value = modelRes.data
    latencyStats.value = latencyRes.data
    lastUpdated.value = now()
  } catch (e) {
    console.error(e)
  }
}

async function loadTPS() {
  try {
    const res = await usageApi.tps('5m', 10)
    tpsStats.value = res.data?.items || []
    await nextTick(renderTPSChart)
  } catch (e) {
    console.error(e)
  }
}

// 智能轮询：每 15s 检查用量变化，并同步刷新 TPS 快照
async function smartPoll() {
  try {
    const { start, end } = getRangeParams(timeRange.value)
    const [res] = await Promise.all([
      usageApi.overview(undefined, start, end, tzOffset),
      loadTPS(),
    ])
    const newTotal = res.data?.total_requests || 0
    const oldTotal = overview.value?.total_requests
    if (oldTotal === undefined || newTotal !== oldTotal) {
      await loadData()
      nextTick(renderCharts)
      lastUpdated.value = now()
    }
  } catch (e) {
    console.error(e)
  }
}

async function refresh() {
  if (refreshing.value) return
  refreshing.value = true
  await Promise.all([loadData(), loadTPS()])
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
    nextTick(() => {
      trendChart?.resize()
      modelChart?.resize()
      tpsChart?.resize()
      latencyChart?.resize()
    })
  }
  updateCols()
  window.addEventListener('resize', updateCols)
  mqListener = () => window.removeEventListener('resize', updateCols)

  await Promise.all([loadData(), loadTPS()])
  // 加载年度热力图（不跟随时间范围）
  try {
    const res = await usageApi.yearHeatmap(tzOffset)
    yearHeatmap.value = res.data
  } catch (e) { console.error(e) }
  loading.value = false
  nextTick(renderCharts)
  // 智能轮询：每 15s 检查用量变化，并同步刷新 TPS
  refreshTimer = setInterval(smartPoll, 15000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  mqListener?.()
  mqListener = null
  trendChart?.dispose()
  modelChart?.dispose()
  tpsChart?.dispose()
  latencyChart?.dispose()
})

function renderCharts() {
  renderTrendChart()
  renderModelChart()
  renderTPSChart()
  renderLatencyChart()
}

function themeColor(token: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(token).trim()
}

function renderTrendChart() {
  const stats = dailyStats.value
  if (trendChart) {
    trendChart.dispose()
    trendChart = null
  }
  if (!chartContainer.value || stats.length === 0) return

  trendChart = echarts.init(chartContainer.value)
  const textColor = themeColor('--text-secondary')
  const accentColor = themeColor('--accent-primary')
  const accentHover = themeColor('--accent-hover')
  const categories = stats.map((s: any) => s.date).reverse()
  const requests = stats.map((s: any) => s.requests).reverse()
  const isCost = trendMetric.value === 'cost'
  const series: any[] = isCost
    ? [
        { name: '费用', type: 'bar', data: stats.map((s: any) => s.cost).reverse(), itemStyle: { color: accentColor, borderRadius: [4, 4, 0, 0] } },
        { name: '请求数', type: 'line', yAxisIndex: 1, data: requests, smooth: true, lineStyle: { color: '#22c55e' }, itemStyle: { color: '#22c55e' } },
      ]
    : [
        { name: '输入 Tokens', type: 'bar', stack: 'tokens', data: stats.map((s: any) => s.prompt_tokens).reverse(), itemStyle: { color: accentColor } },
        { name: '输出 Tokens', type: 'bar', stack: 'tokens', data: stats.map((s: any) => s.completion_tokens).reverse(), itemStyle: { color: accentHover } },
        { name: '缓存命中', type: 'line', data: stats.map((s: any) => s.cache_hit_tokens).reverse(), smooth: true, lineStyle: { color: '#38bdf8', type: 'dashed' }, itemStyle: { color: '#38bdf8' } },
        { name: '请求数', type: 'line', yAxisIndex: 1, data: requests, smooth: true, lineStyle: { color: '#22c55e' }, itemStyle: { color: '#22c55e' } },
      ]

  trendChart.setOption({
    tooltip: { trigger: 'axis' },
    legend: { top: 0, textStyle: { color: textColor } },
    grid: { left: '3%', right: '5%', top: 40, bottom: '4%', containLabel: true },
    xAxis: { type: 'category', data: categories, axisLabel: { color: textColor } },
    yAxis: [
      {
        type: 'value',
        name: isCost ? '费用 ($)' : 'Tokens',
        nameTextStyle: { color: textColor },
        axisLabel: { color: textColor, formatter: (v: number) => isCost ? `$${Number(v).toFixed(2)}` : formatTokens(v) },
      },
      { type: 'value', name: '请求数', nameTextStyle: { color: textColor }, axisLabel: { color: textColor } },
    ],
    series,
  })
}

function renderModelChart() {
  const stats = modelStats.value
  if (modelChart) {
    modelChart.dispose()
    modelChart = null
  }
  if (!modelChartContainer.value || stats.length === 0) return

  const field = modelMetric.value === 'tokens' ? 'total_tokens' : modelMetric.value === 'requests' ? 'requests' : 'cost'
  const label = modelMetric.value === 'tokens' ? 'Tokens' : modelMetric.value === 'requests' ? '请求数' : '费用 ($)'
  const rows = [...stats]
    .sort((a: any, b: any) => Number(b[field] || 0) - Number(a[field] || 0))
    .slice(0, 10)
    .reverse()
  const textColor = themeColor('--text-secondary')
  const accentColor = themeColor('--accent-primary')
  modelChart = echarts.init(modelChartContainer.value)
  modelChart.setOption({
    tooltip: {
      trigger: 'item',
      formatter: (item: any) => `${item.name}<br/>${label}: ${modelMetric.value === 'tokens' ? formatTokens(item.value) : modelMetric.value === 'cost' ? `$${Number(item.value).toFixed(4)}` : Number(item.value).toLocaleString()}`,
    },
    grid: { left: 8, right: 48, top: 8, bottom: 8, containLabel: true },
    xAxis: {
      type: 'value',
      name: label,
      nameTextStyle: { color: textColor },
      axisLabel: { color: textColor, formatter: (v: number) => modelMetric.value === 'tokens' ? formatTokens(v) : modelMetric.value === 'cost' ? `$${Number(v).toFixed(2)}` : Number(v).toLocaleString() },
      splitLine: { lineStyle: { color: themeColor('--border-color') } },
    },
    yAxis: { type: 'category', inverse: true, data: rows.map((r: any) => r.request_model), axisLabel: { color: textColor, width: 130, overflow: 'truncate' } },
    series: [{
      type: 'bar',
      barMaxWidth: 22,
      data: rows.map((r: any) => r[field] || 0),
      itemStyle: { color: accentColor, borderRadius: [0, 5, 5, 0] },
      label: { show: true, position: 'right', color: textColor, formatter: (item: any) => modelMetric.value === 'tokens' ? formatTokens(item.value) : modelMetric.value === 'cost' ? `$${Number(item.value).toFixed(3)}` : Number(item.value).toLocaleString() },
    }],
  })
}

function renderTPSChart() {
  const stats = tpsStats.value
  if (tpsChart) {
    tpsChart.dispose()
    tpsChart = null
  }
  if (!tpsChartContainer.value || stats.length === 0) return

  const rows = [...stats].sort((a: any, b: any) => Number(a.avg_tps) - Number(b.avg_tps))
  const textColor = themeColor('--text-secondary')
  const accentColor = themeColor('--accent-primary')
  const mutedColor = themeColor('--text-muted')
  tpsChart = echarts.init(tpsChartContainer.value)
  tpsChart.setOption({
    tooltip: {
      trigger: 'item',
      formatter: (item: any) => `${item.name}<br/>平均 TPS: ${Number(item.value).toFixed(1)} tokens/s<br/>有效样本: ${item.data.sampleCount}`,
    },
    grid: { left: 8, right: 64, top: 8, bottom: 8, containLabel: true },
    xAxis: { type: 'value', name: 'tokens/s', nameTextStyle: { color: textColor }, axisLabel: { color: textColor }, splitLine: { lineStyle: { color: themeColor('--border-color') } } },
    yAxis: { type: 'category', inverse: true, data: rows.map((r: any) => r.model), axisLabel: { color: textColor, width: 130, overflow: 'truncate' } },
    series: [{
      type: 'bar',
      barMaxWidth: 22,
      data: rows.map((r: any) => ({ value: Number(r.avg_tps) || 0, sampleCount: r.sample_count || 0 })),
      itemStyle: { color: (item: any) => item.data.sampleCount < 3 ? mutedColor : accentColor, borderRadius: [0, 5, 5, 0] },
      label: { show: true, position: 'right', color: textColor, formatter: (item: any) => `${Number(item.value).toFixed(1)} · ${item.data.sampleCount}样本` },
    }],
  })
}

function renderLatencyChart() {
  const stats = latencyStats.value
  if (latencyChart) {
    latencyChart.dispose()
    latencyChart = null
  }
  if (!latencyChartContainer.value || stats.length === 0 || !stats.some((s: any) => s.sample_count > 0)) return

  const rows = [...stats].reverse()
  const textColor = themeColor('--text-secondary')
  const p50Key = latencyMetric.value === 'first' ? 'latency_p50_ms' : 'total_duration_p50_ms'
  const p95Key = latencyMetric.value === 'first' ? 'latency_p95_ms' : 'total_duration_p95_ms'
  const metricName = latencyMetric.value === 'first' ? '首响应延迟' : '总耗时'
  latencyChart = echarts.init(latencyChartContainer.value)
  latencyChart.setOption({
    tooltip: {
      trigger: 'axis',
      formatter: (items: any[]) => {
        const index = items?.[0]?.dataIndex ?? 0
        const bucket = rows[index]
        return `${bucket?.date || ''}<br/>样本数: ${bucket?.sample_count || 0}<br/>P50: ${bucket?.[p50Key] || 0} ms<br/>P95: ${bucket?.[p95Key] || 0} ms`
      },
    },
    legend: { top: 0, data: ['P50', 'P95'], textStyle: { color: textColor } },
    grid: { left: '3%', right: '4%', top: 32, bottom: '4%', containLabel: true },
    xAxis: { type: 'category', data: rows.map((r: any) => r.date), axisLabel: { color: textColor } },
    yAxis: { type: 'value', name: `${metricName} (ms)`, nameTextStyle: { color: textColor }, axisLabel: { color: textColor }, splitLine: { lineStyle: { color: themeColor('--border-color') } } },
    series: [
      { name: 'P50', type: 'line', smooth: true, showSymbol: true, symbolSize: 8, data: rows.map((r: any) => r.sample_count > 0 ? r[p50Key] : null), lineStyle: { color: themeColor('--accent-primary'), width: 2 }, itemStyle: { color: themeColor('--accent-primary') }, connectNulls: false },
      { name: 'P95', type: 'line', smooth: true, showSymbol: true, symbolSize: 8, data: rows.map((r: any) => r.sample_count > 0 ? r[p95Key] : null), lineStyle: { color: '#f59e0b', width: 2 }, itemStyle: { color: '#f59e0b' }, connectNulls: false },
    ],
  })
}

// 热力图数据格式化
const heatmapValues = computed(() =>
  yearHeatmap.value.map((d: any) => ({ date: d.date, count: d.total_tokens }))
)
const heatmapMax = computed(() =>
  Math.max(...yearHeatmap.value.map((d: any) => d.total_tokens), 1)
)
const heatmapEndDate = computed(() => {
  if (yearHeatmap.value.length === 0) return ''
  return yearHeatmap.value[yearHeatmap.value.length - 1]?.date
})

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
            <NIcon size="22" color="var(--accent-primary)" style="vertical-align:-3px;margin-right:6px"><StatsChartSharp /></NIcon>
            仪表盘
          </h2>
          <p class="page-subtitle">zero-api 全局概览</p>
        </div>
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
        <NButtonGroup size="tiny">
          <NButton v-for="opt in timeRangeOptions" :key="opt.value" :type="timeRange === opt.value ? 'primary' : 'default'" @click="setTimeRange(opt.value)">{{ opt.label }}</NButton>
        </NButtonGroup>
        <NDatePicker
          v-model:value="customRange"
          type="daterange"
          size="small"
          clearable
          :is-date-disabled="(ts: number) => ts > Date.now()"
          style="width: 240px"
          @update:value="onCustomRangeChange"
        />
        <span v-if="lastUpdated" style="font-size:12px;color:var(--text-muted)">
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
          <NCard class="stat-card" hoverable style="height:100%">
            <div class="stat-icon" :style="{ background: card.bg, color: card.color }">
              <NIcon :size="20"><component :is="card.icon" /></NIcon>
            </div>
            <p class="stat-label">{{ card.label }}</p>
            <p class="stat-value">
              <template v-if="card.format === 'tokens'">{{ formatTokens(overview[card.value]) }}</template>
              <template v-else-if="card.format === 'cost'">${{ (overview[card.value] || 0).toFixed(6) }}</template>
              <template v-else>{{ overview[card.value] || 0 }}</template>
            </p>
            <p v-if="card.subLeftKey" class="stat-sub">
              <span :style="{ color: card.subLeftColor }">{{ card.subLeft }} {{ formatTokens(overview[card.subLeftKey]) }}</span>
              <span style="color:var(--text-muted);margin:0 4px">/</span>
              <span :style="{ color: card.subRightColor }">{{ card.subRight }} {{ formatTokens(overview[card.subRightKey]) }}</span>
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
                  <p style="color:var(--text-muted);font-size:12px;margin-top:12px">缓存命中率 = 缓存命中 Tokens / 总 Tokens</p>
                </div>
              </div>
            </div>
          </NCard>
        </NGi>
        <NGi>
          <NCard class="chart-card">
            <template #header>
              <div class="card-header-with-icon">
                <NIcon size="18" color="var(--accent-primary)"><PieChartSharp /></NIcon>
                <span>模型排行 · Top 10</span>
              </div>
            </template>
            <template #header-extra>
              <NButtonGroup size="tiny">
                <NButton v-for="opt in modelMetricOptions" :key="opt.value" :type="modelMetric === opt.value ? 'primary' : 'default'" @click="modelMetric = opt.value">{{ opt.label }}</NButton>
              </NButtonGroup>
            </template>
            <div v-if="modelStats.length > 0" ref="modelChartContainer" style="height:280px;width:100%"></div>
            <p v-else style="color:var(--text-secondary);text-align:center;padding:40px">暂无模型统计</p>
          </NCard>
        </NGi>
      </NGrid>

      <NCard class="chart-card">
        <template #header>
          <div class="card-header-with-icon">
            <NIcon size="18" color="var(--accent-primary)"><TrendingUpSharp /></NIcon>
            <span>使用趋势</span>
          </div>
        </template>
        <template #header-extra>
          <NButtonGroup size="tiny">
            <NButton v-for="opt in trendMetricOptions" :key="opt.value" :type="trendMetric === opt.value ? 'primary' : 'default'" @click="trendMetric = opt.value">{{ opt.label }}</NButton>
          </NButtonGroup>
        </template>
        <div ref="chartContainer" style="height:330px"></div>
        <p v-if="dailyStats.length === 0" style="color:var(--text-secondary);text-align:center;padding:60px">暂无使用数据</p>
      </NCard>

      <NCard class="chart-card">
        <template #header>
          <div class="card-header-with-icon">
            <NIcon size="18" color="var(--accent-primary)"><TrendingUpSharp /></NIcon>
            <span>请求延迟分位数</span>
          </div>
        </template>
        <template #header-extra>
          <NButtonGroup size="tiny">
            <NButton v-for="opt in latencyMetricOptions" :key="opt.value" :type="latencyMetric === opt.value ? 'primary' : 'default'" @click="latencyMetric = opt.value">{{ opt.label }}</NButton>
          </NButtonGroup>
        </template>
        <div v-if="latencyStats.some((s: any) => s.sample_count > 0)" ref="latencyChartContainer" style="height:320px;width:100%"></div>
        <p v-else style="color:var(--text-secondary);text-align:center;padding:44px 12px;margin:0">当前范围暂无有效延迟样本</p>
        <p style="font-size:12px;color:var(--text-muted);margin:8px 0 0">
          首响应延迟使用 usage_records.latency_ms（流式首个输出增量/非流式首响应）；总耗时使用 total_duration_ms。仅有样本的时间桶显示数据点，样本数仅包含两项耗时均有效的请求。
        </p>
      </NCard>

      <NCard class="chart-card">
        <template #header>
          <div class="card-header-with-icon">
            <NIcon size="18" color="var(--accent-primary)"><TrendingUpSharp /></NIcon>
            <span>最近 5 分钟模型生成速度</span>
          </div>
        </template>
        <template #header-extra>
          <span style="font-size:12px;color:var(--text-muted)">浅色条表示样本少于 3 次</span>
        </template>
        <div v-if="tpsStats.length > 0" ref="tpsChartContainer" style="height:280px;width:100%"></div>
        <p v-else style="color:var(--text-secondary);text-align:center;padding:36px 12px;margin:0">
          暂无有效样本；只统计上游返回输出 Token 用量的已完成流式请求。
        </p>
      </NCard>

      <NCard class="chart-card">
        <template #header>
          <div class="card-header-with-icon">
            <NIcon size="18" color="#26a641"><GridSharp /></NIcon>
            <span>年度 Token 热力图 <span style="font-size:12px;color:var(--text-muted);font-weight:400">(过去一年每日 Tokens 用量)</span></span>
          </div>
        </template>
        <div style="padding:12px 0">
          <CalendarHeatmap
            v-if="yearHeatmap.length > 0"
            :values="heatmapValues"
            :end-date="heatmapEndDate"
            :range-color="isDarkTheme ? ['#00000000', '#252525', '#14532d', '#15803d', '#22c55e', '#4ade80'] : ['#00000000', '#f4f4f5', '#dcfce7', '#86efac', '#22c55e', '#15803d']"
            :max="heatmapMax"
            tooltip-unit="tokens"
            :tooltip-formatter="(v: any) => v.date + ': ' + formatTokens(v.count) + ' tokens'"
            :round="2"
            :dark-mode="isDarkTheme"
            no-data-text="无数据"
          />
          <p v-else style="color:var(--text-secondary);text-align:center;padding:30px">暂无数据</p>
        </div>
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
/* 保持同行 stat 卡片等高：NGi stretch + NCard height:100% */
:deep(.n-gi) { align-items: stretch; }
:deep(.n-card.n-card--hoverable) { height: 100%; }
.n-card.stat-card {
  height: 100%;
}
.stat-card {
  padding: 8px;
  cursor: default;
  transition: transform 0.2s, box-shadow 0.2s;
  text-align: center;
  min-height: 130px;
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
  color: var(--text-secondary);
  font-size: 13px;
  margin: 0 0 6px 0;
}
.stat-sub {
  color: var(--text-secondary);
  font-size: 12px;
  margin: 6px 0 0 0;
}
.stat-value {
  font-size: 26px;
  font-weight: 700;
  margin: 0;
  background: linear-gradient(135deg, var(--text-primary), var(--text-secondary));
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
  border-bottom: 1px solid var(--border-color);
}
.cache-stat-label { color: var(--text-secondary); font-size: 13px; }
.cache-stat-value { color: var(--text-primary); font-size: 14px; font-weight: 600; }

/* 响应式 */
@media (max-width: 767px) {
  .stat-value { font-size: 22px; }
  .cache-stat-row { flex-direction: column; gap: 2px; }
}
</style>
