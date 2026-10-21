<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NForm, NFormItem, NGrid, NGi, NInput, NInputNumber,
  NSpin, NSpace, NSwitch, NTag, useMessage,
} from 'naive-ui'
import { cpaApi } from '@/api'
import { copyToClipboard } from '@/utils/clipboard'

const message = useMessage()
const loading = ref(true)
const busy = ref(false)
const config = ref<any>({
  enabled: true, auto_start: true, host: '127.0.0.1', port: 8317,
  api_keys: [], proxy_url: '', request_retry: 3, debug: false, allow_remote: false,
})
const status = ref<any>({})
const update = ref<any>({})
const quota = ref<any>(null)
const quotaBusy = ref(false)
const managementKey = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let quotaTimer: ReturnType<typeof setInterval> | undefined

async function load() {
  try {
    const [cfg, state, key] = await Promise.all([cpaApi.getConfig(), cpaApi.status(), cpaApi.managementKey()])
    config.value = { ...config.value, ...cfg.data }
    status.value = state.data
    managementKey.value = key.data.management_key || ''
  } catch (error: any) {
    message.error(error.response?.data?.error || '读取 CPA 配置失败')
  } finally {
    loading.value = false
  }
}

async function refreshStatus() {
  try { status.value = (await cpaApi.status()).data } catch { /* 页面保留上次状态 */ }
}

async function refreshQuota(force = false) {
  quotaBusy.value = true
  try {
    quota.value = (await cpaApi.quota(force)).data
  } catch (error: any) {
    quota.value = { provider: 'codex', accounts: [], error: error.response?.data?.error || error.message || '额度查询失败' }
  } finally { quotaBusy.value = false }
}

function quotaPercent(value: number | undefined | null): number {
  if (value == null || Number.isNaN(value)) return 0
  return Math.max(0, Math.min(100, value))
}

function quotaColor(value: number | undefined | null): string {
  const remaining = quotaPercent(value)
  if (remaining <= 20) return '#ef4444'
  if (remaining <= 60) return '#f59e0b'
  return '#22c55e'
}

function resetLabel(window: any): string {
  if (!window) return '未知'
  if (window.reset_at) return new Date(window.reset_at).toLocaleString()
  if (window.reset_after_seconds != null) {
    const seconds = Math.max(0, Number(window.reset_after_seconds))
    return `约 ${Math.ceil(seconds / 3600)} 小时后`
  }
  return '未知'
}

function quotaWindows(account: any): any[] {
  return [account.five_hour, account.weekly].filter(Boolean)
}

function cpaQuotaGroups(account: any): any[] {
  const groups = account.cpa_result?.groups
  return Array.isArray(groups) ? groups : []
}

function cpaQuotaSummary(account: any): any[] {
  const summary = account.cpa_result?.summary
  return Array.isArray(summary) ? summary : []
}

function cpaQuotaGroupLabel(group: any): string {
  return group?.displayName || group?.display_name || group?.name || '额度'
}

function cpaQuotaRemainingPercent(bucket: any): number | null {
  const raw = bucket?.remainingFraction ?? bucket?.remaining_fraction
  if (raw == null || raw === '') return null
  const value = Number(raw)
  if (!Number.isFinite(value)) return null
  return value >= 0 && value <= 1 ? value * 100 : value
}

function cpaQuotaWindowLabel(window: any): string {
  const value = String(window || '').trim()
  if (value === '5h') return '5 小时窗口'
  if (value === 'weekly' || value === '7d') return '周窗口'
  return value || '额度窗口'
}

function cpaQuotaResetLabel(bucket: any): string {
  const resetTime = bucket?.resetTime ?? bucket?.reset_time
  if (resetTime) {
    const date = new Date(resetTime)
    return Number.isNaN(date.getTime()) ? String(resetTime) : date.toLocaleString()
  }
  const seconds = bucket?.resetAfterSeconds ?? bucket?.reset_after_seconds
  return seconds == null ? '未知' : resetLabel({ reset_after_seconds: seconds })
}

function cpaQuotaMetricValue(metric: any): string {
  const value = Number(metric?.value)
  if (!Number.isFinite(value)) return String(metric?.value ?? '-')
  if (metric?.format === 'currency' && metric?.currency) {
    try { return new Intl.NumberFormat(undefined, { style: 'currency', currency: metric.currency }).format(value) } catch { /* 显示原始数值 */ }
  }
  const formatted = value.toLocaleString()
  return metric?.unit ? `${formatted} ${metric.unit}` : formatted
}

function cpaQuotaJSON(account: any): string {
  return JSON.stringify(account?.cpa_result ?? {}, null, 2)
}

function nativeManagementURL(): string {
  return `${window.location.origin}/cpa-native/management.html#/login`
}

