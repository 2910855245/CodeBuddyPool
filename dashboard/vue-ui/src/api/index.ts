// API 层：统一请求封装（30s 超时 + Bearer 鉴权）+ 按域分组
import { useAppStore } from '@/stores/app'

const API_BASE = ''

export interface ApiResponse<T = any> {
  success: boolean
  message: string
  data: T
}

function buildQuery(params?: Record<string, any>): string {
  if (!params) return ''
  const q = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
    .join('&')
  return q ? `?${q}` : ''
}

async function request<T = any>(method: string, path: string, body?: any): Promise<ApiResponse<T>> {
  const store = useAppStore()
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (store.adminToken) headers['Authorization'] = `Bearer ${store.adminToken}`

  const opts: RequestInit = { method, headers }
  if (body !== undefined && method !== 'GET') opts.body = JSON.stringify(body)

  const controller = new AbortController()
  opts.signal = controller.signal
  const timer = setTimeout(() => controller.abort(), 30000)

  try {
    const res = await fetch(API_BASE + path, opts)
    const data = (await res.json().catch(() => null)) as ApiResponse<T> | null
    if (res.status === 401) {
      // 令牌失效：清空并跳登录
      store.setAdminToken('')
      if (location.hash !== '#/login') {
        store.toast('登录已过期，请重新登录', 'warning')
        location.hash = '#/login'
      }
      throw new Error(toMsg((data as any)?.message ?? (data as any)?.detail, '未授权'))
    }
    if (!res.ok) {
      throw new Error(toMsg((data as any)?.message ?? (data as any)?.detail, `请求失败 (${res.status})`))
    }
    if (data === null) throw new Error('响应格式错误')
    return data
  } finally {
    clearTimeout(timer)
  }
}

// 把后端返回的 message/detail 统一转成可读字符串（FastAPI 的 detail 可能是数组/对象）
function toMsg(v: any, fallback: string): string {
  if (typeof v === 'string' && v) return v
  if (Array.isArray(v) && v.length) {
    const parts = v.map((x: any) => {
      const loc = Array.isArray(x?.loc) ? x.loc.filter((s: any) => s !== 'body').join('.') : ''
      const msg = x?.msg || x?.message || JSON.stringify(x)
      return loc ? `${loc}: ${msg}` : msg
    })
    return parts.join('；')
  }
  if (v && typeof v === 'object') return v.msg || v.message || JSON.stringify(v)
  return fallback
}

const get = <T = any>(path: string) => request<T>('GET', path)
const post = <T = any>(path: string, body?: any) => request<T>('POST', path, body)
const put = <T = any>(path: string, body?: any) => request<T>('PUT', path, body)
const del = <T = any>(path: string) => request<T>('DELETE', path)

export const api = {
  // 管理员认证
  auth: {
    login: (username: string, password: string) =>
      post<{ token: string; username: string }>('/api/admin/login', { username, password }),
    verify: () => post<{ username: string }>('/api/admin/verify'),
    logout: () => post('/api/admin/logout'),
    changePassword: (oldPassword: string, newPassword: string) =>
      post('/api/admin/change-password', { old_password: oldPassword, new_password: newPassword }),
  },

  // 号池管理
  pool: {
    status: () => get('/api/pool'),
    checkinAll: () => post('/api/checkin'),
    checkinOne: (phone: string) => post(`/api/checkin/${encodeURIComponent(phone)}`),
    refreshOne: (phone: string) => post(`/api/refresh/${encodeURIComponent(phone)}`),
    refreshAll: () => post('/api/refresh-all'),
    register: (count: number, interval: number) =>
      post(`/api/register${buildQuery({ count, interval })}`),
    remove: (phone: string) => del(`/api/account/${encodeURIComponent(phone)}`),
  },

  // 自动化设置
  settings: {
    get: () => get('/api/settings'),
    update: (data: Record<string, any>) => post('/api/settings', data),
  },

  // 代理监控
  proxy: {
    status: () => get('/api/proxy-status'),
    accounts: () => get('/api/proxy/accounts'),
    logs: (n = 100) => get(`/api/proxy/logs${buildQuery({ n })}`),
    checkin: () => post('/api/proxy/checkin'),
    reload: () => post('/api/proxy/reload'),
  },

  // API 密钥管理（发给别人的 Key，增删/启停/用量）
  keys: {
    list: () => get('/api/keys'),
    create: (name: string) => post('/api/keys', { name }),
    toggle: (key: string) => put(`/api/keys/${encodeURIComponent(key)}/toggle`),
    remove: (key: string) => del(`/api/keys/${encodeURIComponent(key)}`),
    usage: (key: string) => get(`/api/keys/${encodeURIComponent(key)}/usage`),
  },

  // 分享注册（公开，无鉴权）
  registerPage: {
    start: (phone: string) => post('/api/register-page/start', { phone }),
    submit: (phone: string, code: string) => post('/api/register-page/submit', { phone, code }),
    check: (phone: string) => post('/api/register-page/check', { phone }),
  },

  // 部署文档
  skill: {
    deployDoc: () => get('/api/skill/deploy-doc'),
  },
}
