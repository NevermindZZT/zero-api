<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, h } from 'vue'
import {
  NCard, NGrid, NGi, NButton, NModal, NForm, NFormItem, NInput,
  NSelect, NSpace, NTag, NIcon, NDrawer, NDrawerContent, NDataTable, NAlert,
  useMessage, useDialog,
} from 'naive-ui'
import { AddSharp } from '@vicons/ionicons5'
import { LayersSharp } from '@vicons/ionicons5'
import { skillCombinationApi, skillApi } from '@/api'

const message = useMessage()
const dialog = useDialog()

const combinations = ref<any[]>([])
const allSkills = ref<any[]>([])
const loading = ref(false)

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDetailDrawer = ref(false)
const editingCombo = ref<any>(null)
const detailSkills = ref<any[]>([])
const comboCols = ref(3)

const formData = ref({ name: '', description: '' })
const editSkills = ref<number[]>([])

function updateComboCols() {
  const w = window.innerWidth
  comboCols.value = w < 480 ? 1 : w < 768 ? 2 : 3
}

onMounted(() => {
  loadCombinations()
  loadAllSkills()
  updateComboCols()
  window.addEventListener('resize', updateComboCols)
})

onUnmounted(() => {
  window.removeEventListener('resize', updateComboCols)
})

async function loadCombinations() {
  loading.value = true
  try {
    const res = await skillCombinationApi.list()
    combinations.value = res.data || []
  } catch { message.error('加载组合列表失败') }
  finally { loading.value = false }
}

async function loadAllSkills() {
  try {
    const res = await skillApi.list()
    allSkills.value = (res.data || []).map((s: any) => ({ label: `${s.name} (ID:${s.id})`, value: s.id }))
  } catch { /* ignore */ }
}

function openCreate() {
  formData.value = { name: '', description: '' }
  showCreateModal.value = true
}

async function handleCreate() {
  if (!formData.value.name) { message.error('请输入名称'); return }
  try {
    await skillCombinationApi.create(formData.value)
    message.success('创建成功')
    showCreateModal.value = false
    loadCombinations()
  } catch { message.error('创建失败') }
}

function openEdit(row: any) {
  editingCombo.value = row
  formData.value = { name: row.name, description: row.description || '' }
  editSkills.value = []
  showEditModal.value = true
}

async function handleUpdate() {
  if (!editingCombo.value) return
  try {
    await skillCombinationApi.update(editingCombo.value.id, formData.value)
    message.success('更新成功')
    showEditModal.value = false
    editingCombo.value = null
    loadCombinations()
  } catch { message.error('更新失败') }
}

