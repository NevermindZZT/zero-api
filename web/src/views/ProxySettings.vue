<script setup lang="ts">
import { onMounted, ref, computed, h } from 'vue'
import {
  NCard, NForm, NFormItem, NInput, NInputNumber, NButton, NSpace,
  NTag, NSelect, NSwitch, NModal, NPopconfirm, NDivider,
  useMessage, NAlert, NSpin, NIcon, NDataTable,
} from 'naive-ui'
import { ShieldCheckmarkSharp, SwapHorizontalSharp, ChevronDownSharp } from '@vicons/ionicons5'
import { proxyApi, modelApi } from '@/api'

const message = useMessage()
const loading = ref(true)
const config = ref({
  intercept_domains: [] as string[],
  smart_intercept_domains: [] as string[],
  model_mappings: {} as Record<string, any>,
  mitm_all: false,
  proxy_username: '',
  proxy_password: '',
})

// 所有模型列表（用于下拉选择）
const allModels = ref<any[]>([])

const hostname = computed(() => {
  if (typeof window !== 'undefined') {
    return window.location.hostname
  }
  return '127.0.0.1'
})

const newDomain = ref('')

// 模型映射
const showMappingModal = ref(false)
const editingMappingKey = ref('')
const modelSearch = ref('')
const mappingForm = ref({
  source_model: '',
  target_model: '',
  name: '',
  context_window: 1048576,
  max_output_tokens: 64000,
  thinking: false,
  reasoning_effort: 'high',
  vision: false,
})

// 模型选择弹出控制
const showSourcePopover = ref(false)
const showTargetPopover = ref(false)

// 弹窗内模型搜索过滤
const filteredModelOptions = computed(() => {
  const q = modelSearch.value.toLowerCase().trim()
  if (!q) return modelOptions.value
  return modelOptions.value.filter(o => o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q))
})

// 从 allModels 中查找模型信息
function findModelInfo(modelId: string) {
  return allModels.value.find((m: any) => m.model_id === modelId) || null
}

// 根据模型信息自动填充表单
function applyModelInfo(modelId: string) {
  const info = findModelInfo(modelId)
  if (!info) return
  if (info.context_window > 0) mappingForm.value.context_window = info.context_window
  if (info.max_output_tokens > 0) mappingForm.value.max_output_tokens = info.max_output_tokens
  mappingForm.value.thinking = !!info.supports_thinking
  mappingForm.value.vision = !!info.supports_vision
  if (!mappingForm.value.name) mappingForm.value.name = info.display_name || modelId
}

// 弹窗内快速选择源模型
function pickSourceModel(val: string) {
  mappingForm.value.source_model = val
  modelSearch.value = ''
  showSourcePopover.value = false
  // 仅参数注入时（目标模型为空），用源模型信息自动填充
  if (!mappingForm.value.target_model) {
    applyModelInfo(val)
  }
}

// 弹窗内快速选择目标模型（自动填充模型信息）
function pickTargetModel(val: string) {
  mappingForm.value.target_model = val
  modelSearch.value = ''
  showTargetPopover.value = false
  // 自动填充模型属性
  applyModelInfo(val)
}

// 已有模型 ID 选项（用于快速选择）
const modelOptions = computed(() => {
  const seen = new Set<string>()
  const options: { label: string; value: string }[] = []
  for (const m of allModels.value) {
    const id = m.model_id || ''
    if (id && !seen.has(id)) {
      seen.add(id)
      options.push({ label: `${id} (${m.display_name || ''})`, value: id })
    }
  }
  options.sort((a, b) => a.value.localeCompare(b.value))
  return options
})

const mappingColumns = [
  { title: '源模型名', key: 'source', width: 140, ellipsis: true },
  {
    title: '目标模型', key: 'target_model', width: 160,
    render: (r: any) => {
      if (!r.target_model) return h(NTag, { size: 'tiny', type: 'info', bordered: false }, () => '仅参数注入')
      return r.target_model
    },
  },
  { title: '名称', key: 'name', width: 120 },
  { title: '上下文', key: 'context_window', width: 80, render: (r: any) => formatContext(r.context_window) },
  { title: '思考', key: 'thinking', width: 60, render: (r: any) => r.thinking ? '✅' : '-' },
  {
    title: '推理强度', key: 'reasoning_effort', width: 80,
    render: (r: any) => r.thinking ? (r.reasoning_effort || 'high') : '-',
  },
  { title: '视觉', key: 'vision', width: 60, render: (r: any) => r.vision ? '✅' : '-' },
  {
    title: '操作', key: 'actions', width: 100,
    render: (r: any) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => editMapping(r.source) }, () => '编辑'),
        h(NPopconfirm, {
          onPositiveClick: () => deleteMapping(r.source),
        }, {
          default: () => `确认删除映射 "${r.source}"？`,
          trigger: () => h(NButton, { size: 'tiny', type: 'error' }, () => '删除'),
        }),
      ]),
  },
]

