<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NButton, NCard, NDataTable, NSpace, NTag, NModal,
  NForm, NFormItem, NInput, NInputGroup, NInputGroupLabel, NAlert,
  useMessage, NSpin, NPopconfirm, NIcon,
} from 'naive-ui'
import { KeySharp } from '@vicons/ionicons5'
import api from '@/api'

const message = useMessage()
const loading = ref(true)
const keys = ref<any[]>([])
const showCreate = ref(false)
const newName = ref('')
const createdKey = ref<string | null>(null)
const apiBase = ref('')

onMounted(async () => {
  apiBase.value = window.location.origin + '/v1'
  await loadKeys()
})

const columns = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '名称', key: 'name' },
  {
    title: '密钥', key: 'key', ellipsis: true,
    render: (r: any) => r.key?.substring(0, 16) + '...',
  },
  {
    title: '状态',
    key: 'enabled',
    render: (r: any) =>
      h(NTag, { type: r.enabled ? 'success' : 'default', size: 'small' }, () =>
        r.enabled ? '启用' : '禁用'
      ),
  },
  { title: '创建时间', key: 'created_at' },
  {
    title: '操作',
    key: 'actions',
    render: (r: any) =>
      h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => copyKey(r.key) }, '复制'),
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
  navigator.clipboard.writeText(key).then(() => {
    message.success('已复制到剪贴板')
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

      <NCard>
        <template #header>
          <div style="display:flex;justify-content:space-between;align-items:center">
            <span>密钥列表</span>
            <NButton type="primary" size="small" @click="openCreate">新建密钥</NButton>
          </div>
        </template>
        <NDataTable :columns="columns" :data="keys" :bordered="false" />
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
