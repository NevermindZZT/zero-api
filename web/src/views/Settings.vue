<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NCard, NForm, NFormItem, NInput, NInputNumber, NButton, NSpace,
  NSpin, NIcon, NAlert, NSwitch, useMessage,
} from 'naive-ui'
import { SettingsSharp } from '@vicons/ionicons5'
import { proxyApi } from '@/api'

const message = useMessage()
const loading = ref(true)
const saving = ref(false)

const config = ref({
  request_timeout_seconds: 60,
  probe_api_key: '',
  failover_enabled: true,
})

onMounted(async () => {
  await loadConfig()
})

async function loadConfig() {
  loading.value = true
  try {
    const res = await proxyApi.getConfig()
    config.value = {
      request_timeout_seconds: res.data.request_timeout_seconds || 60,
      probe_api_key: res.data.probe_api_key || '',
      failover_enabled: res.data.failover_enabled !== false,
    }
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  try {
    // 读取完整配置再合并，避免覆盖其他字段
    const fullRes = await proxyApi.getConfig()
    const fullConfig = fullRes.data
    fullConfig.request_timeout_seconds = config.value.request_timeout_seconds
    fullConfig.probe_api_key = config.value.probe_api_key
    fullConfig.failover_enabled = config.value.failover_enabled
    await proxyApi.updateConfig(fullConfig)
    message.success('设置已保存')
  } catch (e: any) {
    message.error(e.response?.data?.error || '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><SettingsSharp /></NIcon>
          系统设置
        </h2>
      </div>

      <NCard title="请求超时">
        <NAlert type="info" style="margin-bottom:12px">
          控制中转站向上游 LLM API 发起请求的超时时间。超过此时间未收到响应，会触发渠道熔断回落。
        </NAlert>
        <NForm label-placement="left" label-width="120">
          <NFormItem label="请求超时">
            <NInputNumber
              v-model:value="config.request_timeout_seconds"
              :min="10"
              :max="300"
              :step="10"
              style="width:200px"
              placeholder="60"
            />
            <span style="color:#94a3b8;font-size:13px;margin-left:8px">秒（10-300）</span>
          </NFormItem>
        </NForm>
      </NCard>

      <NCard title="渠道回落">
        <NAlert type="info" style="margin-bottom:12px">
          当多个渠道配置了相同模型时，请求会按渠道优先级使用。如果优先级高的渠道请求失败，会自动回落到下一个渠道。启用熔断后，失败的渠道会被暂时隔离，避免重复请求导致的超时等待。
        </NAlert>
        <NForm label-placement="left" label-width="120">
          <NFormItem label="全局开关">
            <NSwitch v-model:value="config.failover_enabled" />
            <span style="color:#94a3b8;font-size:13px;margin-left:8px">
              关闭后所有渠道的熔断回落机制将停止（每个渠道仍可单独控制）
            </span>
          </NFormItem>
        </NForm>
      </NCard>

      <NCard title="熔断探测">
        <NAlert type="info" style="margin-bottom:12px">
          当渠道请求失败后，会进入冷却期。冷却到期后，使用探针 API Key 和渠道配置的测试模型发起一个极小请求（content: "hi", max_tokens: 1）来验证渠道是否恢复。
          <br /><br />
          <strong>注意事项：</strong>
          <ul style="margin:8px 0 0;padding-left:20px">
            <li>探针 API Key 应为中转站自身的密钥（管理面板创建的 sk-xxx），而非渠道自身的 API Key</li>
            <li>探针请求会记录使用量（request_model 带 <code>__probe__</code> 前缀）</li>
            <li>冷却时长采用指数退避：5分钟 → 10分钟 → 20分钟 → 40分钟</li>
          </ul>
        </NAlert>
        <NForm label-placement="left" label-width="120">
          <NFormItem label="探针 API Key">
            <NInput
              v-model:value="config.probe_api_key"
              type="password"
              show-password-on="click"
              placeholder="留空则使用管理后台 API Key"
              style="width:320px"
            />
          </NFormItem>
        </NForm>
      </NCard>

      <NSpace>
        <NButton type="primary" :loading="saving" @click="saveConfig">保存设置</NButton>
      </NSpace>
    </NSpace>
  </NSpin>
</template>
