<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api'
import { useAppStore } from '@/stores/app'

const router = useRouter()
const store = useAppStore()

const username = ref('')
const password = ref('')
const loading = ref(false)

async function submit() {
  if (!username.value || !password.value) {
    store.toast('请输入账号和密码', 'warning')
    return
  }
  loading.value = true
  try {
    const res = await api.auth.login(username.value, password.value)
    if (res.success && res.data?.token) {
      store.setAdminToken(res.data.token, res.data.username || username.value)
      store.toast('登录成功', 'success')
      router.push('/')
    } else {
      store.toast(res.message || '登录失败', 'error')
    }
  } catch (e: any) {
    store.toast(e.message || '登录失败，请稍后重试', 'error')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-logo">
        <svg class="logo-svg" viewBox="0 0 52 52" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
          <defs>
            <linearGradient id="logoGrad" x1="0" y1="0" x2="52" y2="52" gradientUnits="userSpaceOnUse">
              <stop stop-color="#4f6ef7" />
              <stop offset="1" stop-color="#7c5cf0" />
            </linearGradient>
          </defs>
          <rect width="52" height="52" rx="15" fill="url(#logoGrad)" />
          <path d="M15 20.5 22 26l-7 5.5" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />
          <path d="M26 32h11" stroke="#fff" stroke-width="3" stroke-linecap="round" />
        </svg>
      </div>
      <h1 class="login-title">CodeBuddy 号池面板</h1>
      <p class="login-sub">管理员登录</p>

      <form @submit.prevent="submit">
        <div class="field">
          <label>账号</label>
          <input v-model.trim="username" type="text" placeholder="管理员账号" autocomplete="username" />
        </div>
        <div class="field">
          <label>密码</label>
          <input v-model="password" type="password" placeholder="密码" autocomplete="current-password" />
        </div>

        <button class="btn btn-primary btn-lg btn-block" type="submit" :disabled="loading">
          <span v-if="loading" class="spinner"></span>
          <span>{{ loading ? '登录中...' : '登 录' }}</span>
        </button>
      </form>

      <div class="login-foot">
        <RouterLink to="/register">前往分享注册页</RouterLink>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  width: 100%;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background:
    radial-gradient(600px 400px at 20% 10%, rgba(79, 110, 247, .12), transparent 70%),
    radial-gradient(500px 380px at 85% 85%, rgba(124, 92, 240, .1), transparent 70%),
    var(--c-bg);
}

.login-card {
  width: 100%;
  max-width: 400px;
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-xl);
  padding: 38px 32px 26px;
  box-shadow: var(--shadow-md);
  animation: fadeInUp .4s var(--ease);
}

.login-logo { display: flex; justify-content: center; margin-bottom: 14px; }
.logo-svg {
  width: 52px; height: 52px; display: block;
  border-radius: 15px;
  box-shadow: 0 8px 24px rgba(79, 110, 247, .4);
  animation: scaleIn .45s var(--ease);
}

.login-title { text-align: center; font-size: 21px; font-weight: 600; letter-spacing: -.02em; margin: 0 0 4px; }
.login-sub { text-align: center; color: var(--c-text-muted); margin: 0 0 26px; font-size: 13.5px; }

.login-foot { text-align: center; margin-top: 20px; font-size: 13px; }
</style>
