<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NButton, NCard, NForm, NFormItem, NInput, NSelect, NSpin, NAlert, NIcon } from 'naive-ui'
import { ChatbubbleEllipsesSharp } from '@vicons/ionicons5'
import api, { chatTestApi } from '@/api'

const loading = ref(false)
const modelsLoading = ref(false)
const apiKeys = ref<any[]>([])
const models = ref<any[]>([])
const selectedKey = ref<string | null>(null)
const selectedModel = ref<string | null>(null)
const prompt = ref('')
const responseText = ref('')
const errorText = ref('')

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

async function sendMessage() {
  if (!selectedKey.value || !selectedModel.value || !prompt.value.trim()) return
  loading.value = true
  responseText.value = ''
  errorText.value = ''
  try {
    const res = await chatTestApi.chat(selectedKey.value, selectedModel.value, prompt.value.trim())
    const content = res.data?.choices?.[0]?.message?.content
    if (Array.isArray(content)) {
      responseText.value = content.map((item: any) => item?.text || JSON.stringify(item)).join('\n')
    } else if (typeof content === 'string') {
      responseText.value = content
    } else {
      responseText.value = JSON.stringify(res.data, null, 2)
    }
  } catch (err: any) {
    errorText.value = err.response?.data?.error || err.message || '请求失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <NSpin :show="loading || modelsLoading" style="width:100%">
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
            <NSelect v-model:value="selectedKey" :options="keyOptions" placeholder="选择一个已启用的 API Key" style="width:100%" />
          </NFormItem>
          <NFormItem label="模型">
            <NSelect v-model:value="selectedModel" :options="modelOptions" placeholder="先选择 API Key 再加载模型" :disabled="!selectedKey" style="width:100%" />
          </NFormItem>
          <NFormItem label="Prompt">
            <NInput v-model:value="prompt" type="textarea" :autosize="{ minRows: 6, maxRows: 12 }" placeholder="输入测试内容" style="width:100%" />
          </NFormItem>
          <NButton type="primary" :disabled="!selectedKey || !selectedModel || !prompt.trim()" @click="sendMessage" style="width:100%">发送测试</NButton>
        </NForm>
      </NCard>

      <div v-if="errorText" style="width:100%">
        <NAlert type="error" :show-icon="false" :title="errorText" />
      </div>

      <NCard title="响应结果" style="min-width:100%;width:100%">
        <pre style="white-space:pre-wrap;word-break:break-word;min-height:160px;color:#e2e8f0">{{ responseText || '暂无响应' }}</pre>
      </NCard>
    </div>
  </NSpin>
</template>