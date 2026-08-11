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
const allModels = ref<string[]>([])
const showModal = ref(false)
const editing = ref<any>(null)
const form = ref({ name: '', display_name: '', main_model: '', vision_model: '', description: '', status: 'active' })

// 模型选项（含 supports_vision 标记）
const modelOptions = computed(() =>
  allModels.value.map((m: any) => ({ label: `${m.model_id}${m.supports_vision ? ' 📷' : ''}`, value: m.model_id }))
)

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
        <p class="page-subtitle">模型路由：下游请求虚拟模型名，按规则路由到实际模型。配置识图模型后，纯文本主模型自动获得识图能力</p>
      </div>

      <NCard size="small" title="使用说明">
        <div style="font-size:13px;color:#94a3b8;line-height:1.8">
          <p style="margin:0">📌 <b>无图请求</b>：直接路由到 <b>主模型</b>（零额外成本）</p>
          <p style="margin:0">📷 <b>有图请求</b>：先调用 <b>识图模型</b> 识别图片 → 图片替换为文字描述 → 交给主模型继续回答</p>
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
          <NFormItem label="主模型" label-description="无图请求路由的目标（纯文本模型）">
            <NSelect v-model:value="form.main_model" :options="modelOptions" filterable placeholder="选择主模型" />
          </NFormItem>
          <NFormItem label="识图模型" label-description="有图请求先调用此模型识图（需支持多模态）">
            <NSelect v-model:value="form.vision_model" :options="modelOptions" filterable clearable placeholder="选择识图模型（可留空=不启用识图扩展）" />
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
