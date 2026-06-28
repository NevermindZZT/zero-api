<script setup lang="ts">
import { h, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NBreadcrumb, NBreadcrumbItem, NIcon, NDropdown } from 'naive-ui'
import {
  MenuSharp,
  PersonCircleSharp,
  LogOutSharp,
} from '@vicons/ionicons5'

const route = useRoute()
const router = useRouter()

const username = localStorage.getItem('user') || '管理员'

const breadcrumbMap: Record<string, string> = {
  '/dashboard': '仪表盘',
  '/channels': '渠道管理',
  '/models': '模型管理',
  '/proxy': '代理设置',
  '/api-keys': 'API 密钥',
  '/usage': '使用统计',
}

const currentTitle = computed(() => breadcrumbMap[route.path] || '')

const userMenuOptions = [
  {
    label: '管理员',
    key: 'profile',
    disabled: true,
  },
  {
    type: 'divider' as const,
    key: 'd1',
  },
  {
    label: '退出登录',
    key: 'logout',
    icon: () => h(NIcon, { size: 16 }, { default: () => h(LogOutSharp) }),
  },
]

function handleUserMenuAction(key: string) {
  if (key === 'logout') {
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    router.push('/login')
  }
}

function toggleSidebar() {
  // Emit event or use a store to toggle sidebar
  window.dispatchEvent(new CustomEvent('toggle-sidebar'))
}
</script>

<template>
  <header class="header-bar">
    <div class="header-left">
      <NButton quaternary size="small" class="menu-toggle" @click="toggleSidebar">
        <template #icon>
          <NIcon size="20"><MenuSharp /></NIcon>
        </template>
      </NButton>
      <NBreadcrumb>
        <NBreadcrumbItem>
          <NIcon size="14" style="vertical-align:-2px;margin-right:4px">
            <svg viewBox="0 0 512 512" width="14" height="14" fill="currentColor">
              <path d="M256 48L48 192v272h160V320h96v144h160V192z"/>
            </svg>
          </NIcon>
          zero-api
        </NBreadcrumbItem>
        <NBreadcrumbItem v-if="currentTitle">{{ currentTitle }}</NBreadcrumbItem>
      </NBreadcrumb>
    </div>

    <div class="header-right">
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
</style><｜end▁of▁thinking｜>

<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="manage_todo_list">
<｜｜DSML｜｜parameter name="todoList" string="false">[{"id":1,"title":"替换 emoji 为 ionicons 图标","status":"completed"},{"id":2,"title":"添加页面过渡动画","status":"completed"},{"id":3,"title":"优化统计卡片样式","status":"in-progress"},{"id":4,"title":"添加顶部 Header 栏","status":"completed"},{"id":5,"title":"Sidebar 菜单分组优化","status":"not-started"},{"id":6,"title":"表格样式优化","status":"not-started"},{"id":7,"title":"全局布局与细节打磨","status":"not-started"}]