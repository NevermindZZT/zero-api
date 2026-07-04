<script setup lang="ts">
import { h, ref, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NMenu, NLayoutSider, NIcon, NButton } from 'naive-ui'
import {
  BarChartSharp,
  ChatbubbleEllipsesSharp,
  GlobeSharp,
  HardwareChipSharp,
  ShieldCheckmarkSharp,
  KeySharp,
  TrendingUpSharp,
  FlashSharp,
  RocketSharp,
  ServerSharp,
  SettingsSharp,
  CloseSharp,
} from '@vicons/ionicons5'

const props = defineProps<{
  isMobile?: boolean
}>()

const route = useRoute()
const router = useRouter()
const collapsed = ref(false)

const renderIcon = (icon: any) => () => h(NIcon, { size: 18 }, { default: () => h(icon) })

const menuOptions = [
  {
    type: 'group' as const,
    label: '概览',
    key: 'group-overview',
    children: [
      { label: '仪表盘', key: '/dashboard', icon: renderIcon(BarChartSharp) },
      { label: '使用统计', key: '/usage', icon: renderIcon(TrendingUpSharp) },
    ],
  },
  {
    type: 'group' as const,
    label: '管理',
    key: 'group-management',
    children: [
      { label: '渠道管理', key: '/channels', icon: renderIcon(GlobeSharp) },
      { label: '模型管理', key: '/models', icon: renderIcon(HardwareChipSharp) },
      { label: 'API 密钥', key: '/api-keys', icon: renderIcon(KeySharp) },
      { label: 'Chat 测试', key: '/chat', icon: renderIcon(ChatbubbleEllipsesSharp) },
    ],
  },
  {
    type: 'group' as const,
    label: '设置',
    key: 'group-settings',
    children: [
      { label: '代理设置', key: '/proxy', icon: renderIcon(ShieldCheckmarkSharp) },
      { label: '出站代理', key: '/forward-proxy', icon: renderIcon(RocketSharp) },
      { label: '数据库管理', key: '/database', icon: renderIcon(ServerSharp) },
      { label: '系统设置', key: '/settings', icon: renderIcon(SettingsSharp) },
    ],
  },
]

const appVersion = 'v1.0.9'
const projectUrl = 'https://github.com/NevermindZZT/zero-api'

const activeKey = ref(route.path)
watch(() => route.path, (p) => { activeKey.value = p })

function handleUpdate(key: string) {
  router.push(key)
  // 移动端点击菜单后关闭侧边栏
  if (props.isMobile) {
    window.dispatchEvent(new CustomEvent('close-mobile-sidebar'))
  }
}

function closeMobile() {
  window.dispatchEvent(new CustomEvent('close-mobile-sidebar'))
}
</script>

<template>
  <NLayoutSider
    bordered
    :collapsed="collapsed"
    :collapsed-width="64"
    :width="220"
    collapse-mode="width"
    :show-trigger="isMobile ? false : 'bar'"
    @collapse="collapsed = true"
    @expand="collapsed = false"
    :native-scrollbar="false"
    style="background: var(--bg-secondary); position: relative;"
  >    <!-- 移动端关闭按钮 -->
    <div v-if="isMobile" class="sidebar-close">
      <NButton quaternary size="small" @click="closeMobile">
        <template #icon><NIcon size="18"><CloseSharp /></NIcon></template>
      </NButton>
    </div>    <div class="sidebar-logo">
      <div class="logo-icon-wrapper">
        <NIcon size="22" color="#fff"><FlashSharp /></NIcon>
      </div>
      <span v-show="!collapsed" class="logo-text">zero-api</span>
    </div>

    <NMenu
      :value="activeKey"
      :options="menuOptions"
      :collapsed="collapsed"
      :collapsed-width="64"
      @update:value="handleUpdate"
      style="margin-top: 8px;"
    />

    <div v-show="!collapsed" class="sidebar-footer">
      <a :href="projectUrl" target="_blank" class="project-link">
        <NIcon size="14" style="vertical-align:-2px">
          <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14">
            <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
          </svg>
        </NIcon>
        <span class="project-link-text">GitHub</span>
      </a>
      <span class="version-text">{{ appVersion }}</span>
    </div>
  </NLayoutSider>
</template>

<style scoped>
.sidebar-logo {
  padding: 20px 16px;
  display: flex;
  align-items: center;
  gap: 10px;
  border-bottom: 1px solid var(--border-color);
}
.logo-icon-wrapper {
  width: 34px;
  height: 34px;
  border-radius: 10px;
  background: linear-gradient(135deg, #667eea, #764ba2);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.sidebar-footer {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-top: 1px solid var(--border-color);
  font-size: 12px;
}
.project-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: #64748b;
  text-decoration: none;
  transition: color 0.2s;
}
.project-link:hover {
  color: #667eea;
}
.project-link-text {
  font-size: 12px;
}
.version-text {
  color: #475569;
  font-size: 11px;
}
.logo-text {
  font-size: 18px;
  font-weight: 700;
  background: linear-gradient(135deg, #667eea, #764ba2);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
}

/* 侧边栏收起时图标居中 - Naive UI NMenu 使用 grid 布局
   (grid-template-columns: auto 1fr auto = icon + content + arrow)，
   收起时 content 和 arrow 仅为 opacity:0 但仍占网格空间，
   同时非顶层菜单项有 inline padding-left 缩进导致图标偏移。 */
:deep(.n-menu--collapsed .n-menu-item-content) {
  padding-left: 0 !important;
  padding-right: 0 !important;
  grid-template-columns: 1fr !important;
  grid-template-areas: "icon" !important;
}
:deep(.n-menu--collapsed .n-menu-item-content .n-menu-item-content-header),
:deep(.n-menu--collapsed .n-menu-item-content .n-menu-item-content__arrow) {
  display: none;
}
:deep(.n-menu--collapsed .n-menu-item-content .n-menu-item-content__icon) {
  margin-right: 0 !important;
  justify-self: center;
}

/* 移动端关闭按钮 */
.sidebar-close {
  display: flex;
  justify-content: flex-end;
  padding: 12px 16px 0;
}
</style>
