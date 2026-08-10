<script setup lang="ts">
import { h, onMounted, onUnmounted, ref, computed } from 'vue'
import {
  NButton, NCard, NDataTable, NSpace, NTag, NModal,
  NForm, NFormItem, NInput, NInputGroup, NInputGroupLabel, NAlert,
  useMessage, NSpin, NPopconfirm, NIcon, NSelect, NInputNumber, NSwitch,
} from 'naive-ui'
import { KeySharp } from '@vicons/ionicons5'
import api from '@/api'
import { copyToClipboard } from '@/utils/clipboard'
import { formatDateTime } from '@/utils/format'

const message = useMessage()
const loading = ref(true)
const keys = ref<any[]>([])
const showCreate = ref(false)
const newName = ref('')
const createdKey = ref<string | null>(null)
const apiBase = ref('')
const lastUpdated = ref('')
let refreshTimer: ReturnType<typeof setInterval> | null = null

// 编辑配置状态
const showConfigModal = ref(false)
const editingKey = ref<any>(null)
const configForm = ref({ quota_enabled: false, quota_balance: 0, allowed_models: [] as string[] })
const savingConfig = ref(false)
const allModels = ref<string[]>([])

// 解析模型列表
function parseModels(s: string | undefined): string[] {
  if (!s || s === '[]' || s === 'null') return []
  try { return JSON.parse(s) } catch { return [] }
}

// 模型选择选项
const modelOptions = computed(() => allModels.value.map((m) => ({ label: m, value: m })))

onMounted(async () => {
  apiBase.value = window.location.origin + '/v1'
  await loadKeys()
  loadModels()
  // 15s 智能轮询：余额/用量变化自动刷新
  refreshTimer = setInterval(smartPoll, 15000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})

// 智能轮询：对比 API 数量或余额变化，有变化才刷新
async function smartPoll() {
  try {
    const res = await api.get('/api-keys')
    const fresh = res.data
    if (!fresh || fresh.length !== keys.value.length) {
      await loadKeys()
      return
    }
    // 对比额度/状态是否有变化
    const changed = fresh.some((nk: any) => {
      const ok = keys.value.find((k) => k.id === nk.id)
      return !ok || ok.quota_balance !== nk.quota_balance || ok.quota_used !== nk.quota_used || ok.enabled !== nk.enabled
    })
    if (changed) {
      keys.value = fresh
      lastUpdated.value = now()
    }
  } catch { /* 网络错误忽略 */ }
}

function now() {
  const d = new Date()
  return `${d.getHours().toString().padStart(2,'0')}:${d.getMinutes().toString().padStart(2,'0')}:${d.getSeconds().toString().padStart(2,'0')}`
}

// 加载全部模型供选择
async function loadModels() {
  try {
    const res = await api.get('/models')
    const ids = (res.data || []).map((m: any) => m.model_id)
    allModels.value = ids
  } catch { /* ignore */ }
}

// 打开编辑配置
function openConfig(key: any) {
  editingKey.value = key
  configForm.value = {
    quota_enabled: !!key.quota_enabled,
    quota_balance: key.quota_balance || 0,
    allowed_models: parseModels(key.allowed_models),
  }
  showConfigModal.value = true
}

// 保存配置
async function saveConfig() {
  savingConfig.value = true
  try {
    const res = await api.put(`/api-keys/${editingKey.value.id}`, configForm.value)
    message.success('配置已更新')
    showConfigModal.value = false
    // 更新列表
    const idx = keys.value.findIndex((k) => k.id === editingKey.value.id)
    if (idx >= 0) keys.value[idx] = res.data
  } catch (e: any) {
    message.error(e.response?.data?.error || '保存失败')
  } finally {
    savingConfig.value = false
  }
}

const columns = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '名称', key: 'name' },
  {
    title: '密钥', key: 'key', ellipsis: true,
    render: (r: any) => r.key?.substring(0, 16) + '...',
  },
  {
    title: '剩余余额',
    key: 'quota',
    render: (r: any) => {
      if (!r.quota_enabled) return h(NTag, { size: 'small', type: 'default' }, '不限')
      const balance = r.quota_balance || 0
      const color = balance <= 0 ? 'error' : balance < 1 ? 'warning' : 'success'
      return h('div', { style: 'display:flex;flex-direction:column;align-items:flex-start;gap:2px' }, [
        h(NTag, { size: 'small', type: color }, () => `$${balance.toFixed(4)}`),
      ])
    },
  },
  {
    title: '累计使用',
    key: 'quota_used',
    render: (r: any) => {
      if (!r.quota_enabled) return h('span', { style: 'color:#64748b;font-size:12px' }, '-')
      return h('span', { style: 'font-size:12px;color:#94a3b8' }, `$${(r.quota_used || 0).toFixed(4)}`)
    },
  },
  {
    title: '模型限制',
    key: 'allowed_models',
    render: (r: any) => {
      const models = parseModels(r.allowed_models)
      if (models.length === 0) return h(NTag, { size: 'small', type: 'default' }, '全部')
      return h('div', { style: 'display:flex;flex-wrap:wrap;gap:2px;max-width:220px' }, [
        ...models.slice(0, 3).map((m: string) => h(NTag, { size: 'small' }, () => m)),
        ...(models.length > 3 ? [h(NTag, { size: 'small', type: 'info' }, () => `+${models.length - 3}`)] : []),
      ])
    },
  },
  {
    title: '状态',
    key: 'enabled',
    render: (r: any) =>
      h(NTag, { type: r.enabled ? 'success' : 'default', size: 'small' }, () =>
        r.enabled ? '启用' : '禁用'
      ),
  },
  { title: '创建时间', key: 'created_at', render: (r: any) => formatDateTime(r.created_at) },
  {
    title: '操作',
    key: 'actions',
    render: (r: any) =>
      h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => copyKey(r.key) }, '复制'),
          h(NButton, { size: 'small', onClick: () => openConfig(r) }, '配置'),
          h(NButton, { size: 'small', onClick: () => toggle(r.id) }, () =>
            r.enabled ? '禁用' : '启用'
          ),
          h(NPopconfirm, {
            onPositiveClick: () => deleteKey(r.id),
          }, {
            default: () => '确认删除此密钥？相关使用记录将被保留。',
            trigger: () => h(NButton, { size: 'small', type: 'error' }, '删除'),
          }),
        ],
      }),
  },
]

