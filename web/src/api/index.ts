import axios from 'axios'

const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

// 请求拦截器：自动添加 token
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 响应拦截器：处理 401
api.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('user')
      window.location.href = '/login'
    }
    const msg = err.response?.data?.error || err.message
    console.error('[API Error]', msg)
    return Promise.reject(err)
  }
)

export default api

// ===== Channel API =====
export const channelApi = {
  list: () => api.get('/channels'),
  get: (id: number) => api.get(`/channels/${id}`),
  create: (data: any) => api.post('/channels', data),
  update: (id: number, data: any) => api.put(`/channels/${id}`, data),
  delete: (id: number) => api.delete(`/channels/${id}`),
  test: (id: number) => api.post(`/channels/${id}/test`),
  syncModels: (id: number) => api.post(`/channels/${id}/sync`),
}

// ===== Model API =====
export const modelApi = {
  list: (channelId?: number) =>
    api.get('/models', { params: { channel_id: channelId || undefined } }),
  get: (id: number) => api.get(`/models/${id}`),
  update: (id: number, data: any) => api.put(`/models/${id}`, data),
  delete: (id: number) => api.delete(`/models/${id}`),
  toggle: (id: number) => api.post(`/models/${id}/toggle`),
  batch: (action: string, ids: number[]) => api.post('/models/batch', { action, ids }),
}

export const chatTestApi = {
  models: (apiKey: string) => axios.get('/v1/models', {
    headers: { Authorization: `Bearer ${apiKey}` },
    timeout: 30000,
  }),
  chat: (apiKey: string, model: string, content: string) => axios.post('/v1/chat/completions', {
    model,
    messages: [
      { role: 'user', content },
    ],
  }, {
    headers: { Authorization: `Bearer ${apiKey}` },
    timeout: 120000,
  }),
  chatStream: (apiKey: string, model: string, content: string, onData: (text: string) => void, onDone: () => void, onError: (err: string) => void): AbortController => {
    const controller = new AbortController()
    fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey}` },
      body: JSON.stringify({ model, messages: [{ role: 'user', content }], stream: true }),
      signal: controller.signal,
    }).then(async (response) => {
      if (!response.ok) {
        const errBody = await response.text()
        onError(`HTTP ${response.status}: ${errBody}`)
        return
      }
      const reader = response.body!.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          const payload = line.slice(6).trim()
          if (payload === '[DONE]') continue
          try {
            const parsed = JSON.parse(payload)
            // OpenAI SSE: choices[0].delta.content
            const delta = parsed?.choices?.[0]?.delta?.content
            // Anthropic SSE: delta.text
            const text = parsed?.delta?.text
            const content = delta || text || ''
            if (content) onData(content)
          } catch { /* skip malformed SSE */ }
        }
      }
      onDone()
    }).catch((err) => {
      if (err.name !== 'AbortError') onError(err.message || '流式请求失败')
    })
    return controller
  },
}

// ===== Usage API =====
export const usageApi = {
  overview: (apiKeyId?: number, start?: string, end?: string, tzOffset?: number) =>
    api.get('/stats/overview', { params: { api_key_id: apiKeyId || undefined, start, end, tz_offset: tzOffset } }),
  daily: (start?: string, end?: string, apiKeyId?: number, granularity?: string, tzOffset?: number) =>
    api.get('/stats/daily', { params: { start, end, api_key_id: apiKeyId || undefined, granularity, tz_offset: tzOffset } }),
  byModel: (start?: string, end?: string, apiKeyId?: number, tzOffset?: number) =>
    api.get('/stats/by-model', { params: { start, end, api_key_id: apiKeyId || undefined, tz_offset: tzOffset } }),
  yearHeatmap: (tzOffset?: number) => api.get('/stats/year-heatmap', { params: { tz_offset: tzOffset } }),
  records: (apiKeyId?: number, start?: string, end?: string, limit?: number, tzOffset?: number) =>
    api.get('/usage/records', { params: { api_key_id: apiKeyId || undefined, start, end, limit, tz_offset: tzOffset } }),
}

// ===== Proxy Config API =====
export const proxyApi = {
  getConfig: () => api.get('/proxy/config'),
  updateConfig: (data: any) => api.put('/proxy/config', data),
  downloadCert: (format?: string) => api.get('/proxy/cert/download', {
    params: { format: format || 'pem' },
    responseType: 'blob',
  }),
}

// ===== Skill API =====
export interface SkillFileEntry {
  path: string
  content?: string
  size?: number
}

export interface SkillData {
  id?: number
  name: string
  description?: string
  type?: string
  source_url?: string
  tags?: string[]
  files?: SkillFileEntry[]
  enabled?: boolean
}

export const skillApi = {
  list: (params?: { q?: string; tag?: string }) => api.get('/skills', { params }),
  getTags: () => api.get('/skills/tags'),
  get: (id: number) => api.get(`/skills/${id}`),
  getFile: (id: number, path: string) => api.get(`/skills/${id}/files/${encodeURIComponent(path)}`),
  create: (data: SkillData) => api.post('/skills', data),
  update: (id: number, data: SkillData) => api.put(`/skills/${id}`, data),
  delete: (id: number) => api.delete(`/skills/${id}`),
  importFromGitHub: (url: string, githubToken?: string) => api.post('/skills/import-github', {
    source_url: url,
    ...(githubToken ? { github_token: githubToken } : {}),
  }),
  upload: (formData: FormData) => api.post('/skills/upload', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }),
  uploadFolder: (formData: FormData) => api.post('/skills/upload-folder', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }),
  importRepo: (repoUrl: string, path?: string, githubToken?: string) => api.post('/skills/import-repo', {
    repo_url: repoUrl,
    ...(path ? { path } : {}),
    ...(githubToken ? { github_token: githubToken } : {}),
  }),
  syncRepo: (repoUrl: string, path?: string, githubToken?: string) => api.post('/skills/sync-repo', {
    repo_url: repoUrl,
    ...(path ? { path } : {}),
    ...(githubToken ? { github_token: githubToken } : {}),
  }),
  checkUpdates: () => api.get('/skills/check-updates'),
}

export const skillCombinationApi = {
  list: () => api.get('/skill-combinations'),
  get: (id: number) => api.get(`/skill-combinations/${id}`),
  create: (data: { name: string; description?: string }) => api.post('/skill-combinations', data),
  update: (id: number, data: { name?: string; description?: string }) => api.put(`/skill-combinations/${id}`, data),
  delete: (id: number) => api.delete(`/skill-combinations/${id}`),
  addSkill: (id: number, skillId: number) => api.post(`/skill-combinations/${id}/skills`, { skill_id: skillId }),
  removeSkill: (id: number, skillId: number) => api.delete(`/skill-combinations/${id}/skills/${skillId}`),
  getSkills: (id: number) => api.get(`/skill-combinations/${id}/skills`),
}

// ===== MCP Config API =====
export const mcpApi = {
  status: () => api.get('/mcp/status'),
  updateGitHubToken: (token: string) => api.put('/mcp/github-token', { github_token: token }),
}
