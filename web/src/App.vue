<script setup lang="ts">
import { computed } from 'vue'
import { darkTheme, zhCN, dateZhCN } from 'naive-ui'
import { NConfigProvider, NMessageProvider, NDialogProvider } from 'naive-ui'
import Sidebar from '@/components/Sidebar.vue'
import HeaderBar from '@/components/HeaderBar.vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const isLoginPage = computed(() => route.path === '/login')
</script>

<template>
  <NConfigProvider :theme="darkTheme" :locale="zhCN" :date-locale="dateZhCN">
    <NMessageProvider>
      <NDialogProvider>
        <div v-if="isLoginPage" class="login-layout">
          <router-view />
        </div>
        <div v-else class="app-layout">
          <Sidebar />
          <div class="main-area">
            <HeaderBar />
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
</style>
