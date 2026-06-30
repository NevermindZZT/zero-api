import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/Login.vue'),
    },
    { path: '/', redirect: '/dashboard' },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: () => import('@/views/Dashboard.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/channels',
      name: 'channels',
      component: () => import('@/views/Channels.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/models',
      name: 'models',
      component: () => import('@/views/Models.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/proxy',
      name: 'proxy',
      component: () => import('@/views/ProxySettings.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/usage',
      name: 'usage',
      component: () => import('@/views/Usage.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/api-keys',
      name: 'api-keys',
      component: () => import('@/views/APIKeys.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/chat',
      name: 'chat',
      component: () => import('@/views/ChatTest.vue'),
      meta: { requiresAuth: true },
    },
  ],
})

router.beforeEach((to, _from, next) => {
  const token = localStorage.getItem('token')
  if (to.meta.requiresAuth && !token) {
    next('/login')
  } else if (to.path === '/login' && token) {
    next('/dashboard')
  } else {
    next()
  }
})

export default router
