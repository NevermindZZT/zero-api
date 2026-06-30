<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NButton, NCard, NDataTable, NModal, NForm, NFormItem,
  NInput, NInputNumber, NSelect, NSpace, NTag, NPopconfirm, NMessageProvider,
  useMessage, NSpin, NIcon, NSwitch,
} from 'naive-ui'
import { GlobeSharp } from '@vicons/ionicons5'
import { channelApi } from '@/api'

const message = useMessage()
const loading = ref(true)
const channels = ref<any[]>([])
const showModal = ref(false)
const editing = ref<any>(null)
const syncingId = ref<number | null>(null)
const form = ref({ name: '', type: 'openai', base_url: '', api_key: '', status: 'active', priority: 99, use_proxy: false })

const channelTypes = [
  { label: 'OpenAI 兼容', value: 'openai' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Google Gemini', value: 'gemini' },
  { label: 'OpenRouter', value: 'openrouter' },
]

const columns = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '名称', key: 'name' },
  {
    title: '类型',
    key: 'type',
    render: (r: any) => {
      const t = channelTypes.find((t) => t.value === r.type)
      return t?.label || r.type
    },
  },
  { title: 'Base URL', key: 'base_url', ellipsis: true },
  { title: '优先级', key: 'priority', width: 90, sorter: 'default' as const, defaultSortOrder: 'ascend' as const },
  {
    title: '状态',
    key: 'status',
    render: (r: any) => (
      r.status === 'active'
        ? h(NTag, { type: 'success', size: 'small' }, () => '启用')
        : h(NTag, { type: 'default', size: 'small' }, () => '禁用')
    ),
  },
  {
    title: '代理',
    key: 'use_proxy',
    width: 80,
    render: (r: any) => (
      r.use_proxy
        ? h(NTag, { type: 'warning', size: 'small' }, () => '代理')
        : h(NTag, { type: 'default', size: 'small' }, () => '直连')
    ),
  },
  {
    title: '操作',
    key: 'actions',
    render: (r: any) =>
      h(NSpace, null, {
        default: () => [
          h(NButton, {
            size: 'small', onClick: () => editChannel(r),
          }, () => '编辑'),
          h(NButton, {
            size: 'small',
            loading: syncingId.value === r.id,
            disabled: syncingId.value !== null,
            onClick: () => syncModels(r.id),
          }, () => '同步'),
          h(NPopconfirm, {
            onPositiveClick: () => deleteChannel(r.id),
          }, {
            default: () => '确认删除？',
            trigger: () => h(NButton, { size: 'small', type: 'error' }, () => '删除'),
          }),
        ],
      }),
  },
]

onMounted(loadChannels)

async function loadChannels() {
  loading.value = true
  try {
    const res = await channelApi.list()
    channels.value = res.data
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.value = { name: '', type: 'openai', base_url: '', api_key: '', status: 'active', priority: 99, use_proxy: false }
  showModal.value = true
}

function editChannel(ch: any) {
  editing.value = ch
  form.value = { ...ch }
  showModal.value = true
}

async function save() {
  try {
    if (editing.value) {
      await channelApi.update(editing.value.id, form.value)
      message.success('渠道已更新')
    } else {
      await channelApi.create(form.value)
      message.success('渠道已创建')
    }
    showModal.value = false
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '操作失败')
  }
}

async function deleteChannel(id: number) {
  try {
    await channelApi.delete(id)
    message.success('渠道已删除')
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '删除失败')
  }
}

async function syncModels(id: number) {
  try {
    syncingId.value = id
    message.info('正在同步模型列表...')
    const res = await channelApi.syncModels(id)
    message.success(`同步完成，共 ${res.data.count} 个模型`)
    loadChannels()
  } catch (e: any) {
    message.error(e.response?.data?.error || '同步失败')
  } finally {
    syncingId.value = null
  }
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header" style="display:flex;justify-content:space-between;align-items:center">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><GlobeSharp /></NIcon>
          渠道管理
        </h2>
        <NButton type="primary" @click="openCreate">添加渠道</NButton>
      </div>
      <NCard style="width:100%">
        <NDataTable :columns="columns" :data="channels" :bordered="false" :scroll-x="800" />
      </NCard>

      <NModal v-model:show="showModal" title="渠道" preset="card" style="width:520px">
        <NForm :model="form" label-placement="left" label-width="80">
          <NFormItem label="名称">
            <NInput v-model:value="form.name" placeholder="例如: DeepSeek" />
          </NFormItem>
          <NFormItem label="类型">
            <NSelect v-model:value="form.type" :options="channelTypes" />
          </NFormItem>
          <NFormItem label="Base URL">
            <NInput v-model:value="form.base_url" placeholder="https://api.deepseek.com" />
          </NFormItem>
          <NFormItem label="API Key">
            <NInput
              v-model:value="form.api_key"
              type="password"
              show-password-on="click"
              placeholder="可选，留空则使用请求中的 Authorization"
            />
          </NFormItem>
          <NFormItem label="优先级" label-description="数值越小优先级越高，0 最高">
            <NInputNumber v-model:value="form.priority" :min="0" :max="999" style="width:100%" />
          </NFormItem>
          <NFormItem label="出站代理">
            <NSwitch v-model:value="form.use_proxy" />
            <span style="color:#94a3b8;font-size:13px;margin-left:8px">
              启用后此渠道的请求通过全局代理转发
            </span>
          </NFormItem>
        </NForm>
        <template #footer>
          <NSpace justify="end">
            <NButton @click="showModal = false">取消</NButton>
            <NButton type="primary" @click="save">保存</NButton>
          </NSpace>
        </template>
      </NModal>
    </NSpace>
  </NSpin>
</template>
