<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { NCard, NInputGroup, NInputGroupLabel, NInput, NButton, NSpace, NAlert, NIcon, useMessage } from 'naive-ui'
import { CopySharp, CodeSlashSharp, SaveSharp } from '@vicons/ionicons5'
import { mcpApi } from '@/api'
import { copyToClipboard } from '@/utils/clipboard'

const message = useMessage()

const mcpUrl = ref('')
const mcpHost = ref('localhost')
const mcpEnabled = ref(true)
const mcpHasToken = ref(false)
const mcpToken = ref('')
const mcpSkillsDir = ref('data/skills')
const githubToken = ref('')
const githubTokenLoading = ref(false)

const hasToken = computed(() => mcpHasToken.value && !!mcpToken.value)

onMounted(async () => {
  try {
    const res = await mcpApi.status()
    const data = res.data
    mcpEnabled.value = data.enabled
    mcpHasToken.value = data.has_token
    mcpToken.value = data.token || ''
    mcpSkillsDir.value = data.skills_dir || 'data/skills'
    githubToken.value = data.github_token || ''

    const hostname = window.location.hostname
    const port = window.location.port || '8080'
    mcpHost.value = hostname
    mcpUrl.value = `${window.location.protocol}//${hostname}:${port}${data.path || '/mcp'}`
  } catch (e: any) {
    message.error('获取 MCP 配置失败')
  }
})

function copyUrl() {
  copyToClipboard(mcpUrl.value).then((ok) => {
    if (ok) message.success('已复制 MCP 连接 URL')
    else message.error('复制失败')
  })
}

function copyToken() {
  if (mcpToken.value) {
    copyToClipboard(mcpToken.value).then((ok) => {
      if (ok) message.success('已复制 Token')
      else message.error('复制失败, 请手动复制')
    })
  }
}

async function saveGitHubToken() {
  githubTokenLoading.value = true
  try {
    await mcpApi.updateGitHubToken(githubToken.value)
    message.success('GitHub Token 已保存')
  } catch (e: any) {
    message.error(e.response?.data?.error || '保存失败')
  } finally {
    githubTokenLoading.value = false
  }
}

const configNoToken = computed(() => `{
  "mcpServers": {
    "zero-api-skills": {
      "url": "${mcpUrl.value}"
    }
  }
}`)

const configWithToken = computed(() => `{
  "mcpServers": {
    "zero-api-skills": {
      "url": "${mcpUrl.value}",
      "headers": {
        "Authorization": "Bearer ${mcpToken.value || 'your-token-here'}"
      }
    }
  }
}`)

const curlNoToken = computed(() => `curl -X POST ${mcpUrl.value} \\
  -H "Content-Type: application/json" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`)

const curlWithToken = computed(() => `curl -X POST ${mcpUrl.value} \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer ${mcpToken.value || 'your-token-here'}" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`)
</script>

<template>
  <div>
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><CodeSlashSharp /></NIcon>
          MCP 设置
        </h2>
        <p class="page-subtitle">AI Agent Skill MCP 服务连接管理</p>
      </div>

      <NCard title="MCP 服务状态">
        <NSpace vertical>
          <NInputGroup>
            <NInputGroupLabel>服务地址</NInputGroupLabel>
            <NInput :value="mcpUrl" readonly />
            <NButton @click="copyUrl"><template #icon><CopySharp /></template>复制</NButton>
          </NInputGroup>
          <NInputGroup>
            <NInputGroupLabel>认证状态</NInputGroupLabel>
            <NInput :value="hasToken ? '已启用（需 Bearer Token）' : '未启用（无需认证）'" readonly :style="{ width: '300px' }" />
          </NInputGroup>
          <NInputGroup v-if="hasToken">
            <NInputGroupLabel>Token</NInputGroupLabel>
            <NInput :value="mcpToken" type="password" show-password-on="click" readonly />
            <NButton @click="copyToken"><template #icon><CopySharp /></template>复制</NButton>
          </NInputGroup>
          <NInputGroup>
            <NInputGroupLabel>文件目录</NInputGroupLabel>
            <NInput :value="mcpSkillsDir" readonly />
          </NInputGroup>
        </NSpace>
      </NCard>

      <NCard title="GitHub Token 配置">
        <p style="font-size:13px;color:#888;margin-bottom:8px;">
          用于从 GitHub 导入技能时的 API 认证，可提升速率限制至 5000次/小时。
          所有 <code>import-repo</code> / <code>sync-repo</code> / <code>import-github</code> 请求将默认使用此 Token。
        </p>
        <NInputGroup>
          <NInput v-model:value="githubToken" type="password" show-password-on="click" placeholder="ghp_xxxxxxxxxxxxxxxxxxxx" />
          <NButton :loading="githubTokenLoading" @click="saveGitHubToken">
            <template #icon><SaveSharp /></template>保存
          </NButton>
        </NInputGroup>
      </NCard>

      <NCard title="Agent 配置">
        <NAlert v-if="hasToken" type="warning" :bordered="false" style="margin-bottom:12px;">
          ⚠️ 当前已启用 Token 认证，MCP 客户端配置中必须添加 <code>headers.Authorization</code>。
        </NAlert>
        <NAlert v-else type="info" :bordered="false" style="margin-bottom:12px;">
          ℹ️ 当前未启用 Token 认证，Agent 可直接连接。
        </NAlert>

        <h4 style="margin-bottom:8px;">Claude Desktop / Cursor 配置</h4>
        <pre style="background:#1a1a2e;padding:12px;border-radius:6px;font-size:12px;overflow-x:auto;white-space:pre-wrap;">{{ hasToken ? configWithToken : configNoToken }}</pre>

        <h4 style="margin:16px 0 8px;">curl 测试命令</h4>
        <pre style="background:#1a1a2e;padding:12px;border-radius:6px;font-size:12px;overflow-x:auto;white-space:pre-wrap;">{{ hasToken ? curlWithToken : curlNoToken }}</pre>
      </NCard>

      <NCard title="使用说明">
        <ol style="line-height:2;">
          <li>在技能管理页面创建技能或从 GitHub 导入</li>
          <li>在技能组合页面将相关技能组合在一起</li>
          <li>复制上方 Agent 配置到其 MCP 配置文件中</li>
          <li>Agent 即可通过 MCP 工具发现和使用技能</li>
          <li>如需开启 Token 认证，编辑 <code>configs/config.yaml</code> 中的 <code>mcp.token</code> 并重启</li>
        </ol>
      </NCard>
    </NSpace>
  </div>
</template>
