<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NButton, NCard, NForm, NFormItem, NInput, NSelect, NSpin, NAlert, NIcon, NSwitch } from 'naive-ui'
import { ChatbubbleEllipsesSharp } from '@vicons/ionicons5'
import api, { chatTestApi } from '@/api'

const loading = ref(false)
const modelsLoading = ref(false)
const apiKeys = ref<any[]>([])
const models = ref<any[]>([])
const selectedKey = ref<string | null>(null)
const selectedModel = ref<string | null>(null)
const selectedProtocol = ref<string>('openai')
const prompt = ref('')
const responseText = ref('')
const errorText = ref('')
const streamEnabled = ref(false)
const streamingAbort = ref<AbortController | null>(null)

// 测试使用的下游 API 接口
const protocolOptions = [
  { label: 'OpenAI Chat (/v1/chat/completions)', value: 'openai' },
  { label: 'Anthropic (/v1/messages)', value: 'anthropic' },
  { label: 'OpenAI Responses (/v1/responses)', value: 'responses' },
]

const keyOptions = computed(() => apiKeys.value.map((item: any) => ({
  label: `${item.name} (${item.key?.substring(0, 12)}...)`,
  value: item.key,
})))

const modelOptions = computed(() => {
  const seen = new Set<string>()
  return models.value.filter((item: any) => {
    if (seen.has(item.id)) return false
    seen.add(item.id)
    return true
  }).map((item: any) => ({
    label: item.id,
    value: item.id,
  }))
})

onMounted(async () => {
  const res = await api.get('/api-keys')
  apiKeys.value = res.data || []
})

watch(selectedKey, async (value) => {
  models.value = []
  selectedModel.value = null
  if (!value) return
  modelsLoading.value = true
  try {
    const res = await chatTestApi.models(value)
    models.value = res.data?.data || []
  } catch (err: any) {
    errorText.value = err.response?.data?.error || err.message || '加载模型失败'
  } finally {
    modelsLoading.value = false
  }
})

function cancelStream() {
  if (streamingAbort.value) {
    streamingAbort.value.abort()
    streamingAbort.value = null
    loading.value = false
  }
}

function sendMessage() {
  if (!selectedKey.value || !selectedModel.value || !prompt.value.trim()) return

  // 清除上一次的错误
  errorText.value = ''

  if (streamEnabled.value) {
    // === 流式模式 ===
    responseText.value = ''
    loading.value = true
    streamingAbort.value = chatTestApi.chatStream(
      selectedKey.value,
      selectedModel.value,
      prompt.value.trim(),
      selectedProtocol.value,
      (text) => { responseText.value += text },
      () => { loading.value = false; streamingAbort.value = null },
      (err) => { errorText.value = err; loading.value = false; streamingAbort.value = null },
    )
  } else {
    // === 非流式模式 ===
    loading.value = true
    responseText.value = ''
    chatTestApi.chat(selectedKey.value, selectedModel.value, prompt.value.trim(), selectedProtocol.value)
      .then((res) => {
        const content = extractResponseContent(res.data, selectedProtocol.value)
        if (content !== null && content !== undefined) {
          responseText.value = content
        } else {
          responseText.value = JSON.stringify(res.data, null, 2)
        }
      })
      .catch((err: any) => {
        errorText.value = err.response?.data?.error || err.message || '请求失败'
      })
      .finally(() => { loading.value = false })
  }
}

// 从各协议的非流式响应中提取文本内容
function extractResponseContent(data: any, protocol: string): string | null {
  if (!data) return null
  if (protocol === 'anthropic') {
    const content = data?.content
    if (Array.isArray(content)) {
      return content.map((item: any) => item?.text || '').join('')
    }
    if (typeof content === 'string') return content
    return null
  }
  if (protocol === 'responses') {
    const output = data?.output
    if (Array.isArray(output)) {
      return output.map((item: any) => {
        if (item?.type === 'output_text') return item.text || ''
        // message 块：content 可能是 [{type:'output_text',text}] 或纯字符串数组
        const content = item?.content
        if (Array.isArray(content)) {
          return content.map((c: any) => (typeof c === 'string' ? c : c?.text || '')).join('')
        }
        return ''
      }).join('')
    }
    return null
  }
  // openai
  const content = data?.choices?.[0]?.message?.content
  if (Array.isArray(content)) {
    return content.map((item: any) => item?.text || JSON.stringify(item)).join('\n')
  }
  if (typeof content === 'string') return content
  return null
}
</script>

<template>
  <div style="display:flex;flex-direction:column;gap:16px;width:100%">
    <div class="page-header">
      <div>
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><ChatbubbleEllipsesSharp /></NIcon>
          Chat 测试
        </h2>
        <p class="page-subtitle">通过中转接口直接发起测试请求</p>
      </div>
    </div>

    <NCard title="请求参数" style="min-width:100%;width:100%">
      <NForm label-placement="top">
        <NFormItem label="API Key">
          <NSelect v-model:value="selectedKey" :options="keyOptions" placeholder="选择一个已启用的 API Key" :loading="modelsLoading" style="width:100%" />
        </NFormItem>
        <NFormItem label="模型">
          <NSelect v-model:value="selectedModel" :options="modelOptions" placeholder="先选择 API Key 再加载模型" :disabled="!selectedKey || modelsLoading" :loading="modelsLoading" style="width:100%" />
        </NFormItem>
        <NFormItem label="API 接口">
          <NSelect v-model:value="selectedProtocol" :options="protocolOptions" placeholder="选择下游 API 接口" style="width:100%" />
        </NFormItem>
        <NFormItem label="Prompt">
          <NInput v-model:value="prompt" type="textarea" :autosize="{ minRows: 6, maxRows: 12 }" placeholder="输入测试内容" style="width:100%" />
        </NFormItem>
        <div style="display:flex;align-items:center;gap:12px;margin-bottom:12px">
          <NSwitch v-model:value="streamEnabled" />
          <span style="color:#a0aec0;font-size:13px">流式传输（stream: true）</span>
        </div>
        <div style="display:flex;gap:8px">
          <NButton type="primary" :disabled="!selectedKey || !selectedModel || !prompt.trim() || loading" :loading="loading && !streamEnabled" @click="sendMessage" style="flex:1">发送测试</NButton>
          <NButton v-if="loading && streamEnabled" type="warning" @click="cancelStream">取消</NButton>
        </div>
      </NForm>
    </NCard>

    <div v-if="errorText" style="width:100%">
      <NAlert type="error" :show-icon="false" :title="errorText" />
    </div>

    <NCard title="响应结果" style="min-width:100%;width:100%">
      <pre style="white-space:pre-wrap;word-break:break-word;min-height:160px;color:#e2e8f0">{{ responseText || '暂无响应' }}</pre>
    </NCard>
  </div>
</template>