const mappingData = computed(() => {
  return Object.entries(config.value.model_mappings || {}).map(([k, v]) => ({
    source: k,
    ...v,
  }))
})

onMounted(() => {
  loadConfig()
  loadAllModels()
})

async function loadAllModels() {
  try {
    const res = await modelApi.list()
    allModels.value = res.data || []
  } catch {
    // 静默失败
  }
}

async function loadConfig() {
  loading.value = true
  try {
    const res = await proxyApi.getConfig()
    config.value = res.data
  } finally {
    loading.value = false
  }
}

function addDomain() {
  const d = newDomain.value.trim().toLowerCase()
  if (!d) return
  if (!config.value.intercept_domains.includes(d)) {
    config.value.intercept_domains.push(d)
  }
  newDomain.value = ''
}

function removeDomain(domain: string) {
  config.value.intercept_domains = config.value.intercept_domains.filter((d) => d !== domain)
}

function formatContext(v: number) {
  if (!v || v === 0) return '-'
  if (v >= 1048576) return (v / 1048576).toFixed(0) + 'M'
  if (v >= 1000) return (v / 1000).toFixed(0) + 'K'
  return String(v)
}

// 模型映射操作
function addMapping() {
  editingMappingKey.value = ''
  mappingForm.value = {
    source_model: '',
    target_model: '',
    name: '',
    context_window: 1048576,
    max_output_tokens: 64000,
    thinking: false,
    reasoning_effort: 'high',
    vision: false,
  }
  showMappingModal.value = true
}

function editMapping(source: string) {
  editingMappingKey.value = source
  const m = config.value.model_mappings[source]
  mappingForm.value = {
    source_model: source,
    target_model: m.target_model || '',
    name: m.name || '',
    context_window: m.context_window || 1048576,
    max_output_tokens: m.max_output_tokens || 64000,
    thinking: m.thinking || false,
    reasoning_effort: m.reasoning_effort || 'high',
    vision: m.vision || false,
  }
  showMappingModal.value = true
}

function deleteMapping(source: string) {
  delete config.value.model_mappings[source]
  saveConfig()
}

function saveMapping() {
  const sourceModel = mappingForm.value.source_model.trim()
  if (!sourceModel) {
    message.warning('请填写源模型名')
    return
  }
  // 如果编辑中且源模型名变了，删除旧的
  if (editingMappingKey.value && editingMappingKey.value !== sourceModel) {
    delete config.value.model_mappings[editingMappingKey.value]
  }
  config.value.model_mappings[sourceModel] = {
    target_model: mappingForm.value.target_model || '',
    name: mappingForm.value.name || mappingForm.value.source_model,
    context_window: mappingForm.value.context_window || 0,
    max_output_tokens: mappingForm.value.max_output_tokens || 0,
    thinking: mappingForm.value.thinking,
    reasoning_effort: mappingForm.value.reasoning_effort,
    vision: mappingForm.value.vision,
  }
  showMappingModal.value = false
  saveConfig()
}

async function saveConfig() {
  try {
    await proxyApi.updateConfig(config.value)
    message.success('代理配置已保存')
  } catch (e: any) {
    message.error(e.response?.data?.error || '保存失败')
  }
}

async function downloadCert(format: string) {
  try {
    const res = await proxyApi.downloadCert(format)
    const blob = new Blob([res.data])
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `zero-api-root-ca.${format}`
    a.click()
    window.URL.revokeObjectURL(url)
  } catch {
    message.error('下载证书失败')
  }
}
</script>

