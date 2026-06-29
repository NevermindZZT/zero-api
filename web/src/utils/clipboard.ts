/**
 * 复制文本到剪贴板
 *
 * navigator.clipboard.writeText 仅能在安全上下文（HTTPS/localhost）中使用，
 * 部署到 HTTP 环境时会失败。此函数提供兼容性 fallback：
 *   1. 优先使用 Clipboard API（安全上下文）
 *   2. 降级使用 document.execCommand('copy')（通用方案）
 *
 * @param text 要复制的文本
 * @returns 是否复制成功
 */
export function copyToClipboard(text: string): Promise<boolean> {
  // 方案一：Clipboard API（需安全上下文）
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopy(text))
  }
  // 方案二：fallback
  return fallbackCopy(text)
}

function fallbackCopy(text: string): Promise<boolean> {
  return new Promise((resolve) => {
    try {
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      textarea.style.width = '0'
      textarea.style.height = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
      resolve(true)
    } catch {
      resolve(false)
    }
  })
}
