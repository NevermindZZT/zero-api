<script setup lang="ts">
import { h, computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NBreadcrumb, NBreadcrumbItem, NIcon, NDropdown, useMessage } from 'naive-ui'
import { auth, authState, authErrorMessage } from '@/auth'
import {
  HomeSharp,
  PersonCircleSharp,
  LogOutSharp,
  DesktopSharp,
  MoonSharp,
  SunnySharp,
} from '@vicons/ionicons5'
import { useTheme, type ThemePreference } from '@/utils/theme'

defineProps<{
  isMobile?: boolean
}>()

const emit = defineEmits<{
  'toggle-mobile-menu': []
}>()

const route = useRoute()
const router = useRouter()
const { isDarkTheme, setThemePreference, themePreference } = useTheme()

const username = computed(() => authState.user || '管理员')
const message = useMessage()
const loggingOut = ref(false)

const breadcrumbMap: Record<string, string> = {
  '/dashboard': '仪表盘',
  '/channels': '渠道管理',
  '/models': '模型管理',
  '/proxy': '代理设置',
  '/forward-proxy': '出站代理',
  '/api-keys': 'API 密钥',
  '/usage': '使用统计',
  '/database': '数据库管理',
}

const currentTitle = computed(() => breadcrumbMap[route.path] || '')
const themeIcon = computed(() => themePreference.value === 'system'
  ? DesktopSharp
  : isDarkTheme.value ? MoonSharp : SunnySharp)
const themeButtonLabel = computed(() => {
  if (themePreference.value === 'system') return '主题：跟随系统'
  return isDarkTheme.value ? '主题：深色' : '主题：浅色'
})
const themeMenuOptions = computed(() => {
  const options: { label: string; key: ThemePreference; icon: () => ReturnType<typeof h> }[] = [
    { label: '跟随系统', key: 'system', icon: () => h(NIcon, { size: 16 }, { default: () => h(DesktopSharp) }) },
    { label: '浅色模式', key: 'light', icon: () => h(NIcon, { size: 16 }, { default: () => h(SunnySharp) }) },
    { label: '深色模式', key: 'dark', icon: () => h(NIcon, { size: 16 }, { default: () => h(MoonSharp) }) },
  ]
  return options.map((option) => ({
    ...option,
    label: option.key === themePreference.value ? `${option.label} ✓` : option.label,
  }))
})

function handleThemeSelect(key: string | number) {
  if (key === 'system' || key === 'light' || key === 'dark') {
    setThemePreference(key)
  }
}

const userMenuOptions = computed(() => [
  { label: username.value, key: 'profile', disabled: true },
  ...(authState.authEnabled ? [
    { type: 'divider' as const, key: 'd1' },
    {
      label: '退出登录', key: 'logout', disabled: loggingOut.value,
      icon: () => h(NIcon, { size: 16 }, { default: () => h(LogOutSharp) }),
    },
  ] : []),
])

async function handleUserMenuAction(key: string) {
  if (key !== 'logout' || loggingOut.value || !authState.authEnabled) return
  loggingOut.value = true
  try {
    await auth.logout()
    message.success('已退出登录')
    await router.replace('/login')
  } catch (error: any) {
    const detail = error.response?.status === 401 ? '服务器未确认撤销会话，请重试' : authErrorMessage(error, '退出登录')
    message.error(`${detail}；未确认退出成功`)
  } finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <header class="header-bar">
    <div class="header-left">
      <NButton quaternary size="small" class="home-btn" @click="isMobile ? emit('toggle-mobile-menu') : router.push('/dashboard')">
        <template #icon>
          <NIcon size="18"><HomeSharp /></NIcon>
        </template>
        <span class="home-text">zero-api</span>
      </NButton>
      <NBreadcrumb v-if="currentTitle" style="margin-left:8px">
        <NBreadcrumbItem>{{ currentTitle }}</NBreadcrumbItem>
      </NBreadcrumb>
    </div>

    <div class="header-right">
      <NDropdown :options="themeMenuOptions" @select="handleThemeSelect">
        <NButton quaternary size="small" :title="themeButtonLabel" :aria-label="themeButtonLabel">
          <template #icon>
            <NIcon size="18"><component :is="themeIcon" /></NIcon>
          </template>
        </NButton>
      </NDropdown>
      <NDropdown :options="userMenuOptions" @select="handleUserMenuAction">
        <NButton quaternary size="small" class="user-btn">
          <template #icon>
            <NIcon size="20"><PersonCircleSharp /></NIcon>
          </template>
          <span class="user-name">{{ username }}</span>
        </NButton>
      </NDropdown>
    </div>
  </header>
</template>

<style scoped>
.header-bar {
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  background: var(--bg-secondary);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  border-bottom: 1px solid var(--border-color);
  flex-shrink: 0;
  gap: 12px;
  position: relative;
  z-index: 10;
}
.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.home-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 700;
}
.home-text {
  font-size: 15px;
  font-weight: 700;
  background: var(--gradient-primary);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.menu-toggle {
  display: none;
}
.user-btn {
  display: flex;
  align-items: center;
  gap: 4px;
}
.user-name {
  font-size: 13px;
  color: var(--text-primary);
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>