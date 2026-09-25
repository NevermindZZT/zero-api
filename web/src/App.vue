<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import { darkTheme, zhCN, dateZhCN, type GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider, NMessageProvider, NDialogProvider } from 'naive-ui'
import Sidebar from '@/components/Sidebar.vue'
import HeaderBar from '@/components/HeaderBar.vue'
import { useRoute } from 'vue-router'
import { useTheme } from '@/utils/theme'

const route = useRoute()
const { isDarkTheme } = useTheme()
const isLoginPage = computed(() => route.path === '/login')
const naiveTheme = computed(() => isDarkTheme.value ? darkTheme : null)
const themeOverrides = computed<GlobalThemeOverrides>(() => {
  const dark = isDarkTheme.value
  return {
    common: {
      baseColor: dark ? '#111111' : '#ffffff',
      primaryColor: dark ? '#9b7cff' : '#7542d8',
      primaryColorHover: dark ? '#ae97ff' : '#6633ca',
      primaryColorPressed: dark ? '#835de8' : '#5f2cbb',
      primaryColorSuppl: dark ? '#9b7cff' : '#7542d8',
      infoColor: dark ? '#9b7cff' : '#7542d8',
      infoColorHover: dark ? '#ae97ff' : '#6633ca',
      infoColorPressed: dark ? '#835de8' : '#5f2cbb',
      textColorBase: dark ? '#f5f5f5' : '#18181b',
      textColor1: dark ? '#f0f0f0' : '#18181b',
      textColor2: dark ? '#a3a3a3' : '#52525b',
      textColor3: dark ? '#737373' : '#71717a',
      textColorDisabled: dark ? '#525252' : '#a1a1aa',
      placeholderColor: dark ? '#737373' : '#a1a1aa',
      dividerColor: dark ? '#2c2c2c' : '#e4e4e7',
      borderColor: dark ? '#303030' : '#e4e4e7',
      bodyColor: dark ? '#111111' : '#f5f5f5',
      cardColor: dark ? '#1c1c1c' : '#ffffff',
      popoverColor: dark ? '#202020' : '#ffffff',
      modalColor: dark ? '#202020' : '#ffffff',
      tableColor: dark ? '#1c1c1c' : '#ffffff',
      tableHeaderColor: dark ? '#242424' : '#f4f4f5',
      tableColorHover: dark ? '#242424' : '#f4f4f5',
      tableColorStriped: dark ? '#202020' : '#fafafa',
      hoverColor: dark ? '#252525' : '#f4f4f5',
      inputColor: dark ? '#191919' : '#ffffff',
      inputColorDisabled: dark ? '#222222' : '#f4f4f5',
      codeColor: dark ? '#252525' : '#f4f4f5',
      tabColor: dark ? '#1c1c1c' : '#ffffff',
      actionColor: dark ? '#262626' : '#f4f4f5',
      pressedColor: dark ? '#2d2d2d' : '#e4e4e7',
      buttonColor2: dark ? '#202020' : '#f4f4f5',
      buttonColor2Hover: dark ? '#2b2b2b' : '#e4e4e7',
      buttonColor2Pressed: dark ? '#333333' : '#d4d4d8',
      scrollbarColor: dark ? 'rgba(255,255,255,0.18)' : 'rgba(24,24,27,0.20)',
      scrollbarColorHover: dark ? 'rgba(255,255,255,0.30)' : 'rgba(24,24,27,0.35)',
    },
  }
})

// 登录页需要保留浏览器的账号密码填充；其他 zero-api 页面统一避免被识别为登录表单。
function disableAutofillOnAppForms() {
  if (isLoginPage.value) return

  document.querySelectorAll<HTMLFormElement>('#app form').forEach((form) => {
    form.setAttribute('autocomplete', 'off')
  })
  document.querySelectorAll<HTMLInputElement>('#app input').forEach((input) => {
    input.setAttribute('autocomplete', input.type === 'password' ? 'new-password' : 'off')
  })
}

// 移动端检测
const isMobile = ref(false)
const sidebarOpen = ref(false)
let mq: MediaQueryList | null = null
let autofillObserver: MutationObserver | null = null

function onMqChange(e: MediaQueryListEvent | MediaQueryList) {
  isMobile.value = e.matches
  if (!e.matches) sidebarOpen.value = false
}

