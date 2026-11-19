import type { APIKeyOption } from '@/api'

export interface DashboardFilter {
  apiKeyId?: number
  start: string
  end: string
  granularity: 'hour' | 'day' | 'month'
  tzOffset: number
}

// Key IDs distinguish duplicate names and remain visible after a key is deleted.
export function apiKeyOptionLabel(key: APIKeyOption): string {
  if (key.deleted) return `已删除密钥 #${key.id}`
  return `${key.name} (#${key.id})${key.enabled ? '' : '（已禁用）'}`
}

export function createLatestRequestGate() {
  let revision = 0
  return {
    issue() {
      const ownRevision = ++revision
      return () => revision === ownRevision
    },
    invalidate() { revision++ },
    current() { return revision },
  }
}

// All dashboard statistics use the same captured key and time range.
export async function fetchDashboardData(api: typeof import('@/api').usageApi, filter: DashboardFilter) {
  const { apiKeyId, start, end, granularity, tzOffset } = filter
  const [overview, daily, records, models, latency, heatmap] = await Promise.all([
    api.overview(apiKeyId, start, end, tzOffset),
    api.daily(start, end, apiKeyId, granularity, tzOffset),
    api.records(apiKeyId, start, end, 200, tzOffset),
    api.byModel(start, end, apiKeyId, tzOffset),
    api.latency(start, end, apiKeyId, granularity, tzOffset),
    api.yearHeatmap(tzOffset, apiKeyId),
  ])
  return { overview: overview.data, daily: daily.data, records: records.data, models: models.data, latency: latency.data, heatmap: heatmap.data }
}
