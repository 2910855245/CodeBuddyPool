import { defineStore } from 'pinia'
import { reactive, ref } from 'vue'

const ADMIN_TOKEN_KEY = 'admin_token'

export const useAppStore = defineStore('app', () => {
  // 管理员令牌（持久化到 localStorage）
  const adminToken = ref<string>(localStorage.getItem(ADMIN_TOKEN_KEY) || '')
  const adminUsername = ref<string>('')

  function setAdminToken(token: string, username = '') {
    adminToken.value = token
    adminUsername.value = username
    if (token) localStorage.setItem(ADMIN_TOKEN_KEY, token)
    else localStorage.removeItem(ADMIN_TOKEN_KEY)
  }

  // Toast 通知
  const toasts = reactive<Array<{ id: number; message: string; type: string }>>([])

  function toast(message: string, type: 'success' | 'error' | 'warning' | 'info' = 'success') {
    const id = Date.now() + Math.random()
    toasts.push({ id, message, type })
    setTimeout(() => {
      const idx = toasts.findIndex(t => t.id === id)
      if (idx > -1) toasts.splice(idx, 1)
    }, 3500)
  }

  return { adminToken, adminUsername, setAdminToken, toasts, toast }
})