onMounted(() => {
  mq = window.matchMedia('(max-width: 767px)')
  mq.addEventListener('change', onMqChange)
  onMqChange(mq)

  disableAutofillOnAppForms()
  autofillObserver = new MutationObserver(disableAutofillOnAppForms)
  autofillObserver.observe(document.getElementById('app')!, { childList: true, subtree: true })

  window.addEventListener('toggle-sidebar', toggleSidebar)
  window.addEventListener('close-mobile-sidebar', closeSidebar)
})

watch(isLoginPage, () => disableAutofillOnAppForms())

onUnmounted(() => {
  mq?.removeEventListener('change', onMqChange)
  autofillObserver?.disconnect()
  window.removeEventListener('toggle-sidebar', toggleSidebar)
  window.removeEventListener('close-mobile-sidebar', closeSidebar)
})

function toggleSidebar() {
  sidebarOpen.value = !sidebarOpen.value
}

function closeSidebar() {
  sidebarOpen.value = false
}
</script>

<template>
  <NConfigProvider :theme="naiveTheme" :theme-overrides="themeOverrides" :locale="zhCN" :date-locale="dateZhCN">
    <NMessageProvider>
      <NDialogProvider>
        <div v-if="isLoginPage" class="login-layout">
          <router-view />
        </div>
        <div v-else class="app-layout" :class="{ 'is-mobile': isMobile }">
          <!-- 移动端遮罩 -->
          <div v-if="isMobile && sidebarOpen" class="sidebar-overlay" @click="closeSidebar"></div>
          <!-- 移动端：渲染 Sidebar 为固定浮层 / 桌面端：正常侧边栏 -->
          <div v-if="isMobile" class="sidebar-mobile-wrapper" :class="{ open: sidebarOpen }">
            <Sidebar :is-mobile="true" />
          </div>
          <Sidebar v-else />
          <div class="main-area">
            <HeaderBar :is-mobile="isMobile" @toggle-mobile-menu="toggleSidebar" />
            <main class="main-content">
              <div class="content-container">
                <router-view v-slot="{ Component }">
                  <transition name="fade-slide" mode="out-in">
                    <component :is="Component" :key="$route.path" />
                  </transition>
                </router-view>
              </div>
            </main>
          </div>
        </div>
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
html, body, #app { height: 100%; }
:root {
  color-scheme: dark;
  --bg-primary: #101010;
  --bg-secondary: #171717;
  --bg-card: #1c1c1c;
  --bg-elevated: #202020;
  --bg-muted: #252525;
  --surface-input: #191919;
  --surface-table-head: rgba(255,255,255,0.04);
  --surface-table-hover: rgba(255,255,255,0.05);
  --surface-table-striped: rgba(255,255,255,0.02);
  --text-primary: #f5f5f5;
  --text-secondary: #a3a3a3;
  --text-muted: #737373;
  --border-color: rgba(255,255,255,0.08);
  --border-strong: rgba(255,255,255,0.13);
  --scrollbar-thumb: rgba(255,255,255,0.18);
  --scrollbar-thumb-hover: rgba(255,255,255,0.32);
  --accent-primary: #9b7cff;
  --accent-hover: #ae97ff;
  --accent-soft: rgba(155,124,255,0.12);
  --ambient-glow: rgba(155,124,255,0.055);
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 20px;
  --shadow-card: 0 8px 24px -8px rgba(0,0,0,0.36), 0 0 0 1px rgba(255,255,255,0.025) inset;
  --shadow-card-hover: 0 12px 32px -8px rgba(0,0,0,0.48), 0 0 0 1px rgba(255,255,255,0.045) inset, 0 0 20px rgba(155,124,255,0.05);
  --gradient-primary: linear-gradient(135deg, #9b7cff, #7658d6);
}
:root[data-theme='light'] {
  color-scheme: light;
  --bg-primary: #f5f5f5;
  --bg-secondary: #ffffff;
  --bg-card: #ffffff;
  --bg-elevated: #ffffff;
  --bg-muted: #f4f4f5;
  --surface-input: #ffffff;
  --surface-table-head: #fafafa;
  --surface-table-hover: #f4f4f5;
  --surface-table-striped: #fafafa;
  --text-primary: #18181b;
  --text-secondary: #52525b;
  --text-muted: #71717a;
  --border-color: rgba(24,24,27,0.10);
  --border-strong: rgba(24,24,27,0.16);
  --scrollbar-thumb: rgba(24,24,27,0.18);
  --scrollbar-thumb-hover: rgba(24,24,27,0.32);
  --accent-primary: #7542d8;
  --accent-hover: #6633ca;
  --accent-soft: rgba(117,66,216,0.09);
  --ambient-glow: rgba(117,66,216,0.035);
  --shadow-card: 0 6px 20px -8px rgba(24,24,27,0.12), 0 0 0 1px rgba(24,24,27,0.025) inset;
  --shadow-card-hover: 0 10px 28px -8px rgba(24,24,27,0.16), 0 0 0 1px rgba(24,24,27,0.04) inset;
  --gradient-primary: linear-gradient(135deg, #7542d8, #5f55c8);
}
body {
  background: var(--bg-primary);
  color: var(--text-primary);
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

/* Subtle ambient background glow */
.main-content::before {
  content: '';
  position: fixed;
  top: -20%;
  right: -10%;
  width: 600px;
  height: 600px;
  border-radius: 50%;
  background: radial-gradient(circle, var(--ambient-glow) 0%, transparent 70%);
  pointer-events: none;
  z-index: 0;
}
.login-layout { height: 100vh; }
.app-layout { display: flex; height: 100vh; }
.main-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.main-content {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--bg-primary);
  display: flex;
  flex-direction: column;
  min-height: 0;
}

/* Custom scrollbar */
.main-content::-webkit-scrollbar {
  width: 6px;
}
.main-content::-webkit-scrollbar-track {
  background: transparent;
}
.main-content::-webkit-scrollbar-thumb {
  background: var(--scrollbar-thumb);
  border-radius: 3px;
}
.main-content::-webkit-scrollbar-thumb:hover {
  background: var(--scrollbar-thumb-hover);
}

/* Firefox：统一所有滚动条为细条 + 深色（scrollbar-color 需要与 ::-webkit 配合，
   保证 Firefox 下不出现白色默认滚动条） */
.main-content,
.n-drawer-body-content-wrapper,
.n-modal-body-content-wrapper,
.n-modal .n-card__content,
.n-modal .n-card__content-wrapper,
.n-modal.n-card .n-card__content,
.n-modal.n-card .n-card__content-wrapper,
.n-card[role="dialog"] .n-card__content,
.n-card[role="dialog"] .n-card__content-wrapper,
.model-edit-modal .n-card__content,
.model-edit-modal .n-card__content-wrapper,
.n-base-select-menu,
.n-data-table-base-table-body {
  scrollbar-width: thin;
  scrollbar-color: var(--scrollbar-thumb) transparent;
}

/* 弹层（Drawer/Modal 等）内容区的原生滚动条统一为深色细条，
   避免出现浏览器默认白色滚动条与暗色主题不一致 */
.n-drawer-body-content-wrapper::-webkit-scrollbar,
.n-modal-body-content-wrapper::-webkit-scrollbar,
.n-modal .n-card__content::-webkit-scrollbar,
.n-modal .n-card__content-wrapper::-webkit-scrollbar,
.n-modal.n-card .n-card__content::-webkit-scrollbar,
.n-modal.n-card .n-card__content-wrapper::-webkit-scrollbar,
.n-card[role="dialog"] .n-card__content::-webkit-scrollbar,
.n-card[role="dialog"] .n-card__content-wrapper::-webkit-scrollbar,
.model-edit-modal .n-card__content::-webkit-scrollbar,
.model-edit-modal .n-card__content-wrapper::-webkit-scrollbar,
.n-base-select-menu::-webkit-scrollbar,
.n-data-table-base-table-body::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}
.n-drawer-body-content-wrapper::-webkit-scrollbar-track,
.n-modal-body-content-wrapper::-webkit-scrollbar-track,
.n-modal .n-card__content::-webkit-scrollbar-track,
.n-modal .n-card__content-wrapper::-webkit-scrollbar-track,
.n-modal.n-card .n-card__content::-webkit-scrollbar-track,
.n-modal.n-card .n-card__content-wrapper::-webkit-scrollbar-track,
.n-card[role="dialog"] .n-card__content::-webkit-scrollbar-track,
.n-card[role="dialog"] .n-card__content-wrapper::-webkit-scrollbar-track,
.model-edit-modal .n-card__content::-webkit-scrollbar-track,
.model-edit-modal .n-card__content-wrapper::-webkit-scrollbar-track,
.n-base-select-menu::-webkit-scrollbar-track,
.n-data-table-base-table-body::-webkit-scrollbar-track {
  background: transparent;
}
.n-drawer-body-content-wrapper::-webkit-scrollbar-thumb,
.n-modal-body-content-wrapper::-webkit-scrollbar-thumb,
.n-modal .n-card__content::-webkit-scrollbar-thumb,
.n-modal .n-card__content-wrapper::-webkit-scrollbar-thumb,
.n-modal.n-card .n-card__content::-webkit-scrollbar-thumb,
.n-modal.n-card .n-card__content-wrapper::-webkit-scrollbar-thumb,
.n-card[role="dialog"] .n-card__content::-webkit-scrollbar-thumb,
.n-card[role="dialog"] .n-card__content-wrapper::-webkit-scrollbar-thumb,
.model-edit-modal .n-card__content::-webkit-scrollbar-thumb,
.model-edit-modal .n-card__content-wrapper::-webkit-scrollbar-thumb,
.n-base-select-menu::-webkit-scrollbar-thumb,
.n-data-table-base-table-body::-webkit-scrollbar-thumb {
  background: var(--scrollbar-thumb);
  border-radius: 3px;
}
.n-drawer-body-content-wrapper::-webkit-scrollbar-thumb:hover,
.n-modal-body-content-wrapper::-webkit-scrollbar-thumb:hover,
.n-modal .n-card__content::-webkit-scrollbar-thumb:hover,
.n-modal .n-card__content-wrapper::-webkit-scrollbar-thumb:hover,
.n-modal.n-card .n-card__content::-webkit-scrollbar-thumb:hover,
.n-modal.n-card .n-card__content-wrapper::-webkit-scrollbar-thumb:hover,
.n-card[role="dialog"] .n-card__content::-webkit-scrollbar-thumb:hover,
.n-card[role="dialog"] .n-card__content-wrapper::-webkit-scrollbar-thumb:hover,
.model-edit-modal .n-card__content::-webkit-scrollbar-thumb:hover,
.model-edit-modal .n-card__content-wrapper::-webkit-scrollbar-thumb:hover,
.n-base-select-menu::-webkit-scrollbar-thumb:hover,
.n-data-table-base-table-body::-webkit-scrollbar-thumb:hover {
  background: var(--scrollbar-thumb-hover);
}

/* NPopover 内自定义滚动容器（如模型映射弹窗里的模型选择列表）统一深色细条：
   这类容器是自定义 div（overflow-y:auto），不在 naive-ui 组件选择器范围内，
   需要通配覆盖；scrollbar-color 是继承属性，作用于 .n-popover 即可 */
.n-popover {
  scrollbar-width: thin;
  scrollbar-color: var(--scrollbar-thumb) transparent;
}
.n-popover *::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}
.n-popover *::-webkit-scrollbar-track {
  background: transparent;
}
.n-popover *::-webkit-scrollbar-thumb {
  background: var(--scrollbar-thumb);
  border-radius: 3px;
}
.n-popover *::-webkit-scrollbar-thumb:hover {
  background: var(--scrollbar-thumb-hover);
}
.content-container {
  width: 100%;
  max-width: 1400px;
  margin: 0 auto;
  padding: 24px 32px;
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

/* Page transition */
.fade-slide-enter-active,
.fade-slide-leave-active {
  transition: all 0.25s ease;
}
.fade-slide-enter-from {
  opacity: 0;
  transform: translateY(12px);
}
.fade-slide-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}

