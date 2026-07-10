<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import {
  NDataTable, NButton, NModal, NForm, NFormItem, NInput, NSelect,
  NTag, NSpace, NIcon, NTree, NDrawer, NDrawerContent, NAlert, NDivider, useMessage, useDialog,
} from 'naive-ui'
import { CloudDownloadSharp, CloudUploadSharp, DocumentTextSharp } from '@vicons/ionicons5'
import { skillApi } from '@/api'
import { formatDateTime } from '@/utils/format'

const message = useMessage()
const dialog = useDialog()

const skills = ref<any[]>([])
const loading = ref(false)
const searchQuery = ref('')
const selectedTag = ref('')
const allTags = ref<string[]>([])

const showEditModal = ref(false)
const showImportModal = ref(false)
const showUploadModal = ref(false)
const editingSkill = ref<any>(null)

// 编辑表单：只编辑元数据
const editForm = ref({ name: '', description: '', tags: [] as string[] })

// 文件预览
const previewFile = ref<{ path: string; content: string } | null>(null)
const showPreview = ref(false)

// 导入
// 导入
const importUrl = ref('')
const importLoading = ref(false)

// 仓库导入
const repoUrl = ref('')
const repoImportLoading = ref(false)

// 上传
const uploadLoading = ref(false)

onMounted(() => {
  loadSkills()
  loadTags()
})

async function loadSkills() {
  loading.value = true
  try {
    const params: any = {}
    if (searchQuery.value) params.q = searchQuery.value
    if (selectedTag.value) params.tag = selectedTag.value
    const res = await skillApi.list(params)
    skills.value = res.data
  } catch (e: any) {
    message.error('加载技能列表失败')
  } finally {
    loading.value = false
  }
}

async function loadTags() {
  try {
    const res = await skillApi.getTags()
    allTags.value = res.data || []
  } catch { /* ignore */ }
}

function openEdit(row: any) {
  editingSkill.value = row
  editForm.value = {
    name: row.name || '',
    description: row.description || '',
    tags: row.tags || [],
  }
  showEditModal.value = true
}

async function handleUpdate() {
  if (!editingSkill.value) return
  try {
    await skillApi.update(editingSkill.value.id, editForm.value)
    message.success('更新成功')
    showEditModal.value = false
    editingSkill.value = null
    loadSkills()
  } catch (e: any) {
    message.error(e.response?.data?.error || '更新失败')
  }
}

