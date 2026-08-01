<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api } from '@/api'

const route = useRoute()
const router = useRouter()
const store = useAppStore()

const isPublic = computed(() => Boolean(route.meta.public))

const navItems = [
  { path: '/', label: '号池', icon: 'pool' },
  { path: '/proxy', label: '代理', icon: 'proxy' },
  { path: '/keys', label: 'API 密钥', icon: 'key' },
  { path: '/skill', label: '部署文档', icon: 'doc' },
  { path: '/help', label: '使用说明', icon: 'help' },
]

// 修改密码弹窗
const showPwd = ref(false)
const oldPwd = ref('')
const newPwd = ref('')
const confirmPwd = ref('')
const pwdLoading = ref(false)

function openPwd() {
  oldPwd.value = ''
  newPwd.value = ''
  confirmPwd.value = ''
  showPwd.value = true
}

async function submitPwd() {
  if (!oldPwd.value) { store.toast('请输入旧密码', 'warning'); return }
  if (newPwd.value.length < 6) { store.toast('新密码至少 6 位', 'warning'); return }
  if (newPwd.value !== confirmPwd.value) { store.toast('两次输入的新密码不一致', 'warning'); return }
  if (oldPwd.value === newPwd.value) { store.toast('新密码不能与旧密码相同', 'warning'); return }
  pwdLoading.value = true
  try {
    const res = await api.auth.changePassword(oldPwd.value, newPwd.value)
    if (res.success) {
      store.toast('密码已修改，请重新登录', 'success')
      showPwd.value = false
      store.setAdminToken('')
      router.push('/login')
    } else {
      store.toast(res.message || '修改失败', 'error')
    }
  } catch (e: any) {
    store.toast(e?.message || '修改失败', 'error')
  } finally {
    pwdLoading.value = false
  }
}

async function logout() {
  try { await api.auth.logout() } catch { /* 忽略 */ }
  store.setAdminToken('')
  router.push('/login')
}

// 打开分享注册页（新标签）
function openShare() {
  window.open(`${location.origin}${location.pathname}#/register`, '_blank')
}

// 返回登录页（保留登录态，仅跳转）
function gotoLogin() {
  router.push('/login')
}
</script>