/* Shared page header */
.page-header { margin-bottom: 8px; }
.page-header h2 { margin: 0; font-size: 24px; font-weight: 700; display: flex; align-items: center; }
.page-subtitle { color: var(--text-secondary); font-size: 14px; margin-top: 4px; }

/* Naive UI surface overrides — driven by the active color scheme */
.n-card {
  border-radius: var(--radius-lg) !important;
  box-shadow: var(--shadow-card) !important;
  border: 1px solid var(--border-color) !important;
  background: var(--bg-card) !important;
  backdrop-filter: blur(16px) !important;
  -webkit-backdrop-filter: blur(16px) !important;
  transition: box-shadow 0.25s ease, transform 0.25s ease, border-color 0.25s ease;
}
.n-card:hover {
  box-shadow: var(--shadow-card-hover) !important;
  border-color: var(--border-strong) !important;
  transform: translateY(-1px);
}

.n-layout-sider {
  background: var(--bg-secondary) !important;
  backdrop-filter: blur(16px) !important;
  -webkit-backdrop-filter: blur(16px) !important;
}

.n-data-table {
  border-radius: var(--radius-md);
  background: transparent !important;
  min-width: 100%;
}
.n-data-table-wrapper,
.n-data-table-base-table,
.n-data-table-table,
.n-data-table-thead,
.n-card__content {
  background: transparent !important;
}
.n-data-table-th {
  background: var(--surface-table-head) !important;
  border-bottom: 1px solid var(--border-color) !important;
}
.n-data-table-td {
  background: transparent !important;
  border-bottom: 1px solid var(--border-color) !important;
}
.n-data-table-tr:hover .n-data-table-td {
  background: var(--surface-table-hover) !important;
}
.n-data-table-tr--striped .n-data-table-td {
  background: var(--surface-table-striped) !important;
}
.n-card__header {
  background: transparent !important;
  border-bottom: 1px solid var(--border-color) !important;
  padding: 16px 20px !important;
}

