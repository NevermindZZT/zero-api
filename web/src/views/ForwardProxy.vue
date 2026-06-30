<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NCard, NForm, NFormItem, NInput, NButton, NSpace,
  NAlert, NSpin, NIcon, useMessage,
} from 'naive-ui'
import { RocketSharp } from '@vicons/ionicons5'
import { proxyApi, channelApi } from '@/api'

const message = useMessage()
const loading = ref(true)

const config = ref({
  forward_proxy_url: '',
  forward_proxy_user: '',
  forward_proxy_pass: '',
})

// 渠道列表 & 已启用代理的渠道
const channels = ref<any[]>([])
const proxyChannels = ref<any[]>([])

onMounted(async () => {
  await Promise.all([loadConfig(), loadChannels()])
})

async function loadConfig() {
  loading.value = true
  try {
    const res = await proxyApi.getConfig()
    config.value = {
      forward_proxy_url: res.data.forward_proxy_url || '',
      forward_proxy_user: res.data.forward_proxy_user || '',
      forward_proxy_pass: res.data.forward_proxy_pass || '',
    }
  } finally {
    loading.value = false
  }
}

async function loadChannels() {
  try {
    const res = await channelApi.list()
    channels.value = res.data || []
    proxyChannels.value = channels.value.filter((c: any) => c.use_proxy)
  } catch {
    // 静默失败
  }
}

async function saveConfig() {
  try {
    // 读取完整配置再合并，避免覆盖其他字段
    const fullRes = await proxyApi.getConfig()
    const fullConfig = fullRes.data
    fullConfig.forward_proxy_url = config.value.forward_proxy_url
    fullConfig.forward_proxy_user = config.value.forward_proxy_user
    fullConfig.forward_proxy_pass = config.value.forward_proxy_pass
    await proxyApi.updateConfig(fullConfig)
    message.success('出站代理配置已保存')
  } catch (e: any) {
    message.error(e.response?.data?.error || '保存失败')
  }
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><RocketSharp /></NIcon>
          出站代理
        </h2>
      </div>

      <NCard title="代理配置">
        <NAlert type="info" style="margin-bottom:12px">
          配置全局出站代理。在渠道管理中开启「出站代理」开关后，该渠道的上游请求将通过此代理转发。
          支持 HTTP 代理，格式如 <code>http://127.0.0.1:7890</code>。
        </NAlert>
        <NForm label-placement="left" label-width="120">
          <NFormItem label="代理地址">
            <NInput
              v-model:value="config.forward_proxy_url"
              placeholder="http://127.0.0.1:7890"
              style="width:320px"
            />
          </NFormItem>
          <NFormItem label="代理用户名">
            <NInput v-model:value="config.forward_proxy_user" placeholder="可选" style="width:200px" />
          </NFormItem>
          <NFormItem label="代理密码">
            <NInput
              v-model:value="config.forward_proxy_pass"
              type="password"
              show-password-on="click"
              placeholder="可选"
              style="width:200px"
            />
          </NFormItem>
          <NFormItem>
            <NButton type="primary" @click="saveConfig">保存配置</NButton>
          </NFormItem>
        </NForm>
      </NCard>

      <NCard title="使用说明">
        <NAlert type="info">
          <p><strong>1. 配置代理地址</strong>：在上方填写你的 HTTP 代理地址（如 Clash/V2Ray 的本地代理端口）</p>
          <p><strong>2. 启用渠道代理</strong>：在「渠道管理」页面编辑渠道，开启「出站代理」开关</p>
          <p><strong>3. 生效</strong>：之后通过该渠道模型的所有请求将自动通过代理转发到上游 API</p>
          <br />
          <p v-if="proxyChannels.length > 0">
            <strong>已启用代理的渠道：</strong>
            <span v-for="(ch, i) in proxyChannels" :key="ch.id">
              {{ ch.name }}<span v-if="i < proxyChannels.length - 1">、</span>
            </span>
          </p>
          <p v-else style="color:#94a3b8">暂无启用代理的渠道</p>
        </NAlert>
      </NCard>
    </NSpace>
  </NSpin>
</template>
