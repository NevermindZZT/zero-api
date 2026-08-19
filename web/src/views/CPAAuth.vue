<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NSelect, NSpace, NSwitch, NTag, useMessage } from 'naive-ui'
import { cpaApi } from '@/api'

const message = useMessage()
const provider = ref('codex')
const device = ref(false)
const noBrowser = ref(false)
const busy = ref(false)
const auth = ref<any>({ auth_files: [] })
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
    await cpaApi.startAuth(provider.value, device.value, noBrowser.value)
    message.success('登录流程已启动，请按输出提示完成授权')
    await refresh()
  } catch (error: any) {
    message.error(error.response?.data?.error || '启动登录失败')
  } finally { busy.value = false }
}

async function stop() {
  busy.value = true
  try {
    await cpaApi.stopAuth()
    message.info('登录流程已取消')
    await refresh()
  } catch (error: any) {
    message.error(error.response?.data?.error || '取消登录失败')
  } finally { busy.value = false }
}

onMounted(() => { refresh(); timer = setInterval(refresh, 2000) })
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

    <NCard title="添加订阅账号">
      <NSpace vertical size="large">
        <NSelect v-model:value="provider" :options="providers" :disabled="auth.running" />
        <NSpace align="center" wrap>
          <NSwitch v-model:value="device" :disabled="provider !== 'codex' || auth.running" />
          <span>Codex 使用设备码登录</span>
        </NSpace>
        <NSpace align="center" wrap>
          <NSwitch v-model:value="noBrowser" :disabled="auth.running" />
          <span>不自动打开浏览器</span>
        </NSpace>
        <NSpace>
          <NButton type="primary" :loading="busy" :disabled="auth.running" @click="start">开始登录</NButton>
          <NButton type="warning" :loading="busy" :disabled="!auth.running" @click="stop">取消登录</NButton>
          <NButton :loading="busy" @click="refresh">刷新状态</NButton>
        </NSpace>
      </NSpace>
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