.n-modal {
  background: var(--bg-elevated) !important;
  backdrop-filter: blur(20px) !important;
  -webkit-backdrop-filter: blur(20px) !important;
  border: 1px solid var(--border-color) !important;
}
.n-modal-mask {
  background: rgba(0, 0, 0, 0.5) !important;
  backdrop-filter: blur(4px) !important;
  -webkit-backdrop-filter: blur(4px) !important;
}

.n-button { border-radius: var(--radius-sm) !important; }
.n-tag { border-radius: 6px !important; }
.n-input {
  background: var(--surface-input) !important;
  border-color: var(--border-color) !important;
}
.n-input:hover { border-color: var(--border-strong) !important; }
.n-input--focus { border-color: var(--accent-primary) !important; }
.n-select .n-base-selection {
  background: var(--surface-input) !important;
  border-color: var(--border-color) !important;
}
.n-alert {
  background: var(--surface-table-head) !important;
  backdrop-filter: blur(8px) !important;
  border: 1px solid var(--border-color) !important;
}

/* NSpin 强制 block 布局（默认 inline-block 导致宽度塌陷） */
.n-spin-container {
  display: block !important;
  width: 100% !important;
}

/* NSpace 子元素撑满宽度（解决卡片变窄） */
.n-space--vertical > .n-space-item {
  width: 100% !important;
}