onMounted(loadKeys)

async function loadKeys() {
  loading.value = true
  try {
    const res = await api.get('/api-keys')
    keys.value = res.data
  } finally {
    loading.value = false
  }
}

function openCreate() {
  newName.value = ''
  createdKey.value = null
  showCreate.value = true
}

async function doCreate() {
  if (!newName.value.trim()) {
    message.warning('请输入密钥名称')
    return
  }
  try {
    const res = await api.post('/api-keys', { name: newName.value })
    createdKey.value = res.data.key
    message.success('密钥已创建，请立即复制！')
    loadKeys()
  } catch (e: any) {
    message.error(e.response?.data?.error || '创建失败')
  }
}

async function toggle(id: number) {
  try {
    await api.post(`/api-keys/${id}/toggle`)
    loadKeys()
  } catch {
    message.error('操作失败')
  }
}

async function deleteKey(id: number) {
  try {
    await api.delete(`/api-keys/${id}`)
    message.success('已删除')
    loadKeys()
  } catch {
    message.error('删除失败')
  }
}

function copyKey(key: string) {
  copyToClipboard(key).then((ok) => {
    if (ok) {
      message.success('已复制到剪贴板')
    } else {
      message.error('复制失败，请手动复制')
    }
  })
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><KeySharp /></NIcon>
          API 密钥管理
        </h2>
        <p class="page-subtitle">用于客户端调用中转 API 的认证凭据</p>
      </div>

      <NCard title="使用说明" size="small">
        <div style="display:flex;flex-direction:column;gap:12px">
          <div style="display:flex;align-items:center;gap:12px">
            <span style="color:#94a3b8;font-size:13px;white-space:nowrap">Base URL：</span>
            <NInputGroup>
              <NInput :value="apiBase" readonly :style="{ fontFamily: 'monospace', fontSize: '13px' }" />
              <NButton type="primary" @click="copyKey(apiBase)">复制</NButton>
            </NInputGroup>
          </div>
          <p style="color:#94a3b8;font-size:13px;margin:0">
            客户端调用时，在请求头中携带 API Key 即可完成认证：
          </p>
          <pre style="background:rgba(0,0,0,0.2);padding:12px;border-radius:8px;font-size:12px;margin:0"><span style="color:#94a3b8">Authorization: Bearer </span><span style="color:#22c55e">sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx</span></pre>
        </div>
      </NCard>

      <NCard style="width:100%">
        <template #header>
          <div style="display:flex;justify-content:space-between;align-items:center">
            <span>密钥列表</span>
            <div style="display:flex;align-items:center;gap:12px">
              <span v-if="lastUpdated" style="font-size:12px;color:#64748b">
                更新: {{ lastUpdated }} <span style="color:#22c55e;margin-left:4px">● 15s 自动刷新</span>
              </span>
              <NButton type="primary" size="small" @click="openCreate">新建密钥</NButton>
            </div>
          </div>
        </template>
        <NDataTable :columns="columns" :data="keys" :bordered="false" :scroll-x="700" />
      </NCard>

      <!-- Create Modal -->
      <NModal v-model:show="showCreate" title="新建密钥" preset="card" style="width:520px">
        <template v-if="!createdKey">
          <NForm>
            <NFormItem label="名称">
              <NInput v-model:value="newName" placeholder="例如：My Agent" @keyup.enter="doCreate" />
            </NFormItem>
          </NForm>
          <div style="display:flex;justify-content:flex-end;gap:8px;margin-top:16px">
            <NButton @click="showCreate = false">取消</NButton>
            <NButton type="primary" @click="doCreate">生成</NButton>
          </div>
        </template>
        <div v-else>
          <div style="background:rgba(34,197,94,0.1);color:#22c55e;padding:12px;border-radius:8px;margin-bottom:16px;font-size:13px">
            ⚠️ 密钥只显示一次，请立即复制保存！
          </div>
          <div style="display:flex;gap:0">
            <div style="background:rgba(255,255,255,0.05);padding:8px 12px;border-radius:8px 0 0 8px;font-size:12px;color:#94a3b8;display:flex;align-items:center;border:1px solid rgba(255,255,255,0.1);border-right:none">密钥</div>
            <div style="flex:1;padding:8px 12px;font-family:monospace;font-size:12px;border:1px solid rgba(255,255,255,0.1);border-radius:0;background:rgba(0,0,0,0.2)">{{ createdKey }}</div>
            <NButton type="primary" @click="copyKey(createdKey!)" style="border-radius:0 8px 8px 0">复制</NButton>
          </div>
          <div style="margin-top:16px">
            <NButton @click="showCreate = false" style="width:100%">关闭</NButton>
          </div>
        </div>
      </NModal>

      <!-- 配置弹窗：额度 + 模型限制 -->
      <NModal v-model:show="showConfigModal" title="密钥配置" preset="card" style="width:520px;max-width:calc(100vw - 32px)">
        <template v-if="editingKey">
          <div style="margin-bottom:12px;font-size:13px;color:#94a3b8">
            密钥：<code>{{ editingKey.key?.substring(0, 20) }}...</code>
          </div>
          <NForm label-placement="left" label-width="100">
            <NFormItem label="额度限制">
              <NSwitch v-model:value="configForm.quota_enabled" />
              <span style="color:#94a3b8;font-size:12px;margin-left:8px">开启后按用量扣减，余额为 0 时拒绝请求</span>
            </NFormItem>
            <NFormItem v-if="configForm.quota_enabled" label="剩余余额 ($)" label-description="输入新值即覆盖，可随时充值/重置">
              <NInputNumber v-model:value="configForm.quota_balance" :min="0" :step="0.01" style="width:100%" placeholder="剩余额度" />
            </NFormItem>
            <NFormItem v-if="configForm.quota_enabled" label="累计已用 ($)">
              <span style="color:#94a3b8">$ {{ (editingKey.quota_used || 0).toFixed(4) }}</span>
            </NFormItem>
            <NFormItem label="可用模型" label-description="留空=全部模型可用">
              <NSelect
                v-model:value="configForm.allowed_models"
                :options="modelOptions"
                multiple
                filterable
                :max-tag-count="5"
                placeholder="选择允许使用的模型（留空=全部）"
              />
            </NFormItem>
          </NForm>
          <div style="display:flex;justify-content:flex-end;gap:8px;margin-top:16px">
            <NButton @click="showConfigModal = false">取消</NButton>
            <NButton type="primary" :loading="savingConfig" @click="saveConfig">保存</NButton>
          </div>
        </template>
      </NModal>
    </NSpace>
  </NSpin>
</template>

<style scoped>
/* page-header styles are now global in App.vue */
code {
  background: rgba(102,126,234,0.2);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
}
</style>
