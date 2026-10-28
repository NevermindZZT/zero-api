export function buildChatRequest(model: string, content: string, protocol: string, stream: boolean): { url: string; body: any } {
  const base = { model, stream }
  if (protocol === 'responses') return { url: '/v1/responses', body: { ...base, input: [{ role: 'user', content }] } }
  if (protocol === 'anthropic') return { url: '/v1/messages', body: { ...base, max_tokens: 4096, messages: [{ role: 'user', content }] } }
  return { url: '/v1/chat/completions', body: { ...base, messages: [{ role: 'user', content }] } }
}

function contentText(content: any): string {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content.map((part: any) => {
    if (typeof part === 'string') return part
    if (part?.type === 'refusal') return typeof part.refusal === 'string' ? part.refusal : ''
    return typeof part?.text === 'string' ? part.text : ''
  }).join('')
}

export function extractResponseContent(data: any, protocol: string): string | null {
  if (!data || typeof data !== 'object') return null
  if (protocol === 'anthropic') return data.content === undefined ? null : contentText(data.content)
  if (protocol === 'responses') {
    if (typeof data.output_text === 'string') return data.output_text
    if (!Array.isArray(data.output)) return null
    return data.output.map((item: any) => {
      if (item?.type === 'output_text') return typeof item.text === 'string' ? item.text : ''
      if (item?.type === 'refusal') return typeof item.refusal === 'string' ? item.refusal : ''
      return contentText(item?.content)
    }).join('')
  }
  const content = data.choices?.[0]?.message?.content
  return content === undefined ? null : contentText(content)
}

export function formatChatError(value: any, fallback = '请求失败'): string {
  const detail = value?.error ?? value?.response?.error ?? value
  if (typeof detail === 'string' && detail.trim()) return detail.slice(0, 2000)
  if (typeof detail?.message === 'string' && detail.message.trim()) return detail.message.slice(0, 2000)
  if (typeof detail?.code === 'string' && detail.code.trim()) return detail.code.slice(0, 200)
  return fallback
}

export function responseProblem(data: any): string | null {
  if (data?.error) return formatChatError(data)
  if (['failed', 'error', 'cancelled'].includes(data?.status)) return formatChatError(data, '上游响应失败')
  if (data?.status === 'incomplete') {
    const reason = data?.incomplete_details?.reason
    return typeof reason === 'string' ? `响应未完成：${reason.slice(0, 200)}` : '响应未完成'
  }
  return null
}