/* 表格容器横向滚动（移动端不换行） */
.n-data-table-wrapper {
  overflow-x: auto !important;
  -webkit-overflow-scrolling: touch;
}
.n-data-table {
  min-width: 100%;
}

/* 卡片在页面布局内撑满宽度 */
.main-area .n-card {
  overflow: hidden;
  align-self: stretch;
}

/* ===== 移动端遮罩 ===== */
.sidebar-overlay {
  position: fixed;
  inset: 0;
  z-index: 99;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
}

/* ===== 移动端侧边栏浮层 ===== */
.sidebar-mobile-wrapper {
  position: fixed;
  left: 0;
  top: 0;
  bottom: 0;
  z-index: 100;
  width: 220px;
  transform: translateX(-100%);
  transition: transform 0.3s ease;
}
.sidebar-mobile-wrapper.open {
  transform: translateX(0);
}
.sidebar-mobile-wrapper .n-layout-sider {
  width: 220px !important;
  height: 100%;
}

/* ===== 响应式布局 ===== */
@media (max-width: 767px) {
  .content-container {
    padding: 16px !important;
  }

  .page-header {
    flex-direction: column !important;
    align-items: flex-start !important;
    gap: 8px;
  }
  .page-header h2 {
    font-size: 20px !important;
  }

  /* 统计卡片一行一个 */
  .n-grid > .n-gi {
    min-width: 100% !important;
  }

  /* 表格在小屏横向滚动 */
  .n-data-table {
    overflow-x: auto;
  }

  /* 图表卡片内 flex 元素垂直排列 */
  .chart-card-body {
    flex-direction: column !important;
    text-align: center;
  }
  .chart-card-body .cache-stat-row {
    justify-content: center;
  }
}

/* 小屏适配（续） */
@media (max-width: 1023px) {
  .content-container {
    padding: 20px !important;
  }
}

/* 移动端内容容器 */
@media (max-width: 767px) {
  .content-container {
    padding: 12px !important;
    max-width: 100% !important;
  }
}

/* 登录页面移动端适配 */
@media (max-width: 480px) {
  .login-card {
    width: 90vw !important;
    padding: 16px !important;
  }
  .login-header h1 {
    font-size: 24px !important;
  }
}

/* 弹窗移动端适配 */
@media (max-width: 600px) {
  .n-modal-content,
  .n-card[role="dialog"] {
    max-width: calc(100vw - 32px) !important;
    margin: 16px !important;
  }
}

/* 表单行移动端竖排 */
@media (max-width: 600px) {
  .n-form .n-form-item {
    flex-direction: column;
  }
  .n-form .n-form-item-label {
    text-align: left;
    padding-bottom: 4px !important;
  }
}
</style>
