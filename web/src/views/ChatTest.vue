<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NButton, NCard, NForm, NFormItem, NInput, NSelect, NAlert, NIcon, NSwitch } from 'naive-ui'
import { ChatbubbleEllipsesSharp } from '@vicons/ionicons5'
import api, { chatTestApi } from '@/api'
import { extractResponseContent, formatChatError, responseProblem } from '@/api/chatTest'

const loading = ref(false)
const modelsLoading = ref(false)
const apiKeys = ref<any[]>([])
const models = ref<any[]>([])
const selectedKey = ref<string | null>(null)
const selectedModel = ref<string | null>(null)
const selectedProtocol = ref('openai')
const prompt = ref('')
const responseText = ref('')
const errorText = ref('')
const requestId = ref('')
const completed = ref(false)
const cancelled = ref(false)
const streamEnabled = ref(false)
const streamingAbort = ref<AbortController | null>(null)
let generation = 0
let modelGeneration = 0

const protocolOptions = [
  { label: 'OpenAI Chat (/v1/chat/completions)', value: 'openai' },
  { label: 'Anthropic (/v1/messages)', value: 'anthropic' },
  { label: 'OpenAI Responses (/v1/responses)', value: 'responses' },
]
const keyOptions = computed(() => apiKeys.value.map((item: any) => ({
  label: `${item.name} (${item.key?.substring(0, 12)}...)`, value: item.key,
})))
const modelOptions = computed(() => {
  const seen = new Set<string>()
  return models.value.filter((item: any) => {
    if (seen.has(item.id)) return false
    seen.add(item.id)
    return true
  }).map((item: any) => ({ label: item.id, value: item.id }))
})
const responsePlaceholder = computed(() => {
  if (loading.value) return '等待模型返回内容…'
  if (cancelled.value) return '请求已取消'
  if (completed.value) return '请求已完成，但未返回可显示的文本（可能仅包含工具调用或其他输出）。'
  if (errorText.value) return '请求未成功完成，请查看上方错误信息。'
  return '暂无响应'
})

onMounted(async () => {
  try { const res = await api.get('/api-keys'); apiKeys.value = (res.data || []).filter((item: any) => item.enabled) }
  catch (err: any) { errorText.value = formatChatError(err.response?.data ?? err, '加载 API Key 失败') }
})
watch(selectedKey, async (value) => {
  const version = ++modelGeneration
  models.value = []; selectedModel.value = null; modelsLoading.value = !!value
  if (!value) return
  try {
    const res = await chatTestApi.models(value)
    if (version === modelGeneration) models.value = res.data?.data || []
  } catch (err: any) {
    if (version === modelGeneration) errorText.value = formatChatError(err.response?.data ?? err, '加载模型失败')
  } finally { if (version === modelGeneration) modelsLoading.value = false }
})
function cancelStream() {
  generation++
  streamingAbort.value?.abort()
  streamingAbort.value = null
  loading.value = false
  cancelled.value = true
}
onUnmounted(() => { generation++; modelGeneration++; streamingAbort.value?.abort() })

function sendMessage() {
  if (loading.value || !selectedKey.value || !selectedModel.value || !prompt.value.trim()) return
  const key = selectedKey.value, model = selectedModel.value, content = prompt.value.trim(), protocol = selectedProtocol.value
  const version = ++generation
  errorText.value = ''; responseText.value = ''; requestId.value = ''; completed.value = false; cancelled.value = false
  loading.value = true
  if (streamEnabled.value) {
    streamingAbort.value = chatTestApi.chatStream(key, model, content, protocol,
      (text) => { if (version === generation) responseText.value += text },
      () => { if (version === generation) { loading.value = false; completed.value = true; streamingAbort.value = null } },
      (error) => { if (version === generation) { errorText.value = error; loading.value = false; streamingAbort.value = null } },
      (id) => { if (version === generation) requestId.value = id },
    )
    return
  }
  chatTestApi.chat(key, model, content, protocol).then((res) => {
    if (version !== generation) return
    requestId.value = String(res.headers['x-request-id'] || '')
    const text = extractResponseContent(res.data, protocol)
    if (text !== null) responseText.value = text
    const problem = responseProblem(res.data)
    if (problem) errorText.value = problem
    else {
      completed.value = true
      if (text === null) responseText.value = JSON.stringify(res.data, null, 2)
    }
  }).catch((err: any) => {
    if (version !== generation) return
    requestId.value = String(err.response?.headers?.['x-request-id'] || '')
    errorText.value = formatChatError(err.response?.data ?? err, '请求失败')
  }).finally(() => { if (version === generation) loading.value = false })
}
</script>

<template>
  <div style="display:flex;flex-direction:column;gap:16px;width:100%">
    <div class="page-header">
      <div>
        <h2>
          <NIcon size="20" color="var(--accent-primary)" style="vertical-align:-2px;margin-right:6px"><ChatbubbleEllipsesSharp /></NIcon>
          Chat 测试
        </h2>
        <p class="page-subtitle">通过中转接口直接发起测试请求</p>
      </div>
    </div>

    <NCard title="请求参数" style="min-width:100%;width:100%">
      <NForm label-placement="top">
        <NFormItem label="API Key">
          <NSelect v-model:value="selectedKey" :options="keyOptions" :disabled="loading" placeholder="选择一个已启用的 API Key" :loading="modelsLoading" style="width:100%" />
        </NFormItem>
        <NFormItem label="模型">
          <NSelect v-model:value="selectedModel" :options="modelOptions" placeholder="先选择 API Key 再加载模型" :disabled="loading || !selectedKey || modelsLoading" :loading="modelsLoading" style="width:100%" />
        </NFormItem>
        <NFormItem label="API 接口">
          <NSelect v-model:value="selectedProtocol" :options="protocolOptions" :disabled="loading" placeholder="选择下游 API 接口" style="width:100%" />
        </NFormItem>
        <NFormItem label="Prompt">
          <NInput v-model:value="prompt" type="textarea" :autosize="{ minRows: 6, maxRows: 12 }" placeholder="输入测试内容" style="width:100%" />
        </NFormItem>
        <div style="display:flex;align-items:center;gap:12px;margin-bottom:12px">
          <NSwitch v-model:value="streamEnabled" :disabled="loading" />
          <span style="color:var(--text-secondary);font-size:13px">流式传输（stream: true）</span>
        </div>
        <div style="display:flex;gap:8px">
          <NButton type="primary" :disabled="!selectedKey || !selectedModel || !prompt.trim() || loading" :loading="loading && !streamEnabled" @click="sendMessage" style="flex:1">发送测试</NButton>
          <NButton v-if="loading && streamingAbort" type="warning" @click="cancelStream">取消</NButton>
        </div>
      </NForm>
    </NCard>

    <div v-if="errorText" style="width:100%">
      <NAlert type="error" :show-icon="false" :title="errorText" />
    </div>

    <NCard title="响应结果" style="min-width:100%;width:100%">
      <div v-if="requestId" style="margin-bottom:12px;color:var(--text-secondary);font-size:12px;word-break:break-all">请求 ID：{{ requestId }}</div>
      <pre style="white-space:pre-wrap;word-break:break-word;min-height:160px;color:var(--text-primary)">{{ responseText || responsePlaceholder }}</pre>
    </NCard>
  </div>
</template>