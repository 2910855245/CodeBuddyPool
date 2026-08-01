import { createRouter, createWebHashHistory } from 'vue-router'
import { useAppStore } from '@/stores/app'

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('@/views/RegisterView.vue'),
    meta: { public: true, title: '注册' },
  },
  {
    path: '/',
    name: 'pool',
    component: () => import('@/views/PoolView.vue'),
    meta: { title: '号池管理' },
  },
  {
    path: '/proxy',
    name: 'proxy',
    component: () => import('@/views/ProxyView.vue'),
    meta: { title: '代理监控' },
  },
  {
    path: '/keys',
    name: 'keys',
    component: () => import('@/views/KeysView.vue'),
    meta: { title: 'API 密钥' },
  },
  {
    path: '/skill',
    name: 'skill',
    component: () => import('@/views/SkillView.vue'),
    meta: { title: '部署文档' },
  },
  {
    path: '/help',
    name: 'help',
    component: () => import('@/views/HelpView.vue'),
    meta: { title: '使用说明' },
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

router.beforeEach((to) => {
  const store = useAppStore()
  if (to.meta.public) return true
  if (!store.adminToken) return { path: '/login' }
  return true
})

router.afterEach((to) => {
  const t = to.meta.title as string | undefined
  document.title = t ? `${t} · CodeBuddy 号池面板` : 'CodeBuddy 号池面板'
})

export default router
