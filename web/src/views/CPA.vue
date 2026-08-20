<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NForm, NFormItem, NGrid, NGi, NInput, NInputNumber,
  NSpin, NSpace, NSwitch, NTag, useMessage,
} from 'naive-ui'
import { cpaApi } from '@/api'

const message = useMessage()
const loading = ref(true)
const busy = ref(false)
const config = ref<any>({
  enabled: true, auto_start: true, host: '127.0.0.1', port: 8317,
  api_keys: [], proxy_url: '', request_retry: 3, debug: false,
})
const status = ref<any>({})
const update = ref<any>({})
let timer: ReturnType<typeof setInterval> | undefined

async function load() {
  try {
    const [cfg, state] = await Promise.all([cpaApi.getConfig(), cpaApi.status()])
    config.value = { ...config.value, ...cfg.data }
    status.value = state.data
  } catch (error: any) {
    message.error(error.response?.data?.error || '读取 CPA 配置失败')
  } finally {
    loading.value = false
  }
}

async function refreshStatus() {
  try { status.value = (await cpaApi.status()).data } catch { /* 页面保留上次状态 */ }
}

async function save() {
  busy.value = true
  try {
    await cpaApi.saveConfig(config.value)
    message.success('CPA 配置已保存')
    await refreshStatus()
  } catch (error: any) {
    message.error(error.response?.data?.error || '保存失败')
  } finally { busy.value = false }
}

async function action(name: 'start' | 'stop' | 'restart') {
  busy.value = true
  try {
    await cpaApi[name]()
    message.success(name === 'start' ? 'sidecar 已启动' : name === 'stop' ? 'sidecar 已停止' : 'sidecar 已重启')
    await refreshStatus()
  } catch (error: any) {
    message.error(error.response?.data?.error || '操作失败')
  } finally { busy.value = false }
}

async function checkUpdate() {
  busy.value = true
  try {
    update.value = (await cpaApi.checkUpdate()).data
    message.info(update.value.has_update ? `发现新版本 ${update.value.latest_version}` : '当前已是最新版本')
  } catch (error: any) {
    message.error(error.response?.data?.error || '检查更新失败')
  } finally { busy.value = false }
}

async function install(force = false) {
  busy.value = true
  try {
    const result = await cpaApi.installBinary(force)
    message.success(`CLIProxyAPI ${result.data.version} 已安装`)
    await refreshStatus()
  } catch (error: any) {
    message.error(error.response?.data?.error || '安装失败')
  } finally { busy.value = false }
}

onMounted(() => { load(); timer = setInterval(refreshStatus, 10000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>CLIProxyAPI 运行管理</h2>
        <div class="page-subtitle">统一管理多订阅 Sidecar 的运行状态、配置、版本和认证数据目录。</div>
      </div>

      <NAlert type="info">
        zero-api 不修改 CLIProxyAPI 内核。完成任一订阅渠道的 OAuth 登录后，即可通过下方 API Key 将 Sidecar 配置为 OpenAI 兼容渠道。
      </NAlert>

      <NCard title="运行状态">
        <div class="status-row">
          <NTag :type="status.healthy ? 'success' : status.running ? 'warning' : 'default'" round>
            {{ status.healthy ? '运行正常' : status.running ? '进程运行中' : '未运行' }}
          </NTag>
          <span v-if="status.pid">PID {{ status.pid }}</span>
          <span v-if="status.version">{{ status.version.trim() }}</span>
        </div>
        <NSpace style="margin-top: 16px" wrap>
          <NButton type="primary" :loading="busy" :disabled="status.running" @click="action('start')">启动</NButton>
          <NButton :loading="busy" :disabled="!status.running" @click="action('restart')">重启</NButton>
          <NButton type="warning" :loading="busy" :disabled="!status.running" @click="action('stop')">停止</NButton>
          <NButton :loading="busy" @click="refreshStatus">刷新状态</NButton>
        </NSpace>
      </NCard>

      <NCard title="Sidecar 配置">
        <NForm label-placement="left" label-width="130">
          <NGrid :cols="2" :x-gap="24" responsive="screen" item-responsive>
            <NGi span="2 m:1">
              <NFormItem label="启用">
                <NSwitch v-model:value="config.enabled" />
              </NFormItem>
              <NFormItem label="自动启动">
                <NSwitch v-model:value="config.auto_start" />
              </NFormItem>
              <NFormItem label="绑定地址"><NInput v-model:value="config.host" /></NFormItem>
              <NFormItem label="监听端口"><NInputNumber v-model:value="config.port" :min="1" :max="65535" style="width: 100%" /></NFormItem>
              <NFormItem label="API Key">
                <NInput v-model:value="config.api_keys[0]" type="password" show-password-on="click" placeholder="供 zero-api 渠道访问 sidecar" />
              </NFormItem>
            </NGi>
            <NGi span="2 m:1">
              <NFormItem label="出站代理" label-description="支持 http/https/socks5，可带账号密码">
                <NInput v-model:value="config.proxy_url" placeholder="例如 socks5://user:pass@127.0.0.1:1080" />
              </NFormItem>
              <NFormItem label="请求重试"><NInputNumber v-model:value="config.request_retry" :min="0" :max="10" style="width: 100%" /></NFormItem>
              <NFormItem label="调试日志"><NSwitch v-model:value="config.debug" /></NFormItem>
            </NGi>
          </NGrid>
          <NButton type="primary" :loading="busy" @click="save">保存配置</NButton>
        </NForm>
      </NCard>

      <NCard title="二进制与数据目录">
        <div class="paths">
          <div><span>版本</span><strong>{{ status.version?.trim() || '未安装' }}</strong></div>
          <div><span>二进制</span><code>{{ status.bin_path || '-' }}</code></div>
          <div><span>配置文件</span><code>{{ status.config_path || '-' }}</code></div>
          <div><span>认证目录</span><code>{{ status.auth_dir || '-' }}</code></div>
          <div><span>日志</span><code>{{ status.log_path || '-' }}</code></div>
        </div>
        <NSpace style="margin-top: 16px" wrap>
          <NButton :loading="busy" @click="checkUpdate">检查更新</NButton>
          <NButton type="primary" :loading="busy" @click="install(!status.bin_exists)">{{ status.bin_exists ? '升级到最新版本' : '下载并安装' }}</NButton>
        </NSpace>
        <NAlert v-if="update.has_update" type="success" style="margin-top: 16px">
          可升级至 {{ update.latest_version }}，安装操作不会修改认证目录。
        </NAlert>
      </NCard>
    </NSpace>
  </NSpin>
</template>

<style scoped>
.status-row { display: flex; align-items: center; gap: 16px; color: var(--text-secondary); flex-wrap: wrap; }
.paths { display: grid; gap: 10px; }
.paths > div { display: grid; grid-template-columns: 90px minmax(0, 1fr); gap: 12px; align-items: baseline; }
.paths span { color: var(--text-secondary); }
.paths code { overflow-wrap: anywhere; color: #a5b4fc; }
@media (max-width: 767px) { .paths > div { grid-template-columns: 1fr; gap: 3px; } }
</style>