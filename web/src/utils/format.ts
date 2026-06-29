/**
 * 格式化日期时间字符串为本地时区显示
 *
 * 后端返回的 created_at 为 UTC ISO8601 格式（如 "2024-01-15T14:30:00Z"），
 * 直接显示会与用户本地时区不一致。
 * 使用 toLocaleString 转换为当前时区可读格式。
 */
export function formatDateTime(isoStr: string | undefined | null): string {
  if (!isoStr) return '-'
  try {
    const d = new Date(isoStr)
    if (isNaN(d.getTime())) return isoStr
    return d.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    })
  } catch {
    return isoStr
  }
}