// Parse SSE frames, not network chunks or isolated data lines. Both event names
// and the Responses JSON type are supported; raw reasoning/tool deltas are not text.
export function createChatStreamParser(protocol: string, onData: (text: string) => void) {
  let line = ''
  let data: string[] = []
  let event = ''
  let skipLF = false
  let completed = false
  let emitted = ''
  let frameBytes = 0
  let firstCharacter = true
  const emit = (text: unknown) => {
    if (typeof text === 'string' && text) { emitted += text; onData(text) }
  }
  const snapshot = (text: unknown) => {
    if (typeof text !== 'string' || !text) return
    if (!emitted) emit(text)
    else if (text.startsWith(emitted)) emit(text.slice(emitted.length))
  }
  const dispatch = () => {
    const payload = data.join('\n')
    const eventType = event
    data = []; event = ''; frameBytes = 0
    if (!payload) {
      if (eventType === 'error') throw new Error('上游返回流式错误')
      return
    }
    if (payload.trim() === '[DONE]') { completed = true; return }
    let parsed: any
    try { parsed = JSON.parse(payload) } catch { throw new Error('无法解析上游 SSE 响应数据') }
    const type = typeof parsed?.type === 'string' ? parsed.type : eventType
    if (eventType === 'error' || type === 'error' || parsed?.error || ['response.failed', 'response.error', 'response.cancelled'].includes(type)) {
      throw new Error(formatChatError(parsed, '上游返回流式错误'))
    }
    if (protocol === 'responses') {
      if (type === 'response.output_text.delta' || type === 'response.refusal.delta') emit(parsed.delta)
      if (type === 'response.output_text.done' || type === 'response.refusal.done') snapshot(parsed.text ?? parsed.refusal)
      if (type === 'response.completed' || type === 'response.incomplete') {
        const response = parsed.response ?? parsed
        snapshot(extractResponseContent(response, 'responses'))
        const problem = responseProblem(response)
        if (problem) throw new Error(problem)
        if (type === 'response.incomplete') throw new Error('响应未完成')
        completed = true
      }
    } else if (protocol === 'anthropic') {
      if (type === 'content_block_delta' && parsed.delta?.type === 'text_delta') emit(parsed.delta.text)
      if (type === 'content_block_start' && parsed.content_block?.type === 'text') emit(parsed.content_block.text)
      if (type === 'message_stop') completed = true
    } else {
      const choice = parsed?.choices?.[0]
      emit(contentText(choice?.delta?.content))
      if (typeof choice?.finish_reason === 'string' && choice.finish_reason) {
        if (choice.finish_reason === 'error') throw new Error('上游生成失败')
        if (choice.finish_reason === 'length' || choice.finish_reason === 'content_filter') throw new Error(`响应未完成：${choice.finish_reason}`)
        completed = true
      }
    }
  }
  const processLine = () => {
    const value = line; line = ''
    if (!value) { dispatch(); return }
    if (value.startsWith(':')) return
    const colon = value.indexOf(':')
    const name = colon < 0 ? value : value.slice(0, colon)
    let field = colon < 0 ? '' : value.slice(colon + 1)
    if (field.startsWith(' ')) field = field.slice(1)
    if (name === 'data') data.push(field)
    if (name === 'event') event = field
  }
  return {
    push(chunk: string) {
      for (const character of chunk) {
        if (firstCharacter) { firstCharacter = false; if (character === '\uFEFF') continue }
        if (skipLF) { skipLF = false; if (character === '\n') continue }
        if (++frameBytes > 4 * 1024 * 1024) throw new Error('SSE 响应帧过大')
        if (character === '\r') { processLine(); skipLF = true }
        else if (character === '\n') processLine()
        else line += character
      }
    },
    finish() {
      if (line) processLine()
      if (data.length || event) dispatch()
      if (!completed) throw new Error('响应流提前结束，未收到完成事件')
    },
    get completed() { return completed },
  }
}

export function streamChatRequest(
  apiKey: string, model: string, content: string, protocol: string,
  onData: (text: string) => void, onDone: () => void, onError: (error: string) => void,
  onRequestId?: (id: string) => void,
): AbortController {
  const controller = new AbortController()
  const { url, body } = buildChatRequest(model, content, protocol, true)
  void (async () => {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    try {
      const response = await fetch(url, {
        method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey}` },
        body: JSON.stringify(body), signal: controller.signal,
      })
      onRequestId?.(response.headers.get('X-Request-Id') || '')
      const isSSE = response.headers.get('Content-Type')?.toLowerCase().includes('text/event-stream')
      if (!response.ok || !isSSE) {
        const raw = await response.text()
        let parsed: any
        try { parsed = JSON.parse(raw) } catch { parsed = null }
        if (!response.ok) throw new Error(`HTTP ${response.status}: ${formatChatError(parsed, raw.slice(0, 1000) || response.statusText)}`)
        const problem = responseProblem(parsed)
        if (problem) throw new Error(problem)
        const text = extractResponseContent(parsed, protocol)
        if (text === null) throw new Error('服务器未返回可识别的 SSE 或 JSON 响应')
        if (!controller.signal.aborted) { if (text) onData(text); onDone() }
        return
      }
      if (!response.body) throw new Error('服务器未返回响应流')
      reader = response.body.getReader()
      const decoder = new TextDecoder()
      const parser = createChatStreamParser(protocol, onData)
      while (!controller.signal.aborted) {
        const { done, value } = await reader.read()
        if (done) { parser.push(decoder.decode()); parser.finish(); break }
        parser.push(decoder.decode(value, { stream: true }))
        if (parser.completed) break
      }
      if (!controller.signal.aborted) onDone()
    } catch (error: any) {
      if (!controller.signal.aborted) onError(formatChatError(error, '流式请求失败'))
    } finally {
      if (reader) { try { await reader.cancel() } catch { /* transport already closed */ } reader.releaseLock() }
    }
  })()
  return controller
}