function openNativeManagement() {
  if (!managementKey.value) {
    message.error('Management Key 尚未加载，请刷新页面后重试')
    return
  }

  // 复制必须直接发生在用户点击事件中；等待异步 API 后再复制会丢失浏览器授权。
  void copyToClipboard(managementKey.value).then((copied) => {
    if (copied) message.success('正确的 Management Key 已复制，请在原生面板中粘贴登录')
    else message.error('复制失败，请检查浏览器剪贴板权限')
  })

  const opened = window.open(nativeManagementURL(), '_blank')
  if (!opened) message.warning('浏览器阻止了新窗口，请允许弹出窗口后重试')
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
    message.success(`CLIProxyAPI ${result.data.version} 已安装${result.data.running ? '并已重启' : ''}`)
    await refreshStatus()
  } catch (error: any) {
    message.error(error.response?.data?.error || '安装失败')
  } finally { busy.value = false }
}

onMounted(() => {
  load()
  refreshQuota()
  timer = setInterval(refreshStatus, 10000)
  quotaTimer = setInterval(() => refreshQuota(), 5 * 60 * 1000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (quotaTimer) clearInterval(quotaTimer)
})
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
          <NButton secondary :disabled="!status.bin_exists" @click="openNativeManagement">打开原生管理面板</NButton>
        </NSpace>
        <div class="management-hint">
          原生面板由 CLIProxyAPI 提供，可管理认证文件、提供商 API Key、模型别名、日志和运行时配置。
        </div>
      </NCard>

      <NCard title="订阅额度" :segmented="{ content: true }">
        <template #header-extra>
          <NButton size="small" :loading="quotaBusy" @click="refreshQuota(true)">刷新额度</NButton>
        </template>
        <NAlert v-if="quota?.error" type="error" style="margin-bottom: 16px">{{ quota.error }}</NAlert>
        <NAlert v-else-if="!quota?.accounts?.length" type="info">
          尚未发现支持额度查询的订阅账号。完成 Codex 或 Antigravity OAuth 登录后，额度会显示在这里。
        </NAlert>
        <NSpace v-else vertical size="large">
          <div v-for="account in quota.accounts" :key="account.auth_index" class="quota-account">
            <div class="quota-account-header">
              <div>
                <strong>{{ account.email || account.account_id || account.credential_name || account.auth_index || '未知订阅' }}</strong>
                <NTag v-if="account.plan_type" size="small" type="info" style="margin-left: 8px">{{ account.plan_type }}</NTag>
              </div>
              <NTag :type="account.status === 'available' ? 'success' : 'error'" size="small">
                {{ account.status === 'available' ? '可用' : '查询失败' }}
              </NTag>
            </div>
            <NAlert v-if="account.error" type="error" style="margin: 12px 0">{{ account.error }}</NAlert>
            <NSpace v-else-if="account.cpa_result" vertical size="large">
              <div v-if="account.cpa_result.subscription?.plan || account.cpa_result.subscription?.tierName || account.cpa_result.subscription?.tier_name" class="quota-group-header">
                <strong>{{ account.cpa_result.subscription.plan || account.cpa_result.subscription.tierName || account.cpa_result.subscription.tier_name }}</strong>
                <span v-if="account.cpa_result.subscription.tierId || account.cpa_result.subscription.tier_id">{{ account.cpa_result.subscription.tierId || account.cpa_result.subscription.tier_id }}</span>
              </div>
              <div v-for="(group, groupIndex) in cpaQuotaGroups(account)" :key="group.id || group.displayName || groupIndex" class="quota-group">
                <div class="quota-group-header"><strong>{{ cpaQuotaGroupLabel(group) }}</strong></div>
                <NGrid :cols="2" :x-gap="24" responsive="screen" item-responsive>
                  <NGi v-for="(bucket, bucketIndex) in group.buckets || []" :key="bucket.id || (groupIndex + '-' + bucketIndex)" span="2 m:1">
                    <div class="quota-window">
                      <div class="quota-window-title">
                        <span>{{ bucket.description || cpaQuotaWindowLabel(bucket.window) }}</span>
                        <b v-if="cpaQuotaRemainingPercent(bucket) !== null">{{ cpaQuotaRemainingPercent(bucket)?.toFixed(1) }}% 剩余</b>
                      </div>
                      <div v-if="cpaQuotaRemainingPercent(bucket) !== null" class="quota-bar"><div class="quota-bar-fill" :style="{ width: `${quotaPercent(cpaQuotaRemainingPercent(bucket) || 0)}%`, background: quotaColor(cpaQuotaRemainingPercent(bucket)) }" /></div>
                      <div class="quota-window-meta">{{ cpaQuotaWindowLabel(bucket.window) }} · 刷新：{{ cpaQuotaResetLabel(bucket) }}</div>
                    </div>
                  </NGi>
                </NGrid>
              </div>
              <div v-if="cpaQuotaSummary(account).length" class="quota-group">
                <div class="quota-group-header"><strong>额度摘要</strong></div>
                <div v-for="metric in cpaQuotaSummary(account)" :key="metric.key || metric.label" class="quota-window-title" style="padding:8px 0">
                  <span>{{ metric.label || metric.key }}</span><b>{{ cpaQuotaMetricValue(metric) }}</b>
                </div>
              </div>
              <details>
                <summary style="cursor:pointer;color:var(--text-secondary)">CPA 原始额度结果</summary>
                <pre style="white-space:pre-wrap;overflow-wrap:anywhere;max-height:280px;overflow:auto;padding:12px;border-radius:6px;background:var(--bg-page);font-size:12px">{{ cpaQuotaJSON(account) }}</pre>
              </details>
            </NSpace>
            <NSpace v-else-if="account.provider === 'antigravity'" vertical size="large">
              <div v-for="group in account.antigravity_groups || []" :key="group.id" class="quota-group">
                <div class="quota-group-header">
                  <strong>{{ group.label }}</strong>
                  <span v-if="group.description">{{ group.description }}</span>
                </div>
                <NGrid :cols="2" :x-gap="24" responsive="screen" item-responsive>
                  <NGi v-for="bucket in group.buckets" :key="bucket.id" span="2 m:1">
                    <div class="quota-window">
                      <div class="quota-window-title"><span>{{ bucket.label }}</span><b>{{ bucket.remaining_percent?.toFixed(1) ?? '-' }}% 剩余</b></div>
                      <div class="quota-bar"><div class="quota-bar-fill" :style="{ width: `${quotaPercent(bucket.remaining_percent)}%`, background: quotaColor(bucket.remaining_percent) }" /></div>
                      <div class="quota-window-meta">{{ bucket.window === '5h' ? '5 小时窗口' : bucket.window === 'weekly' ? '周窗口' : bucket.window || '额度窗口' }} · 刷新：{{ resetLabel(bucket) }}</div>
                    </div>
                  </NGi>
                </NGrid>
              </div>
            </NSpace>
            <NGrid v-else :cols="2" :x-gap="24" responsive="screen" item-responsive>
              <NGi v-for="window in quotaWindows(account)" :key="window.id" span="2 m:1">
                <div class="quota-window">
                  <div class="quota-window-title"><span>{{ window.label === '5h' ? '5 小时额度' : window.label === '7d' ? '7 天额度' : window.label }}</span><b>{{ window.remaining_percent?.toFixed(1) ?? '-' }}% 剩余</b></div>
                  <div class="quota-bar"><div class="quota-bar-fill" :style="{ width: `${quotaPercent(window.remaining_percent)}%`, background: quotaColor(window.remaining_percent) }" /></div>
                  <div class="quota-window-meta">已用 {{ window.used_percent?.toFixed(1) ?? '-' }}% · 刷新：{{ resetLabel(window) }}</div>
                </div>
              </NGi>
            </NGrid>
            <div class="quota-account-footer">
              <span v-if="account.quota_source === 'cpa'">数据来源：CPA 原生 quota/fetch</span>
              <span v-else-if="account.quota_source === 'cpa-api-call'">数据来源：CPA 原生 Antigravity API-call</span>
              <span v-else-if="account.provider === 'codex'">主动重置次数：{{ account.reset_credits ?? 0 }}</span>
              <span v-else>数据来源：Antigravity retrieveUserQuotaSummary（兼容回退）</span>
              <span>查询时间：{{ account.queried_at ? new Date(account.queried_at).toLocaleString() : '-' }}</span>
            </div>
          </div>
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
              <NFormItem label="允许远程访问">
                <NSwitch v-model:value="config.allow_remote" />
              </NFormItem>
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
          <NButton type="primary" :loading="busy" @click="install(true)">{{ status.bin_exists ? '升级到最新版本' : '下载并安装' }}</NButton>
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
.management-hint { margin-top: 12px; color: var(--text-secondary); font-size: 12px; }
.paths { display: grid; gap: 10px; }
.paths > div { display: grid; grid-template-columns: 90px minmax(0, 1fr); gap: 12px; align-items: baseline; }
.paths span { color: var(--text-secondary); }
.paths code { overflow-wrap: anywhere; color: var(--accent-primary); }
.quota-account { padding: 16px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-card); }
.quota-account-header, .quota-window-title, .quota-account-footer { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.quota-account-footer { margin-top:14px; color:var(--text-secondary); font-size:12px; flex-wrap:wrap; }
.quota-group { padding-top:4px; }
.quota-group + .quota-group { padding-top:16px; border-top:1px solid var(--border-color); }
.quota-group-header { margin-bottom:12px; display:flex; flex-direction:column; gap:3px; }
.quota-group-header span { color:var(--text-secondary); font-size:12px; }
.quota-window-title { margin-bottom:8px; color:var(--text-secondary); }
.quota-window-title b { font-size:13px; }
.quota-bar { height:10px; overflow:hidden; border-radius:999px; background:var(--bg-muted); }
.quota-bar-fill { height:100%; border-radius:inherit; transition:width .3s ease; }
.quota-window-meta { margin-top:7px; color:var(--text-secondary); font-size:12px; }
@media (max-width: 767px) { .quota-account { padding:12px; } }
@media (max-width: 767px) { .paths > div { grid-template-columns: 1fr; gap: 3px; } }
</style>