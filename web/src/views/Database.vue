<script setup lang="ts">
import { ref } from 'vue'
import {
  NCard, NButton, NSpace, NAlert, NSpin, NIcon, NUpload,
  NModal, NPopconfirm, useMessage,
} from 'naive-ui'
import { CloudDownloadSharp, CloudUploadSharp } from '@vicons/ionicons5'
import api from '@/api'

const message = useMessage()
const uploading = ref(false)
const downloading = ref(false)
const showRestoreConfirm = ref(false)
const selectedFile = ref<File | null>(null)

async function downloadBackup() {
  downloading.value = true
  try {
    const res = await api.get('/database/backup', {
      responseType: 'blob',
      timeout: 60000,
    })
    // 从 Content-Disposition 获取文件名
    const disposition = res.headers['content-disposition'] || ''
    let filename = 'zero-api-backup.db'
    const match = disposition.match(/filename="?([^";\n]+)"?/)
    if (match) filename = match[1]

    const blob = new Blob([res.data])
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    window.URL.revokeObjectURL(url)
    message.success('数据库备份已下载')
  } catch (e: any) {
    message.error(e.response?.data?.error || '备份下载失败')
  } finally {
    downloading.value = false
  }
}

function handleUploadChange(options: any) {
  const file = options.file
  selectedFile.value = file?.file || null
}

async function doRestore() {
  if (!selectedFile.value) {
    message.warning('请先选择数据库文件')
    return
  }
  showRestoreConfirm.value = true
}

async function confirmRestore() {
  showRestoreConfirm.value = false
  uploading.value = true
  try {
    const formData = new FormData()
    formData.append('file', selectedFile.value!)
    const res = await api.post('/database/restore', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 60000,
    })
    message.success(res.data.message || '数据库恢复成功')
    selectedFile.value = null
  } catch (e: any) {
    message.error(e.response?.data?.error || '恢复失败')
  } finally {
    uploading.value = false
  }
}
</script>

<template>
  <NSpin :show="uploading || downloading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><CloudDownloadSharp /></NIcon>
          数据库管理
        </h2>
      </div>

      <!-- 备份 -->
      <NCard title="导出数据库">
        <NAlert type="info" style="margin-bottom:12px">
          下载当前数据库的完整备份文件（.db），可用于迁移、备份或存档。
        </NAlert>
        <NButton type="primary" :loading="downloading" @click="downloadBackup">
          <template #icon><NIcon><CloudDownloadSharp /></NIcon></template>
          下载备份
        </NButton>
      </NCard>

      <!-- 恢复 -->
      <NCard title="导入数据库">
        <NAlert type="warning" style="margin-bottom:12px">
          上传一个 SQLite 数据库文件（.db）来恢复数据。<strong>当前数据库将被替换</strong>，旧数据库会自动备份为 <code>*.db.old</code>。
        </NAlert>
        <NSpace vertical>
          <NUpload
            :max="1"
            accept=".db,.sqlite,.sqlite3"
            :default-upload="false"
            :custom-request="() => {}"
            @change="handleUploadChange"
          >
            <NButton>选择数据库文件</NButton>
          </NUpload>
          <NButton
            type="warning"
            :disabled="!selectedFile"
            :loading="uploading"
            @click="doRestore"
          >
            <template #icon><NIcon><CloudUploadSharp /></NIcon></template>
            恢复数据库
          </NButton>
        </NSpace>
      </NCard>

      <!-- 使用说明 -->
      <NCard title="使用说明">
        <NAlert type="info">
          <p><strong>迁移步骤：</strong></p>
          <p>1. 在旧服务器上点击「下载备份」</p>
          <p>2. 在新服务器的管理面板中进入此页面</p>
          <p>3. 选择下载的 .db 文件，点击「恢复数据库」</p>
          <br />
          <p><strong>注意事项：</strong></p>
          <p>• 恢复操作会替换当前数据库，旧数据库自动备份为 *.db.old</p>
          <p>• 恢复过程中服务会短暂中断（通常不到 1 秒）</p>
          <p>• 备份文件包含所有数据：渠道、模型、API 密钥、使用记录、代理配置</p>
        </NAlert>
      </NCard>
    </NSpace>
  </NSpin>

  <!-- 恢复确认弹窗 -->
  <NModal v-model:show="showRestoreConfirm" preset="dialog" title="确认恢复" type="warning">
    <template #default>
      <p>确定要用上传的文件恢复数据库吗？</p>
      <p style="color:#f97316;font-size:13px;margin-top:8px">
        ⚠️ 当前数据库将被替换，旧数据库会备份为 *.db.old
      </p>
    </template>
    <template #action>
      <NSpace>
        <NButton @click="showRestoreConfirm = false">取消</NButton>
        <NButton type="warning" @click="confirmRestore">确认恢复</NButton>
      </NSpace>
    </template>
  </NModal>
</template>