function confirmDelete(row: any) {
  dialog.warning({
    title: '确认删除',
    content: `确定要删除技能「${row.name}」吗？将同时删除所有关联文件和组合关联。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await skillApi.delete(row.id)
        message.success('删除成功')
        loadSkills()
      } catch (e: any) {
        message.error('删除失败')
      }
    },
  })
}

async function handleImport() {
  if (!importUrl.value) {
    message.error('请输入 GitHub URL')
    return
  }
  importLoading.value = true
  try {
    const res = await skillApi.importFromGitHub(importUrl.value)
    message.success(res.data?.message || '导入成功')
    showImportModal.value = false
    importUrl.value = ''
    loadSkills()
  } catch (e: any) {
    message.error(e.response?.data?.error || '导入失败')
  } finally {
    importLoading.value = false
  }
}

// 仓库导入
async function handleRepoImport() {
  if (!repoUrl.value) {
    message.error('请输入 GitHub 仓库 URL')
    return
  }
  repoImportLoading.value = true
  try {
    const res = await skillApi.importRepo(repoUrl.value)
    const msg = res.data?.message || ''
    const skills = res.data?.skills || []
    message.success(msg || `成功导入 ${skills.length} 个技能`)
    showImportModal.value = false
    repoUrl.value = ''
    loadSkills()
  } catch (e: any) {
    message.error(e.response?.data?.error || '导入失败')
  } finally {
    repoImportLoading.value = false
  }
}

// ZIP 上传
async function handleUpload(data: { file: File }) {
  const fd = new FormData()
  fd.append('file', data.file)
  uploadLoading.value = true
  try {
    const res = await skillApi.upload(fd)
    message.success(res.data?.message || '上传成功')
    showUploadModal.value = false
    loadSkills()
  } catch (e: any) {
    message.error(e.response?.data?.error || '上传失败')
  } finally {
    uploadLoading.value = false
  }
}

// 文件夹直接上传（webkitdirectory）
async function handleFolderUpload(e: Event) {
  const input = e.target as HTMLInputElement
  if (!input.files?.length) return

  const files = Array.from(input.files)
  const fd = new FormData()
  const paths: { name: string; path: string }[] = []

  for (const file of files) {
    const relPath = (file as any).webkitRelativePath || file.name
    paths.push({ name: file.name, path: relPath })
    fd.append('files', file, file.name)
  }
  fd.append('paths', JSON.stringify(paths))

  uploadLoading.value = true
  try {
    const res = await skillApi.uploadFolder(fd)
    message.success(res.data?.message || '上传成功')
    showUploadModal.value = false
    input.value = '' // 清空选择
    loadSkills()
  } catch (e: any) {
    message.error(e.response?.data?.error || '上传失败')
  } finally {
    uploadLoading.value = false
  }
}

async function previewFileContent(row: any, filePath: string) {
  try {
    const res = await skillApi.getFile(row.id, filePath)
    previewFile.value = { path: filePath, content: typeof res.data === 'string' ? res.data : '(二进制文件)' }
    showPreview.value = true
  } catch {
    message.error('加载文件内容失败')
  }
}

function getFileTree(files: any[]) {
  if (!files) return []
  const sorted = [...files].sort((a, b) => {
    if (a.path === 'SKILL.md') return -1
    if (b.path === 'SKILL.md') return 1
    return a.path.localeCompare(b.path)
  })
  const tree: any[] = []
  for (const f of sorted) {
    const parts = f.path.split('/')
    let current = tree
    for (let i = 0; i < parts.length; i++) {
      const isFile = i === parts.length - 1
      const labelKey = parts[i] === 'SKILL.md' ? '📄 SKILL.md (技能入口)' : (isFile ? '📄 ' + parts[i] : '📁 ' + parts[i])
      const existing = current.find((n: any) => n.label === labelKey)
      if (existing) {
        current = existing.children || []
      } else {
        const node: any = { label: labelKey, key: f.path, isLeaf: isFile }
        current.push(node)
        if (!isFile) { node.children = []; current = node.children }
      }
    }
  }
  return tree
}

const columns = [
  { title: '名称', key: 'name', width: 160 },
  { title: '描述', key: 'description', ellipsis: true },
  {
    title: '来源', key: 'type', width: 70,
    render: (row: any) => h(NTag, { size: 'small', type: row.type === 'github' ? 'info' : 'default' }, { default: () => row.type === 'github' ? 'GitHub' : '本地' }),
  },
  {
    title: '标签', key: 'tags', minWidth: 140,
    ellipsis: { tooltip: true },
    render: (row: any) => (row.tags || []).map((t: string) => h(NTag, { size: 'small', style: 'margin:2px 4px 2px 0;max-width:200px;overflow:hidden;text-overflow:ellipsis;' }, { default: () => t })),
  },
  { title: '文件数', key: 'files', width: 70, render: (row: any) => row.files?.length || 0 },
  { title: '创建时间', key: 'created_at', width: 150, render: (r: any) => formatDateTime(r.created_at) },
  {
    title: '操作', key: 'actions', width: 130,
    render: (row: any) => h(NSpace, {}, {
      default: () => [
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => openEdit(row) }, { default: () => '编辑' }),
        h(NButton, { size: 'tiny', type: 'error', quaternary: true, onClick: () => confirmDelete(row) }, { default: () => '删除' }),
      ],
    }),
  },
]

function expandRow(row: any) {
  const files = row.files || []
  if (!files.length) return h('div', { style: 'padding:12px;color:#888;' }, '暂无文件')
  return h('div', { style: 'padding:8px 24px;' }, [
    h('div', { style: 'margin-bottom:8px;font-size:12px;color:#888;' }, `📂 共 ${files.length} 个文件 · 点击文件预览内容`),
    h(NTree, {
      data: getFileTree(files),
      defaultExpandAll: true,
      selectable: true,
      'onUpdate:selectedKeys': (keys: string[]) => {
        if (keys.length) previewFileContent(row, keys[0])
      },
      style: 'background:transparent;',
    }),
  ])
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header" style="display:flex;justify-content:space-between;align-items:center">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><DocumentTextSharp /></NIcon>
          技能管理
        </h2>
        <NSpace>
          <NButton @click="showUploadModal = true" type="primary">
            <template #icon><NIcon><CloudUploadSharp /></NIcon></template>
            上传技能文件夹
          </NButton>
          <NButton @click="showImportModal = true" type="warning">
            <template #icon><NIcon><CloudDownloadSharp /></NIcon></template>
            从 GitHub 导入
          </NButton>
        </NSpace>
      </div>

    <div style="display:flex; gap:12px; margin-bottom:16px;">
      <NInput v-model:value="searchQuery" placeholder="搜索技能名称/描述" clearable style="max-width:300px;" @keyup.enter="loadSkills" />
      <NSelect v-model:value="selectedTag" :options="allTags.map(t => ({ label: t, value: t }))" placeholder="按标签筛选" clearable style="max-width:200px;" @update:value="loadSkills" />
      <NButton @click="loadSkills">搜索</NButton>
    </div>

    <NCard style="width:100%">
      <NDataTable :columns="columns" :data="skills" :loading="loading" :row-key="(r:any) => r.id" striped>
      <template #empty>
        <div style="padding:40px;text-align:center;color:#888;">
          <p>暂无技能</p>
          <p style="margin-top:8px;font-size:13px;">点击「上传技能文件夹」上传本地的 SKILL.md 技能包，或从 GitHub 导入</p>
        </div>
      </template>
    </NDataTable>
    </NCard>

    <!-- skill 说明 -->
    <div style="margin-top:16px;padding:12px 16px;background:#1a1a2e;border-radius:6px;font-size:13px;color:#888;line-height:1.8;">
      <strong style="color:#ccc;">📦 什么是 Skill？</strong><br/>
      Skill 是 AI Agent 的指令包，每个 skill 是一个包含 <code>SKILL.md</code> 的文件夹：
      <pre style="margin-top:6px;padding:8px;background:#0d0d1a;border-radius:4px;font-size:12px;">
my-skill/
├── SKILL.md          ← 入口文件（YAML frontmatter 定义 name/description + 指令内容）
├── scripts/          ← 可选：可执行脚本
├── references/       ← 可选：参考文档
└── examples/         ← 可选：示例输出</pre>
    </div>

    <!-- 编辑 Modal -->
    <NModal v-model:show="showEditModal" title="编辑技能元数据" preset="card" style="width:500px;">
      <NForm :model="editForm" label-placement="top">
        <NFormItem label="名称">
          <NInput v-model:value="editForm.name" placeholder="技能标识符" />
        </NFormItem>
        <NFormItem label="描述">
          <NInput v-model:value="editForm.description" type="textarea" placeholder="技能描述" />
        </NFormItem>
        <NFormItem label="标签">
          <NSelect v-model:value="editForm.tags" multiple filterable tag placeholder="输入标签后回车" :options="allTags.map(t => ({ label: t, value: t }))" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="showEditModal = false">取消</NButton>
          <NButton type="primary" @click="handleUpdate">更新</NButton>
        </NSpace>
      </template>
    </NModal>

    <!-- GitHub 导入 Modal -->
    <NModal v-model:show="showImportModal" title="从 GitHub 导入" preset="card" style="width:600px;">
      <NDivider style="margin-top:0;">导入仓库（推荐）</NDivider>
      <p style="margin-bottom:8px; font-size:13px; color:#888;">
        扫描整个仓库，自动发现所有包含 <code>SKILL.md</code> 的技能目录，批量导入并创建技能组合。<br/>
        例如 <code>https://github.com/Leonxlnx/taste-skill</code> 或 <code>https://github.com/owner/repo/tree/main/skills</code>
      </p>
      <NInput v-model:value="repoUrl" placeholder="https://github.com/owner/repo" style="margin-bottom:8px;" />
      <NSpace justify="end" style="margin-bottom:16px;">
        <NButton type="primary" :loading="repoImportLoading" @click="handleRepoImport">
          <template #icon><NIcon><CloudDownloadSharp /></NIcon></template>扫描并导入仓库
        </NButton>
      </NSpace>

      <NDivider>导入单个技能</NDivider>
      <p style="margin-bottom:8px; font-size:13px; color:#888;">
        导入 GitHub 仓库中的单个 skill 目录：<br/>
        <code>https://github.com/user/repo/tree/main/skills/my-skill</code>
      </p>
      <NInput v-model:value="importUrl" placeholder="https://github.com/..." style="margin-bottom:8px;" />
      <NSpace justify="end">
        <NButton :loading="importLoading" @click="handleImport">
          <template #icon><NIcon><CloudDownloadSharp /></NIcon></template>导入单个技能
        </NButton>
      </NSpace>

      <template #footer>
        <NSpace justify="end">
          <NButton @click="showImportModal = false">关闭</NButton>
        </NSpace>
      </template>
    </NModal>

    <!-- ZIP 上传 Modal -->
    <NModal v-model:show="showUploadModal" title="上传技能" preset="card" style="width:550px;">
      <div style="margin-bottom:20px;">
        <h4 style="margin-bottom:8px;">📁 方式一：选择文件夹</h4>
        <p style="font-size:13px;color:#888;margin-bottom:8px;">直接选择本地的 skill 文件夹（需包含 SKILL.md）</p>
        <input type="file" webkitdirectory multiple @change="handleFolderUpload" style="display:block;margin-bottom:8px;" />
      </div>

      <NAlert type="info" :bordered="false" closable>
        <template #header>📦 方式二：上传 zip 包</template>
        将 skill 文件夹打包为 <strong>.zip</strong> 后上传
      </NAlert>

      <div style="margin-top:12px;">
        <input type="file" accept=".zip" @change="(e: any) => { const f = e.target.files?.[0]; if (f) handleUpload({ file: f }) }" />
      </div>

      <div v-if="uploadLoading" style="margin-top:12px;text-align:center;color:#888;">正在上传...</div>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="showUploadModal = false">取消</NButton>
        </NSpace>
      </template>
    </NModal>

    <!-- 文件内容预览 Drawer -->
    <NDrawer v-model:show="showPreview" :width="550" placement="right">
      <NDrawerContent :title="previewFile?.path">
        <pre style="white-space:pre-wrap;font-size:13px;line-height:1.6;font-family:monospace;">{{ previewFile?.content }}</pre>
      </NDrawerContent>
    </NDrawer>
    </NSpace>
  </NSpin>
</template>