<template>
  <NSpin :show="loading">
    <NSpace vertical size="large">
      <div class="page-header">
        <h2>
          <NIcon size="20" color="#667eea" style="vertical-align:-2px;margin-right:6px"><ShieldCheckmarkSharp /></NIcon>
          代理设置
        </h2>
      </div>

      <NCard title="拦截域名配置">
        <NForm label-placement="left" label-width="120">
          <NFormItem label="代理端口">
            <NInput :value="'8520'" disabled placeholder="默认代理端口" />
          </NFormItem>

          <NFormItem label="拦截域名">
            <div style="width:100%">
              <div style="display:flex;gap:8px;margin-bottom:12px">
                <NInput
                  v-model:value="newDomain"
                  placeholder="例如: api.openai.com"
                  @keyup.enter="addDomain"
                />
                <NButton type="primary" @click="addDomain">添加</NButton>
              </div>
              <NSpace>
                <NTag
                  v-for="d in config.intercept_domains"
                  :key="d"
                  closable
                  @close="removeDomain(d)"
                >
                  {{ d }}
                </NTag>
                <span v-if="!config.intercept_domains?.length" style="color:#888">暂无域名</span>
              </NSpace>
            </div>
          </NFormItem>

          <NFormItem>
            <NButton type="primary" @click="saveConfig">保存配置</NButton>
          </NFormItem>
        </NForm>
      </NCard>

      <!-- 代理高级设置 -->
      <NCard title="代理高级设置">
        <NForm label-placement="left" label-width="120">
          <NFormItem label="全量 MITM">
            <NSwitch v-model:value="config.mitm_all" />
            <span style="color:#94a3b8;font-size:13px;margin-left:8px">
              开启后所有 HTTPS 域名都会尝试 MITM 解密并自动检测 LLM 请求，无需逐个配置拦截域名
            </span>
          </NFormItem>

          <NDivider />

          <NFormItem label="代理用户名">
            <NInput v-model:value="config.proxy_username" placeholder="留空则使用管理后台账号" style="width:200px" />
          </NFormItem>
          <NFormItem label="代理密码">
            <NInput
              v-model:value="config.proxy_password"
              type="password"
              show-password-on="click"
              placeholder="留空则使用管理后台密码"
              style="width:200px"
            />
          </NFormItem>
          <NAlert type="info" style="margin-top:8px">
            设置独立的代理认证信息，客户端需在代理设置中填写。部署到公网时强烈建议开启认证。
          </NAlert>
          <NFormItem style="margin-top:12px">
            <NButton type="primary" @click="saveConfig">保存配置</NButton>
          </NFormItem>
        </NForm>
      </NCard>

      <!-- 模型映射配置 -->
      <NCard title="模型映射">
        <template #header-extra>
          <NButton size="small" type="primary" @click="addMapping">
            <template #icon><NIcon><SwapHorizontalSharp /></NIcon></template>
            添加映射
          </NButton>
        </template>
        <NAlert type="info" style="margin-bottom:12px">
          模型映射允许客户端发送一个模型名（如 gpt-4o），代理自动映射到目标渠道的模型（如 deepseek-chat），
          并注入 thinking/reasoning_effort 等参数。适用于不能自定义 Base URL 的客户端。
        </NAlert>
        <NDataTable
          :columns="mappingColumns"
          :data="mappingData"
          :bordered="false"
          :max-height="400"
          size="small"
          :paginate="false"
        />
      </NCard>

      <!-- 添加/编辑模型映射弹窗 -->
      <NModal v-model:show="showMappingModal" title="模型映射" preset="card" style="width:580px">
        <NForm :model="mappingForm" label-placement="left" label-width="100">
          <!-- 源模型名：NInput + NPopover 快速选择 -->
          <NFormItem label="源模型名">
            <NInput
              v-model:value="mappingForm.source_model"
              placeholder="输入模型名，或点击右侧▼选择已有模型"
              clearable
              style="width:100%"
            >
              <template #suffix>
                <NPopover v-model:show="showSourcePopover" trigger="click" placement="bottom-end" :width="280" scrollable>
                  <template #trigger>
                    <NButton text size="tiny" style="color:#888;padding:0 2px">
                      <NIcon size="16"><ChevronDownSharp /></NIcon>
                    </NButton>
                  </template>
                  <div style="padding:4px 0">
                    <NInput v-model:value="modelSearch" placeholder="搜索模型…" clearable style="margin-bottom:8px" />
                    <div v-if="filteredModelOptions.length === 0" style="color:#888;padding:8px;text-align:center;font-size:13px">无匹配模型</div>
                    <div
                      v-for="m in filteredModelOptions"
                      :key="m.value"
                      style="padding:6px 10px;cursor:pointer;border-radius:4px;font-size:13px"
                      @click="pickSourceModel(m.value)"
                      @mouseenter="(e: any) => e.target.style.background='rgba(255,255,255,0.06)'"
                      @mouseleave="(e: any) => e.target.style.background='transparent'"
                    >
                      <span style="color:#e2e8f0">{{ m.value }}</span>
                      <span v-if="m.label !== m.value" style="color:#888;margin-left:8px;font-size:12px">{{ m.label.replace(m.value + ' (', '').replace(')', '') }}</span>
                    </div>
                  </div>
                </NPopover>
              </template>
            </NInput>
          </NFormItem>

          <!-- 目标模型名：可选，留空=仅参数注入 -->
          <NFormItem label="目标模型名">
            <div style="display:flex;gap:8px;width:100%;align-items:center">
              <NInput
                v-model:value="mappingForm.target_model"
                placeholder="留空则不映射，仅注入参数"
                clearable
                style="flex:1"
              >
                <template #suffix>
                  <NPopover v-model:show="showTargetPopover" trigger="click" placement="bottom-end" :width="280" scrollable>
                    <template #trigger>
                      <NButton text size="tiny" style="color:#888;padding:0 2px">
                        <NIcon size="16"><ChevronDownSharp /></NIcon>
                      </NButton>
                    </template>
                    <div style="padding:4px 0">
                      <NInput v-model:value="modelSearch" placeholder="搜索模型…" clearable style="margin-bottom:8px" />
                      <div v-if="filteredModelOptions.length === 0" style="color:#888;padding:8px;text-align:center;font-size:13px">无匹配模型</div>
                      <div
                        v-for="m in filteredModelOptions"
                        :key="m.value"
                        style="padding:6px 10px;cursor:pointer;border-radius:4px;font-size:13px"
                        @click="pickTargetModel(m.value)"
                        @mouseenter="(e: any) => e.target.style.background='rgba(255,255,255,0.06)'"
                        @mouseleave="(e: any) => e.target.style.background='transparent'"
                      >
                        <span style="color:#e2e8f0">{{ m.value }}</span>
                        <span v-if="m.label !== m.value" style="color:#888;margin-left:8px;font-size:12px">{{ m.label.replace(m.value + ' (', '').replace(')', '') }}</span>
                      </div>
                    </div>
                  </NPopover>
                </template>
              </NInput>
              <NTag v-if="!mappingForm.target_model" type="info" size="small" bordered>仅参数注入</NTag>
            </div>
          </NFormItem>

          <NFormItem label="显示名称">
            <NInput v-model:value="mappingForm.name" placeholder="可选，留空使用源模型名" />
          </NFormItem>

          <div style="display:flex;gap:12px">
            <NFormItem label="上下文窗口" style="flex:1">
              <NInputNumber v-model:value="mappingForm.context_window" :min="0" :step="1024" style="width:100%" placeholder="0=默认" />
            </NFormItem>
            <NFormItem label="最大输出" style="flex:1">
              <NInputNumber v-model:value="mappingForm.max_output_tokens" :min="0" :step="1024" style="width:100%" placeholder="0=默认" />
            </NFormItem>
          </div>

          <!-- 支持思考 + 推理强度（同一行） -->
          <div style="display:flex;gap:8px;align-items:center">
            <NFormItem label="支持思考" style="flex:0 0 auto;margin-bottom:0">
              <NSwitch v-model:value="mappingForm.thinking" />
            </NFormItem>
            <template v-if="mappingForm.thinking">
              <NFormItem label="推理强度" style="flex:1;margin-bottom:0">
                <NSelect
                  v-model:value="mappingForm.reasoning_effort"
                  :options="[
                    { label: '低 (low)', value: 'low' },
                    { label: '中 (medium)', value: 'medium' },
                    { label: '高 (high)', value: 'high' },
                    { label: '最高 (max)', value: 'max' },
                  ]"
                />
              </NFormItem>
            </template>
          </div>

          <!-- 支持视觉（单独一行） -->
          <NFormItem label="支持视觉" style="margin-bottom:0">
            <NSwitch v-model:value="mappingForm.vision" />
          </NFormItem>
        </NForm>
        <template #footer>
          <NSpace justify="end">
            <NButton @click="showMappingModal = false">取消</NButton>
            <NButton type="primary" @click="saveMapping">保存</NButton>
          </NSpace>
        </template>
      </NModal>

      <!-- 根 CA 证书 -->
      <NCard title="根 CA 证书">
        <NAlert type="info" style="margin-bottom:12px">
          要拦截 HTTPS 流量，需要在客户端设备上安装根 CA 证书为受信任的证书。
        </NAlert>
        <NSpace>
          <NButton type="primary" @click="downloadCert('pem')">下载 .pem（macOS/Linux）</NButton>
          <NButton type="primary" @click="downloadCert('crt')">下载 .crt（Windows）</NButton>
        </NSpace>
      </NCard>

      <NCard title="使用说明">
        <NAlert type="info">
          <p><strong>配置 HTTP 代理：</strong></p>
          <p>代理地址: <code>{{ hostname }}:8520</code></p>
          <br />
          <p><strong>安装根 CA 证书：</strong></p>
          <p><strong>Windows：</strong> 下载 .crt 文件 → 双击 → "安装证书" → "本地计算机" → "受信任的根证书颁发机构"</p>
          <p><strong>macOS：</strong> 下载 .pem 文件 → 终端运行:</p>
          <p><code>sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/Downloads/zero-api-root-ca.pem</code></p>
          <p><strong>Linux：</strong> 下载 .pem 文件 → 终端运行:</p>
          <p><code>sudo cp zero-api-root-ca.pem /usr/local/share/ca-certificates/zero-api.crt && sudo update-ca-certificates</code></p>
          <br />
          <p><strong>支持的拦截域名：</strong></p>
          <p>api.openai.com, api.anthropic.com, openrouter.ai, generativelanguage.googleapis.com</p>
        </NAlert>
      </NCard>
    </NSpace>
  </NSpin>
</template>
