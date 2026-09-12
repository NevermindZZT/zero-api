<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NSelect, NSpace, NSwitch, NTag, useMessage } from 'naive-ui'
import { cpaApi } from '@/api'

const message = useMessage()
const provider = ref('codex')
const device = ref(false)
const noBrowser = ref(false)
const managementMode = ref(true)
const busy = ref(false)
const auth = ref<any>({ auth_files: [] })
const managementAuth = ref<any>(null)
let timer: ReturnType<typeof setInterval> | undefined

const providers = [
  { label: 'OpenAI Codex', value: 'codex' },
  { label: 'Anthropic Claude', value: 'claude' },
  { label: 'xAI Grok', value: 'grok' },
  { label: 'Kimi', value: 'kimi' },
  { label: 'Antigravity', value: 'antigravity' },
]

async function refresh() {
  try { auth.value = (await cpaApi.authStatus()).data } catch { /* preserve last state */ }
}

async function start() {
  busy.value = true
  try {
    if (managementMode.value) {
      if (device.value) {
        message.warning('Management API 不支持设备码，将切换为普通 OAuth 登录')
        device.value = false
      }
      const result = (await cpaApi.startManagementAuth(provider.value)).data
      managementAuth.value = result
      window.open(result.url, '_blank', 'noopener,noreferrer')
      message.success('授权页面已打开，完成授权后请等待状态更新')
    } else {
      await cpaApi.startAuth(provider.value, device.value, noBrowser.value)
      message.success('登录流程已启动，请按输出提示完成授权')
      await refresh()
    }
  } catch (error: any) {
    message.error(error.response?.data?.error || '启动登录失败')
  } finally { busy.value = false }
}

async function stop() {
  busy.value = true
  try {
    if (managementAuth.value?.state) {
      managementAuth.value = null
      message.info('已停止等待当前 Management API 登录状态')
    } else {
      await cpaApi.stopAuth()
      message.info('登录流程已取消')
      await refresh()
    }
  } catch (error: any) {
    message.error(error.response?.data?.error || '取消登录失败')
  } finally { busy.value = false }
}

async function refreshManagementAuth() {
  if (!managementAuth.value?.state) return
  try {
    const result = (await cpaApi.managementAuthStatus(managementAuth.value.state)).data
    managementAuth.value = { ...managementAuth.value, ...result }
    if (result.status === 'ok') message.success('OAuth 登录成功，认证文件已保存')
    if (result.status === 'error') message.error(result.error || 'OAuth 登录失败')
    if (result.status === 'ok' || result.status === 'error') managementAuth.value = null
  } catch { /* preserve current OAuth state */ }
}

function openManagementAuth() {
  const url = managementAuth.value?.url
  if (url) window.open(url, '_blank', 'noopener,noreferrer')
}

