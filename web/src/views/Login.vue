<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NInput, NCard, NForm, NFormItem, useMessage } from 'naive-ui'
import { auth, authState, authErrorMessage, safeAuthRedirect } from '@/auth'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const username = ref('')
const password = ref('')
const loading = ref(false)
const retrying = ref(false)

async function retrySession() {
  if (retrying.value) return
  retrying.value = true
  try {
    if (await auth.ensureSession(true)) {
      await router.replace(safeAuthRedirect(route.query.redirect))
    } else {
      message.info('当前没有可用登录会话，请登录')
    }
  } catch (error) {
    message.error(authErrorMessage(error))
  } finally {
    retrying.value = false
  }
}

async function login() {
  if (loading.value) return
  if (!username.value || !password.value) {
    message.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(username.value, password.value)
    password.value = ''
    message.success('登录成功')
    await router.replace(safeAuthRedirect(route.query.redirect))
  } catch (error: any) {
    message.error(error.response?.status === 401 ? '用户名或密码错误' : authErrorMessage(error, '登录'))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrapper">
    <div class="login-bg"></div>
    <NCard class="login-card" :bordered="false">
      <div class="login-header">
        <div class="login-icon-wrapper">
          <img src="/logo.svg" alt="zero-api" class="login-logo" />
        </div>
        <h1><span class="logo-zero">zero</span><span class="logo-api">-api</span></h1>
        <p class="subtitle">大模型 API 中转站</p>
      </div>
      <NAlert v-if="authState.error" type="warning" title="暂时无法确认登录状态" style="margin-bottom: 20px">
        {{ authState.error }}。现有会话未被判定失效。
        <NButton size="small" :loading="retrying" @click="retrySession">重试会话验证</NButton>
      </NAlert>
      <NForm @submit.prevent="login">
        <NFormItem label="用户名">
          <NInput
            v-model:value="username"
            placeholder="请输入用户名"
            size="large"
            @keyup.enter="login"
          />
        </NFormItem>
        <NFormItem label="密码">
          <NInput
            v-model:value="password"
            type="password"
            show-password-on="click"
            placeholder="请输入密码"
            size="large"
            @keyup.enter="login"
          />
        </NFormItem>
        <NButton
          type="primary"
          block
          size="large"
          :loading="loading"
          @click="login"
        >
          登录
        </NButton>
      </NForm>
    </NCard>
  </div>
</template>

<style scoped>
.login-wrapper {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  overflow: hidden;
  background: var(--bg-primary);
}
.login-bg {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse at 20% 50%, var(--accent-soft) 0%, transparent 50%),
    radial-gradient(ellipse at 80% 50%, var(--ambient-glow) 0%, transparent 50%);
  pointer-events: none;
}
.login-card {
  width: 400px;
  padding: 24px;
  border-radius: 24px !important;
  background: var(--bg-card) !important;
  backdrop-filter: blur(20px);
  border: 1px solid var(--border-color) !important;
  box-shadow: var(--shadow-card-hover) !important;
  position: relative;
  z-index: 1;
}
.login-header {
  text-align: center;
  margin-bottom: 32px;
}
.login-icon-wrapper {
  width: 76px;
  height: 76px;
  border-radius: 22px;
  background: var(--accent-soft);
  box-shadow: 0 8px 28px var(--ambient-glow);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 16px;
}
.login-logo { width: 62px; height: 62px; }
.login-header h1 {
  font-size: 28px;
  font-weight: 700;
  margin: 0 0 8px 0;
}
.logo-zero { color: var(--text-primary); }
.logo-api { color: var(--accent-primary); }
.subtitle {
  color: var(--text-secondary);
  font-size: 14px;
  margin: 0;
}
:deep(.n-card__content) { padding: 0 !important; }
</style>
