<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { darkTheme, zhCN, dateZhCN } from 'naive-ui'
import { NConfigProvider, NMessageProvider, NDialogProvider } from 'naive-ui'
import Sidebar from '@/components/Sidebar.vue'
import HeaderBar from '@/components/HeaderBar.vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const isLoginPage = computed(() => route.path === '/login')

// 移动端检测
const isMobile = ref(false)
const sidebarOpen = ref(false)
let mq: MediaQueryList | null = null

function onMqChange(e: MediaQueryListEvent | MediaQueryList) {
  isMobile.value = e.matches
  if (!e.matches) sidebarOpen.value = false
}

onMounted(() => {
  mq = window.matchMedia('(max-width: 767px)')
  mq.addEventListener('change', onMqChange)
  onMqChange(mq)

  window.addEventListener('toggle-sidebar', toggleSidebar)
  window.addEventListener('close-mobile-sidebar', closeSidebar)
})

onUnmounted(() => {
  mq?.removeEventListener('change', onMqChange)
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
  <NConfigProvider :theme="darkTheme" :locale="zhCN" :date-locale="dateZhCN">
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
  --bg-primary: #0f172a;
  --bg-secondary: rgba(30, 41, 59, 0.85);
  --bg-card: rgba(30, 41, 59, 0.55);
  --text-primary: #f1f5f9;
  --text-secondary: #94a3b8;
  --border-color: rgba(255,255,255,0.08);
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 20px;
  --shadow-card: 0 8px 24px -6px rgba(0,0,0,0.5), 0 0 0 1px rgba(255,255,255,0.04) inset;
  --shadow-card-hover: 0 12px 36px -8px rgba(0,0,0,0.6), 0 0 0 1px rgba(255,255,255,0.06) inset, 0 0 20px rgba(102,126,234,0.08);
  --glow-primary: 0 0 24px rgba(102,126,234,0.12);
  --gradient-primary: linear-gradient(135deg, #667eea, #764ba2);
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
  background: radial-gradient(circle, rgba(102,126,234,0.04) 0%, transparent 70%);
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
  background: rgba(148, 163, 184, 0.2);
  border-radius: 3px;
}
.main-content::-webkit-scrollbar-thumb:hover {
  background: rgba(148, 163, 184, 0.4);
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
.page-subtitle { color: #94a3b8; font-size: 14px; margin-top: 4px; }

/* Naive UI overrides — glassmorphism cards */
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
  border-color: rgba(255,255,255,0.10) !important;
  transform: translateY(-1px);
}

/* Sidebar glass effect */
.n-layout-sider {
  background: var(--bg-secondary) !important;
  backdrop-filter: blur(16px) !important;
  -webkit-backdrop-filter: blur(16px) !important;
}

/* ===== DataTable glass overrides ===== */
/* Outer container */
.n-data-table {
  border-radius: var(--radius-md);
  background: transparent !important;
}
/* Scroll wrapper — removes solid white/gray default */
.n-data-table-wrapper {
  background: transparent !important;
}
/* Inner base table wrapper */
.n-data-table-base-table {
  background: transparent !important;
}
/* The actual <table> element */
.n-data-table-table {
  background: transparent !important;
}
/* Table head wrapper — Naive UI sets solid bg on thead */
.n-data-table-thead {
  background: transparent !important;
}
/* Header cells — remove solid dark bg, subtle separator */
.n-data-table-th {
  background: rgba(255, 255, 255, 0.03) !important;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06) !important;
}
/* Data cells — fully transparent so glass card shows through */
.n-data-table-td {
  background: transparent !important;
  border-bottom: 1px solid rgba(255, 255, 255, 0.03) !important;
}
/* Row hover — subtle highlight */
.n-data-table-tr:hover .n-data-table-td {
  background: rgba(255, 255, 255, 0.04) !important;
}
/* Striped rows — very subtle tint */
.n-data-table-tr--striped .n-data-table-td {
  background: rgba(255, 255, 255, 0.015) !important;
}

/* ===== Card inner content ===== */
.n-card__content {
  background: transparent !important;
}
/* Card header with its own glass layer */
.n-card__header {
  background: transparent !important;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06) !important;
  padding: 16px 20px !important;
}

/* Modal glass */
.n-modal {
  background: rgba(30, 41, 59, 0.92) !important;
  backdrop-filter: blur(20px) !important;
  -webkit-backdrop-filter: blur(20px) !important;
  border: 1px solid var(--border-color) !important;
}

/* Dialog overlay */
.n-modal-mask {
  background: rgba(0, 0, 0, 0.5) !important;
  backdrop-filter: blur(4px) !important;
  -webkit-backdrop-filter: blur(4px) !important;
}

/* Buttons & tags */
.n-button { border-radius: var(--radius-sm) !important; }
.n-tag { border-radius: 6px !important; }

/* Input fields glass */
.n-input {
  background: rgba(255, 255, 255, 0.04) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
}
.n-input:hover {
  border-color: rgba(255, 255, 255, 0.16) !important;
}
.n-input--focus {
  border-color: #667eea !important;
}

/* Select fields */
.n-select .n-base-selection {
  background: rgba(255, 255, 255, 0.04) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
}

/* Alert glass */
.n-alert {
  background: rgba(255, 255, 255, 0.03) !important;
  backdrop-filter: blur(8px) !important;
  border: 1px solid rgba(255, 255, 255, 0.06) !important;
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