onMounted(() => {
  refresh()
  timer = setInterval(() => { refresh(); refreshManagementAuth() }, 2000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <div class="cpa-auth-page">
    <NSpace vertical size="large">
    <div class="page-header">
      <h2>CLIProxyAPI 订阅登录</h2>
      <div class="page-subtitle">认证由 CLIProxyAPI 官方 OAuth 流程完成，zero-api 只管理登录进程和状态。</div>
    </div>

    <NAlert type="info">
      每次只运行一个登录流程。授权完成后，CLIProxyAPI 会把凭据保存在自己的 auth 目录，zero-api 不读取或保存 token 内容。
    </NAlert>

    <NAlert v-if="provider === 'codex' || provider === 'claude' || provider === 'antigravity'" type="warning">
      Docker 或无桌面的远程服务器登录：先在本地电脑建立 SSH 端口转发，再点击“开始登录”。
      {{ provider === 'codex' ? 'Codex 回调端口为 1455。' : provider === 'claude' ? 'Claude 回调端口为 54545。' : 'Antigravity 回调端口为 51121。' }}
      示例：<code>ssh -L {{ provider === 'codex' ? 1455 : provider === 'claude' ? 54545 : 51121 }}:127.0.0.1:{{ provider === 'codex' ? 1455 : provider === 'claude' ? 54545 : 51121 }} root@服务器地址 -p SSH端口</code>
      建立隧道后，在本地浏览器打开输出中的授权链接；不要把 OAuth 回调端口暴露到公网。
    </NAlert>

    <NCard title="添加订阅账号">
      <NSpace vertical size="large">
        <NSelect v-model:value="provider" :options="providers" :disabled="auth.running || !!managementAuth" />
        <NSpace align="center" wrap>
          <NSwitch v-model:value="managementMode" :disabled="auth.running || !!managementAuth" />
          <span>使用 CPA Management API 登录（不需要 OAuth 回调端口）</span>
        </NSpace>
        <NSpace align="center" wrap>
          <NSwitch v-model:value="device" :disabled="provider !== 'codex' || auth.running || !!managementAuth || managementMode" />
          <span>Codex 使用设备码登录</span>
        </NSpace>
        <NSpace align="center" wrap>
          <NSwitch v-model:value="noBrowser" :disabled="auth.running" />
          <span>不自动打开浏览器</span>
        </NSpace>
        <NSpace>
          <NButton type="primary" :loading="busy" :disabled="auth.running || !!managementAuth" @click="start">开始登录</NButton>
          <NButton type="warning" :loading="busy" :disabled="!auth.running && !managementAuth" @click="stop">取消登录</NButton>
          <NButton :loading="busy" @click="refresh">刷新状态</NButton>
        </NSpace>
      </NSpace>
    </NCard>

    <NCard v-if="managementAuth" title="Management API 登录">
      <NSpace align="center" wrap>
        <NTag type="warning">等待 {{ managementAuth.provider }} OAuth 回调</NTag>
        <span v-if="managementAuth.state">state: {{ managementAuth.state }}</span>
      </NSpace>
      <NAlert type="info" style="margin-top:16px">
        授权页面的回调会先进入 zero-api 的 8080 端口，再由 CPA Management API 处理；无需暴露 1455、54545 或 51121，也无需 SSH 转发。
      </NAlert>
      <NButton v-if="managementAuth.url" style="margin-top:16px" @click="openManagementAuth">重新打开授权页面</NButton>
    </NCard>

    <NCard title="当前登录流程">
      <NSpace align="center" wrap>
        <NTag :type="auth.running ? 'warning' : 'default'">{{ auth.running ? `正在登录 ${auth.provider}` : '没有进行中的登录' }}</NTag>
        <span v-if="auth.started_at">开始于 {{ new Date(auth.started_at).toLocaleString() }}</span>
      </NSpace>
      <div v-if="auth.output" class="auth-output"><pre>{{ auth.output }}</pre></div>
      <NAlert v-else type="default" style="margin-top:16px">登录输出会显示在这里。</NAlert>
    </NCard>

    <NCard title="已发现的认证文件">
      <NSpace v-if="auth.auth_files?.length" wrap>
        <NTag v-for="file in auth.auth_files" :key="file" type="success">{{ file }}</NTag>
      </NSpace>
      <NAlert v-else type="default">认证目录中尚未发现账号文件。</NAlert>
      <div class="auth-dir">目录：{{ auth.auth_dir || '-' }}</div>
    </NCard>
    </NSpace>
  </div>
</template>

<style scoped>
.cpa-auth-page { width: 100%; min-height: max-content; padding-bottom: 24px; }
.auth-output {
  max-height: 360px;
  margin-top: 16px;
  overflow: auto;
  overscroll-behavior: contain;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.2);
  scrollbar-width: thin;
  scrollbar-color: rgba(148, 163, 184, 0.35) transparent;
}
.auth-output pre {
  min-width: max-content;
  margin: 0;
  padding: 12px;
  color: #cbd5e1;
  font: 12px/1.6 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.auth-output::-webkit-scrollbar { width: 6px; height: 6px; }
.auth-output::-webkit-scrollbar-thumb { background: rgba(148, 163, 184, 0.35); border-radius: 3px; }
.auth-dir { margin-top: 16px; color: var(--text-secondary); font-size: 13px; overflow-wrap: anywhere; }
</style>