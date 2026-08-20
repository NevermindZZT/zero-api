<script setup lang="ts">
import { h, onMounted, ref, computed } from 'vue'
import {
  NButton, NCard, NDataTable, NSpace, NTag, NModal, NForm, NFormItem,
  NInput, NSelect, NPopconfirm, useMessage, NSpin, NIcon,
} from 'naive-ui'
import { GitNetworkSharp } from '@vicons/ionicons5'
import api from '@/api'
import { formatDateTime } from '@/utils/format'

const message = useMessage()
const loading = ref(true)
const list = ref<any[]>([])
const allModels = ref<any[]>([])
const showModal = ref(false)
const editing = ref<any>(null)
const form = ref({ name: '', display_name: '', main_model: '', vision_model: '', description: '', status: 'active' })

// 虚拟模型按 model_id 路由，不需要选择具体渠道；合并同名模型避免重复显示。
const modelOptions = computed(() => {
  const grouped = new Map<string, { supportsVision: boolean; channels: number }>()
  for (const model of allModels.value) {
    if (!model.model_id) continue
    const existing = grouped.get(model.model_id)
    if (existing) {
      existing.supportsVision ||= Boolean(model.supports_vision)
      existing.channels += 1
      continue
    }
    grouped.set(model.model_id, {
      supportsVision: Boolean(model.supports_vision),
      channels: 1,
    })
  }
  return [...grouped.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([modelID, info]) => ({
      label: `${modelID}${info.supportsVision ? ' 📷' : ''}${info.channels > 1 ? ` (${info.channels} 个渠道)` : ''}`,
      value: modelID,
    }))
})

onMounted(async () => {
  await loadList()
  loadModels()
})

async function loadList() {
  loading.value = true
  try {
    const res = await api.get('/virtual-models')
    list.value = res.data || []
  } finally {
    loading.value = false
  }
}

async function loadModels() {
  try {
    const res = await api.get('/models')
    allModels.value = res.data || []
  } catch { /* ignore */ }
}

const columns = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '虚拟模型名', key: 'name' },
  { title: '展示名', key: 'display_name', render: (r: any) => r.display_name || '-' },
  { title: '主模型', key: 'main_model' },
  {
    title: '识图模型',
    key: 'vision_model',
    render: (r: any) =>
      r.vision_model
        ? h(NTag, { size: 'small', type: 'success' }, () => r.vision_model)
        : h(NTag, { size: 'small', type: 'default' }, '未配置'),
  },
  { title: '描述', key: 'description', ellipsis: true, render: (r: any) => r.description || '-' },
  {
    title: '状态',
    key: 'status',
    render: (r: any) =>
      h(NTag, { type: r.status === 'active' ? 'success' : 'default', size: 'small' }, () =>
        r.status === 'active' ? '启用' : '禁用'
      ),
  },
  { title: '创建时间', key: 'created_at', render: (r: any) => formatDateTime(r.created_at) },
  {
    title: '操作',
    key: 'actions',
    render: (r: any) =>
      h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEdit(r) }, '编辑'),
          h(NButton, { size: 'small', onClick: () => toggle(r) }, () =>
            r.status === 'active' ? '禁用' : '启用'
          ),
          h(NPopconfirm, {
            onPositiveClick: () => del(r.id),
          }, {
            default: () => '确认删除此虚拟模型？',
            trigger: () => h(NButton, { size: 'small', type: 'error' }, '删除'),
          }),
        ],
      }),
  },
]

function openCreate() {
  editing.value = null
  form.value = { name: '', display_name: '', main_model: '', vision_model: '', description: '', status: 'active' }
  showModal.value = true
}

function openEdit(r: any) {
  editing.value = r
  form.value = { ...r }
  showModal.value = true
}

async function save() {
  if (!form.value.name || !form.value.main_model) {
    message.warning('虚拟模型名和主模型必填')
    return
  }
  try {
    if (editing.value) {
      await api.put(`/virtual-models/${editing.value.id}`, form.value)
      message.success('已更新')
    } else {
      await api.post('/virtual-models', form.value)
      message.success('已创建')
    }
    showModal.value = false
    loadList()
  } catch (e: any) {
    message.error(e.response?.data?.error || '操作失败')
  }
}

async function toggle(r: any) {
  await api.post(`/virtual-models/${r.id}/toggle`)
  loadList()
}

async function del(id: number) {
  await api.delete(`/virtual-models/${id}`)
  message.success('已删除')
  loadList()
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><GitNetworkSharp /></NIcon>
          虚拟模型
        </h2>
        <p class="page-subtitle">稳定模型入口：下游固定使用虚拟模型名，可随时切换主模型；识图扩展为可选能力</p>
      </div>

      <NCard size="small" title="使用说明">
        <div style="font-size:13px;color:#94a3b8;line-height:1.8">
          <p style="margin:0">📌 <b>模型路由</b>：所有请求默认路由到 <b>主模型</b>。更新主模型后，Agent 无需切换模型名。</p>
          <p style="margin:0">📷 <b>主模型原生识图</b>：主模型支持视觉时，识图模型可留空，图片会原样转发给主模型。</p>
          <p style="margin:0">🧩 <b>识图扩展</b>：主模型不支持视觉时，可配置识图模型，系统会先生成图片描述再交给主模型。</p>
        </div>
      </NCard>

      <NCard style="width:100%">
        <template #header>
          <div style="display:flex;justify-content:space-between;align-items:center">
            <span>虚拟模型列表</span>
            <NButton type="primary" size="small" @click="openCreate">新建虚拟模型</NButton>
          </div>
        </template>
        <NDataTable :columns="columns" :data="list" :bordered="false" :scroll-x="900" />
      </NCard>

      <NModal v-model:show="showModal" title="虚拟模型" preset="card" style="width:520px;max-width:calc(100vw - 32px)">
        <NForm :model="form" label-placement="left" label-width="90">
          <NFormItem label="虚拟模型名" label-description="下游请求使用的模型名">
            <NInput v-model:value="form.name" placeholder="如: deepseek-text-vision" :disabled="!!editing" />
          </NFormItem>
          <NFormItem label="展示名">
            <NInput v-model:value="form.display_name" placeholder="如: DeepSeek 文本+识图" />
          </NFormItem>
          <NFormItem label="主模型" label-description="所有请求默认路由的目标；可随时切换">
            <NSelect v-model:value="form.main_model" :options="modelOptions" filterable placeholder="选择主模型" />
          </NFormItem>
          <NFormItem label="识图模型" label-description="可选：仅主模型不支持视觉时配置，用于图片转文字描述">
            <NSelect v-model:value="form.vision_model" :options="modelOptions" filterable clearable placeholder="主模型支持视觉时可留空" />
          </NFormItem>
          <NFormItem label="描述">
            <NInput v-model:value="form.description" type="textarea" placeholder="用途说明" />
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