<template>
  <div class="app-shell">
    <aside v-if="!isPublic" class="sidebar">
      <div class="sidebar-brand">
        <svg class="brand-logo" viewBox="0 0 52 52" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
          <defs>
            <linearGradient id="brandGrad" x1="0" y1="0" x2="52" y2="52" gradientUnits="userSpaceOnUse">
              <stop stop-color="#4f6ef7" />
              <stop offset="1" stop-color="#7c5cf0" />
            </linearGradient>
          </defs>
          <rect width="52" height="52" rx="15" fill="url(#brandGrad)" />
          <path d="M15 20.5 22 26l-7 5.5" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />
          <path d="M26 32h11" stroke="#fff" stroke-width="3" stroke-linecap="round" />
        </svg>
        <div class="brand-text">
          <div class="brand-title">CodeBuddy</div>
          <div class="brand-sub">号池面板</div>
        </div>
      </div>

      <nav class="sidebar-nav">
        <RouterLink
          v-for="item in navItems"
          :key="item.path"
          :to="item.path"
          class="nav-item"
          :class="{ active: route.path === item.path }"
        >
          <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <template v-if="item.icon === 'pool'">
              <ellipse cx="12" cy="5" rx="8" ry="3"></ellipse>
              <path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5"></path>
              <path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"></path>
            </template>
            <template v-else-if="item.icon === 'daemon'">
              <path d="M12 3l7 3v5c0 4.5-3 7.6-7 9-4-1.4-7-4.5-7-9V6l7-3z"></path>
              <path d="M9.5 12l1.8 1.8L15 10"></path>
            </template>
            <template v-else-if="item.icon === 'proxy'">
              <circle cx="6" cy="6" r="2.4"></circle>
              <circle cx="18" cy="6" r="2.4"></circle>
              <circle cx="12" cy="18" r="2.4"></circle>
              <path d="M8 7l3 8.5"></path>
              <path d="M16 7l-3 8.5"></path>
              <path d="M8.4 6h7.2"></path>
            </template>
            <template v-else-if="item.icon === 'key'">
              <circle cx="7.5" cy="15.5" r="4.5"></circle>
              <path d="M11 12l9-9"></path>
              <path d="M16 7l3 3"></path>
              <path d="M13 10l3 3"></path>
            </template>
            <template v-else-if="item.icon === 'doc'">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path>
              <path d="M14 2v6h6"></path>
              <path d="M16 13H8"></path>
              <path d="M16 17H8"></path>
              <path d="M10 9H8"></path>
            </template>
            <template v-else-if="item.icon === 'help'">
              <circle cx="12" cy="12" r="9.2"></circle>
              <path d="M9.4 9a2.6 2.6 0 0 1 5 .95c0 1.7-2.4 2.1-2.4 3.55"></path>
              <circle cx="12" cy="17" r=".4" fill="currentColor"></circle>
            </template>
            <template v-else>
              <path d="M13 2L4.5 13.5H11L10 22l8.5-11.5H12L13 2z"></path>
            </template>
          </svg>
          <span>{{ item.label }}</span>
        </RouterLink>

        <button type="button" class="nav-item nav-action" @click="openPwd">
          <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <rect x="4" y="10" width="16" height="11" rx="2.5"></rect>
            <path d="M8 10V7a4 4 0 0 1 8 0v3"></path>
            <circle cx="12" cy="15.5" r="1.4"></circle>
          </svg>
          <span>修改密码</span>
        </button>
      </nav>

      <div class="sidebar-footer">
        <div class="user-chip" :title="store.adminUsername || 'admin'">
          <span class="user-avatar">{{ (store.adminUsername || 'A')[0].toUpperCase() }}</span>
          <span class="user-name">{{ store.adminUsername || 'admin' }}</span>
          <button class="chip-logout" title="退出登录" @click="logout">退出</button>
        </div>
        <div class="foot-links">
          <button class="link-btn" @click="openShare">分享注册页</button>
          <button class="link-btn" @click="gotoLogin">返回登录页</button>
        </div>
      </div>
    </aside>

    <div class="bg-glow" aria-hidden="true"></div>

    <main class="main-area" :class="{ 'no-sidebar': isPublic }">
      <RouterView v-slot="{ Component }">
        <Transition name="fade" mode="out-in">
          <component :is="Component" />
        </Transition>
      </RouterView>
    </main>

    <!-- 修改密码弹窗 -->
    <Teleport to="body">
      <div v-if="showPwd" class="pwd-mask" @click.self="showPwd = false">
        <div class="pwd-dialog">
          <div class="pwd-title">修改密码</div>
          <label class="pwd-label">旧密码</label>
          <input v-model="oldPwd" type="password" class="pwd-input" placeholder="请输入旧密码" autocomplete="current-password" />
          <label class="pwd-label">新密码</label>
          <input v-model="newPwd" type="password" class="pwd-input" placeholder="至少 6 位" autocomplete="new-password" />
          <label class="pwd-label">确认新密码</label>
          <input v-model="confirmPwd" type="password" class="pwd-input" placeholder="再次输入新密码" autocomplete="new-password" @keyup.enter="submitPwd" />
          <div class="pwd-actions">
            <button class="btn btn-ghost btn-sm" @click="showPwd = false" :disabled="pwdLoading">取消</button>
            <button class="btn btn-primary btn-sm" @click="submitPwd" :disabled="pwdLoading">{{ pwdLoading ? '提交中…' : '确定' }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Toast 通知 -->
    <Teleport to="body">
      <div class="toast-wrap">
        <TransitionGroup name="slide-up">
          <div
            v-for="t in store.toasts"
            :key="t.id"
            class="toast"
            :class="t.type"
          >
            {{ t.message }}
          </div>
        </TransitionGroup>
      </div>
    </Teleport>
  </div>
</template>


<style scoped>
.app-shell {
  display: flex;
  min-height: 100vh;
}

/* ---------- 全局氛围光晕（延续登录页） ---------- */
.bg-glow {
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  background:
    radial-gradient(720px 480px at 12% -6%, rgba(79, 110, 247, .09), transparent 70%),
    radial-gradient(640px 440px at 92% 96%, rgba(124, 92, 240, .08), transparent 70%);
}

/* ---------- 侧边栏 · 毛玻璃 ---------- */
.sidebar {
  width: var(--sidebar-w);
  flex-shrink: 0;
  background: rgba(255, 255, 255, .72);
  -webkit-backdrop-filter: blur(20px) saturate(1.6);
  backdrop-filter: blur(20px) saturate(1.6);
  border-right: 1px solid rgba(0, 0, 0, .05);
  display: flex;
  flex-direction: column;
  padding: 20px 14px;
  position: sticky;
  top: 0;
  height: 100vh;
  z-index: 10;
}

.sidebar-brand {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 2px 10px 18px;
  margin-bottom: 14px;
  position: relative;
}
.sidebar-brand::after {
  content: '';
  position: absolute;
  left: 10px; right: 10px; bottom: 0;
  height: 1px;
  background: var(--c-border);
}
.brand-logo {
  width: 36px; height: 36px;
  display: block;
  border-radius: 11px;
  box-shadow: 0 6px 16px rgba(79, 110, 247, .35);
  flex-shrink: 0;
}
.brand-title { font-weight: 700; font-size: 15px; letter-spacing: -.01em; color: var(--c-text); }
.brand-sub { font-size: 11.5px; color: var(--c-text-muted); }

.sidebar-nav { flex: 1; display: flex; flex-direction: column; gap: 3px; }
.nav-item {
  position: relative;
  display: flex; align-items: center; gap: 10px;
  padding: 9px 12px;
  border-radius: 10px;
  color: var(--c-text-secondary);
  font-weight: 500; font-size: 13.5px;
  transition: background var(--dur) var(--ease), color var(--dur) var(--ease);
}
.nav-item:hover { background: rgba(0, 0, 0, .04); color: var(--c-text); }
.nav-action {
  border: none; background: transparent; cursor: pointer;
  font-family: var(--font); text-align: left; width: 100%;
}
.nav-item.active {
  background: var(--c-primary-bg);
  color: var(--c-primary);
  font-weight: 600;
}
.nav-item.active::before {
  content: '';
  position: absolute;
  left: -6px; top: 50%;
  transform: translateY(-50%);
  width: 3px; height: 18px;
  border-radius: 999px;
  background: var(--c-grad);
}
.nav-icon {
  width: 19px; height: 19px;
  flex-shrink: 0;
  opacity: .75;
  transition: opacity var(--dur) var(--ease);
}
.nav-item:hover .nav-icon { opacity: .95; }
.nav-item.active .nav-icon { opacity: 1; }

.sidebar-footer {
  border-top: 1px solid var(--c-border);
  padding-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.user-chip {
  display: flex; align-items: center; gap: 9px;
  padding: 7px 9px;
  border-radius: 10px;
  background: rgba(0, 0, 0, .035);
  transition: background var(--dur) var(--ease);
}
.user-chip:hover { background: rgba(0, 0, 0, .055); }
.user-avatar {
  width: 28px; height: 28px;
  border-radius: 50%;
  background: var(--c-grad);
  color: #fff;
  font-size: 12.5px; font-weight: 700;
  display: flex; align-items: center; justify-content: center;
  flex-shrink: 0;
  box-shadow: 0 2px 8px rgba(79, 110, 247, .35);
}
.user-name {
  flex: 1; min-width: 0;
  font-size: 13px; font-weight: 600;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.chip-logout {
  border: none; background: transparent; cursor: pointer;
  color: var(--c-text-muted); font-size: 12px; padding: 3px 6px;
  border-radius: 6px; flex-shrink: 0; font-family: var(--font);
  transition: all var(--dur) var(--ease);
}
.chip-logout:hover { color: var(--c-danger); background: var(--c-danger-bg); }
.foot-links {
  display: flex; align-items: center; justify-content: center; gap: 14px;
}
.link-btn {
  border: none; background: transparent; cursor: pointer;
  color: var(--c-text-secondary); font-size: 12.5px; padding: 2px 4px;
  font-family: var(--font);
  transition: color var(--dur) var(--ease);
}
.link-btn:hover { color: var(--c-primary); }
.link-sep { color: var(--c-border-strong); font-size: 12px; }

/* ---------- 主区 ---------- */
.main-area {
  flex: 1;
  min-width: 0;
  padding: 28px 32px 56px;
  position: relative;
  z-index: 1;
}
.main-area.no-sidebar {
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* ---------- Toast · 毛玻璃浮层 ---------- */
.toast-wrap {
  position: fixed;
  top: 20px; right: 20px;
  z-index: 9999;
  display: flex; flex-direction: column; gap: 10px;
  pointer-events: none;
}
.toast {
  padding: 12px 18px;
  border-radius: 12px;
  font-size: 13.5px; font-weight: 500;
  box-shadow: var(--shadow-md);
  max-width: 360px;
  pointer-events: auto;
  color: #fff;
  -webkit-backdrop-filter: blur(12px);
  backdrop-filter: blur(12px);
}
.toast.success { background: rgba(36, 138, 61, .92); }
.toast.error { background: rgba(215, 0, 21, .92); }
.toast.warning { background: rgba(178, 80, 0, .92); }
.toast.info { background: rgba(0, 113, 227, .92); }

/* ---------- 修改密码弹窗 ---------- */
.pwd-mask {
  position: fixed; inset: 0;
  background: rgba(29, 29, 31, .4);
  -webkit-backdrop-filter: blur(6px);
  backdrop-filter: blur(6px);
  display: flex; align-items: center; justify-content: center;
  z-index: 9998;
  padding: 16px;
  animation: fadeIn .2s var(--ease);
}
.pwd-dialog {
  width: 100%; max-width: 380px;
  background: var(--c-surface);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-lg);
  padding: 26px 24px 20px;
  display: flex; flex-direction: column; gap: 8px;
  animation: scaleIn .28s var(--ease);
}
.pwd-title { font-size: 17px; font-weight: 700; margin-bottom: 8px; letter-spacing: -.01em; }
.pwd-label { font-size: 12.5px; font-weight: 500; color: var(--c-text-secondary); margin-top: 8px; }
.pwd-input {
  width: 100%;
  padding: 10px 13px;
  border: 1px solid var(--c-border-strong);
  border-radius: var(--radius-sm);
  background: var(--c-surface);
  color: var(--c-text);
  font-size: 14px;
  outline: none;
  font-family: var(--font);
  transition: border-color var(--dur) var(--ease), box-shadow var(--dur) var(--ease);
}
.pwd-input:focus {
  border-color: var(--c-primary);
  box-shadow: 0 0 0 4px rgba(80, 104, 240, .12);
}
.pwd-actions {
  display: flex; justify-content: flex-end; gap: 8px;
  margin-top: 16px;
}

@media (max-width: 768px) {
  .app-shell { flex-direction: column; }
  .bg-glow { position: fixed; }
  .sidebar {
    width: 100%; height: auto;
    position: sticky;
    flex-direction: row;
    align-items: center;
    padding: 8px 12px;
    gap: 10px;
  }
  .sidebar-brand { margin: 0; padding: 0 4px; }
  .sidebar-brand::after { display: none; }
  .brand-logo { width: 30px; height: 30px; border-radius: 9px; }
  .brand-text { display: none; }
  .sidebar-nav { flex-direction: row; gap: 2px; overflow-x: auto; }
  .nav-item { padding: 8px 10px; font-size: 12.5px; white-space: nowrap; }
  .nav-item.active::before { display: none; }
  .nav-icon { display: none; }
  .sidebar-footer { border: none; padding: 0; flex-direction: row; align-items: center; }
  .chip-logout { display: none; }
  .foot-links { display: none; }
  .main-area { padding: 18px 14px 44px; }
}
</style>
