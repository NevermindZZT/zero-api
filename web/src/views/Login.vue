<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NCard, NForm, NFormItem, useMessage } from 'naive-ui'
import api from '@/api'

const router = useRouter()
const message = useMessage()
const username = ref('')
const password = ref('')
const loading = ref(false)

async function login() {
  if (!username.value || !password.value) {
    message.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    const res = await api.post('/auth/login', {
      username: username.value,
      password: password.value,
    })
    localStorage.setItem('token', res.data.token)
    localStorage.setItem('user', res.data.user)
    message.success('登录成功')
    router.push('/dashboard')
  } catch (e: any) {
    message.error(e.response?.data?.error || '登录失败')
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
  background: #0f172a;
}
.login-bg {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse at 20% 50%, rgba(102, 126, 234, 0.15) 0%, transparent 50%),
    radial-gradient(ellipse at 80% 50%, rgba(118, 75, 162, 0.15) 0%, transparent 50%);
  pointer-events: none;
}
.login-card {
  width: 400px;
  padding: 24px;
  border-radius: 24px !important;
  background: rgba(30, 30, 46, 0.95) !important;
  backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.06) !important;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5) !important;
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
  background: #111a4f;
  box-shadow: 0 8px 28px rgba(71, 117, 255, .32);
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
.logo-zero { color: #e2e8f0; }
.logo-api { color: #6d8cff; }
.subtitle {
  color: #94a3b8;
  font-size: 14px;
  margin: 0;
}
:deep(.n-card__content) { padding: 0 !important; }
</style>