function confirmDelete(row: any) {
  dialog.warning({
    title: '确认删除',
    content: `确定要删除组合「${row.name}」吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await skillCombinationApi.delete(row.id)
        message.success('删除成功')
        loadCombinations()
      } catch { message.error('删除失败') }
    },
  })
}



async function showDetail(row: any) {
  editingCombo.value = row
  addSkillComboId.value = row.id
  try {
    const res = await skillCombinationApi.get(row.id)
    detailSkills.value = res.data?.skills || []
    // 打开抽屉时刷新技能列表
    await loadAllSkills()
    showDetailDrawer.value = true
  } catch { message.error('加载详情失败') }
}

async function addSkill(comboId: number, skillId: number) {
  try {
    await skillCombinationApi.addSkill(comboId, skillId)
    loadCombinations()
  } catch { message.error('添加失败') }
}

async function removeSkill(comboId: number, skillId: number) {
  try {
    await skillCombinationApi.removeSkill(comboId, skillId)
    // 刷新详情
    const res = await skillCombinationApi.get(comboId)
    detailSkills.value = res.data?.skills || []
    loadCombinations()
  } catch { message.error('移除失败') }
}

// 已有技能的 ID 集合（用于在添加对话框中标记已添加项）
const addedSkillIds = computed(() => new Set(detailSkills.value.map((s: any) => s.id)))

// 所有技能选项（已添加的标为 disabled）
const availableSkillOptions = computed(() =>
  allSkills.value.map((opt: any) => ({
    ...opt,
    disabled: addedSkillIds.value.has(opt.value),
  }))
)

// 多选添加技能对话框
const showAddSkillModal = ref(false)
const addSkillComboId = ref(0)
const addSkillSelected = ref<number[]>([])

function openAddSkill(comboId: number) {
  addSkillComboId.value = comboId
  addSkillSelected.value = []
  showAddSkillModal.value = true
}

async function confirmAddSkill() {
  if (!addSkillSelected.value.length) { message.error('请选择至少一个技能'); return }
  for (const sid of addSkillSelected.value) {
    await addSkill(addSkillComboId.value, sid)
  }
  message.success(`成功添加 ${addSkillSelected.value.length} 个技能`)
  showAddSkillModal.value = false
  // 刷新详情
  const res = await skillCombinationApi.get(addSkillComboId.value)
  detailSkills.value = res.data?.skills || []
  addSkillSelected.value = []
}
</script>

<template>
  <div>
      <div class="page-header" style="display:flex;justify-content:space-between;align-items:center">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><LayersSharp /></NIcon>
          技能组合
        </h2>
      <NButton @click="openCreate" type="primary">
        <template #icon><NIcon><AddSharp /></NIcon></template>新建组合
      </NButton>
    </div>

    <NGrid :x-gap="16" :y-gap="16" :cols="comboCols">
      <NGi v-for="combo in combinations" :key="combo.id">
        <NCard :title="combo.name" hoverable @click="showDetail(combo)">
          <template #header-extra>
            <NSpace>
              <NButton size="tiny" quaternary @click.stop="openEdit(combo)">编辑</NButton>
              <NButton size="tiny" quaternary type="error" @click.stop="confirmDelete(combo)">删除</NButton>
            </NSpace>
          </template>
          <p style="color:#888;font-size:13px;">{{ combo.description || '暂无描述' }}</p>
          <p style="margin-top:8px;">
            <NTag type="info" size="small">{{ combo.skill_count }} 个技能</NTag>
          </p>
        </NCard>
      </NGi>
    </NGrid>

    <div v-if="!combinations.length" style="text-align:center;padding:60px 0;color:#666;">暂无技能组合，点击右上角新建</div>

    <!-- 创建 Modal -->
    <NModal v-model:show="showCreateModal" title="新建组合" preset="card" style="width:450px;">
      <NForm :model="formData" label-placement="top">
        <NFormItem label="名称"><NInput v-model:value="formData.name" placeholder="组合名称" /></NFormItem>
        <NFormItem label="描述"><NInput v-model:value="formData.description" type="textarea" placeholder="组合描述" /></NFormItem>
      </NForm>
      <template #footer><NSpace justify="end"><NButton @click="showCreateModal=false">取消</NButton><NButton type="primary" @click="handleCreate">创建</NButton></NSpace></template>
    </NModal>

    <!-- 编辑 Modal -->
    <NModal v-model:show="showEditModal" title="编辑组合" preset="card" style="width:550px;">
      <NForm :model="formData" label-placement="top">
        <NFormItem label="名称"><NInput v-model:value="formData.name" /></NFormItem>
        <NFormItem label="描述"><NInput v-model:value="formData.description" type="textarea" /></NFormItem>
      </NForm>
      <template #footer><NSpace justify="end"><NButton @click="showEditModal=false">取消</NButton><NButton type="primary" @click="handleUpdate">更新</NButton></NSpace></template>
    </NModal>

    <!-- 添加技能 Modal（支持多选，已添加的技能显示为灰色） -->
    <NModal v-model:show="showAddSkillModal" title="添加技能" preset="card" style="width:550px;">
      <NAlert type="info" :bordered="false" style="margin-bottom:12px;">
        搜索并选择要添加到组合的技能，可多选。<strong>灰色项</strong>为已添加，不可重复选择。
      </NAlert>
      <NSelect v-model:value="addSkillSelected" :options="availableSkillOptions" placeholder="搜索技能..." multiple filterable :max-tag-count="10" />
      <template #footer><NSpace justify="end"><NButton @click="showAddSkillModal=false">取消</NButton><NButton type="primary" :disabled="!addSkillSelected.length" @click="confirmAddSkill">添加 {{ addSkillSelected.length > 0 ? `(${addSkillSelected.length})` : '' }}</NButton></NSpace></template>
    </NModal>

    <!-- 详情 Drawer -->
    <NDrawer v-model:show="showDetailDrawer" :width="550" placement="right">
      <NDrawerContent :title="`组合详情: ${editingCombo?.name || ''}`">
        <div style="margin-bottom:16px;">
          <NButton type="primary" size="small" @click="openAddSkill(addSkillComboId)">
            <template #icon><NIcon><AddSharp /></NIcon></template>添加技能
          </NButton>
        </div>
        <div v-if="!detailSkills.length" style="color:#888;padding:20px;text-align:center;">该组合暂无技能，点击上方按钮添加</div>
        <NCard v-for="sk in detailSkills" :key="sk.id" size="small" :title="sk.name" style="margin-bottom:8px;">
          <template #header-extra>
            <NButton size="tiny" type="error" quaternary @click="removeSkill(addSkillComboId, sk.id)">移除</NButton>
          </template>
          <p style="font-size:12px;color:#888;">{{ sk.description }}</p>
          <div v-if="sk.tags?.length">
            <NTag v-for="t in sk.tags" :key="t" size="small" style="margin-right:4px;">{{ t }}</NTag>
          </div>
          <div v-if="sk.files?.length" style="margin-top:8px;">
            <div v-for="f in sk.files" :key="f.path" style="font-size:12px;padding:2px 0;">📄 {{ f.path }}</div>
          </div>
        </NCard>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>
