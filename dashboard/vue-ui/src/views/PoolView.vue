<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '@/api'
import { connectMonitor } from '@/api/stream'
import { useAppStore } from '@/stores/app'

interface Account {
  phone: string
  uid: string
  credits: number
  registered: string
  expires_at: number
  has_refresh: boolean
  status: 'available' | 'exhausted' | 'dead'
}

interface PoolStats {
  total: number
  available: number
  exhausted: number
  dead: number
  credits: number
  accounts: Account[]
}

const store = useAppStore()

const loading = ref(true)
const acting = ref(false)
const stats = ref<PoolStats>({ total: 0, available: 0, exhausted: 0, dead: 0, credits: 0, accounts: [] })
const rowBusy = ref<Record<string, boolean>>({})

// 手动签到/刷新防连点：与后端 15 秒冷却对齐，避免连点直打 CodeBuddy 账单接口触发风控
const OP_COOLDOWN_SEC = 15
const cooling = ref<Record<string, number>>({})  // key=手机号或'__batch__' -> 冷却剩余秒数
let coolTimer: ReturnType<typeof setInterval> | null = null

function startCooldown(key: string) {
  cooling.value[key] = OP_COOLDOWN_SEC
  if (coolTimer) return
  coolTimer = setInterval(() => {
    for (const k of Object.keys(cooling.value)) {
      if (cooling.value[k] > 1) cooling.value[k]--
      else delete cooling.value[k]
    }
    if (!Object.keys(cooling.value).length && coolTimer) {
      clearInterval(coolTimer)
      coolTimer = null
    }
  }, 1000)
}

const batchCooling = computed(() => (cooling.value['__batch__'] ?? 0) > 0)

let disconnect: (() => void) | null = null

const statusMap: Record<string, { label: string; cls: string }> = {
  available: { label: '可用', cls: 'ok' },
  exhausted: { label: '耗尽', cls: 'warn' },
  dead: { label: '失效', cls: 'bad' },
}

const accounts = computed(() => stats.value.accounts || [])

// 重复账号检测：按数字部分归一化分组，同一号出现多次即视为重复
function normPhone(p: string) { return (p || '').replace(/\D/g, '') }
const dupPhones = computed(() => {
  const cnt: Record<string, number> = {}
  for (const a of accounts.value) {
    const k = normPhone(a.phone)
    if (k) cnt[k] = (cnt[k] || 0) + 1
  }
  return new Set(Object.keys(cnt).filter(k => cnt[k] > 1))
})
const dupCount = computed(() => dupPhones.value.size)
const isDup = (p: string) => dupPhones.value.has(normPhone(p))

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const res = await api.pool.status()
    if (res.success) stats.value = res.data
  } catch (e: any) {
    if (!silent) store.toast(e.message || '加载失败', 'error')
  } finally {
    loading.value = false
  }
}

async function act(fn: () => Promise<any>, okMsg: string) {
  if (acting.value) return
  acting.value = true
  try {
    const res = await fn()
    if (res.success) {
      store.toast(okMsg, 'success')
      await load(true)
    } else {
      store.toast(res.message || '操作失败', 'error')
    }
  } catch (e: any) {
    store.toast(e.message || '操作失败', 'error')
  } finally {
    acting.value = false
  }
}

async function batchAct(fn: () => Promise<any>, okMsg: string) {
  if (acting.value || cooling.value['__batch__'] > 0) return
  startCooldown('__batch__')
  await act(fn, okMsg)
}

const refreshAll = () => batchAct(() => api.pool.refreshAll(), '已刷新全部积分')

async function rowAct(phone: string, fn: () => Promise<any>, okMsg: string) {
  if (cooling.value[phone] > 0) return
  startCooldown(phone)
  rowBusy.value[phone] = true
  try {
    const res = await fn()
    if (res.success) {
      store.toast(okMsg, 'success')
      await load(true)
    } else {
      store.toast(res.message || '操作失败', 'error')
    }
  } catch (e: any) {
    store.toast(e.message || '操作失败', 'error')
  } finally {
    delete rowBusy.value[phone]
  }
}

const refreshOne = (p: string) => rowAct(p, () => api.pool.refreshOne(p), `${p} 已刷新`)

// 自绘删除确认（原生 confirm 在部分浏览器/内嵌 webview 会被拦截导致按钮"没反应"）
const confirmDel = ref<string | null>(null)

function askRemove(p: string) {
  confirmDel.value = p
}

async function confirmRemove() {
  const p = confirmDel.value
  confirmDel.value = null
  if (!p) return
  await rowAct(p, () => api.pool.remove(p), `${p} 已删除`)
}

function fmtExp(ts: number) {
  if (!ts) return '-'
  const d = new Date(ts)
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

onMounted(() => {
  disconnect = connectMonitor((f) => {
    if (f.pool) {
      stats.value = f.pool as PoolStats
      loading.value = false
    }
  })
})
onUnmounted(() => { disconnect?.(); disconnect = null; if (coolTimer) clearInterval(coolTimer) })
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div class="page-title-wrap">
        <h1 class="page-title">号池管理</h1>
        <p class="page-sub">CodeBuddy 账号池状态与操作</p>
      </div>
      <div class="head-actions">
        <button class="btn btn-outline" :disabled="acting || batchCooling" @click="refreshAll">{{ batchCooling ? `冷却 ${cooling['__batch__'] ?? 0}s` : '全部刷新' }}</button>
      </div>
    </header>
    <p class="muted checkin-tip">签到由系统每日凌晨自动批量执行（新注册账号即时自动签到），无需手动操作。</p>

    <!-- 统计卡片 -->
    <div class="stat-grid">
      <div class="card stat-card" style="animation-delay: 0ms">
        <div class="stat-icon tile-primary">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <ellipse cx="12" cy="5" rx="8" ry="3"></ellipse>
            <path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5"></path>
            <path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"></path>
          </svg>
        </div>
        <div class="stat-num">{{ stats.total }}</div>
        <div class="stat-label">总账号</div>
      </div>
      <div class="card stat-card" style="animation-delay: 40ms">
        <div class="stat-icon tile-success">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9"></circle>
            <path d="M8.5 12.5l2.5 2.5 5-5.5"></path>
          </svg>
        </div>
        <div class="stat-num c-ok">{{ stats.available }}</div>
        <div class="stat-label">可用</div>
      </div>
      <div class="card stat-card" style="animation-delay: 80ms">
        <div class="stat-icon tile-warning">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9"></circle>
            <path d="M12 7v5.5"></path>
            <path d="M12 16h.01"></path>
          </svg>
        </div>
        <div class="stat-num c-warn">{{ stats.exhausted }}</div>
        <div class="stat-label">耗尽</div>
      </div>
      <div class="card stat-card" style="animation-delay: 120ms">
        <div class="stat-icon tile-danger">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9"></circle>
            <path d="M9 9l6 6"></path>
            <path d="M15 9l-6 6"></path>
          </svg>
        </div>
        <div class="stat-num c-bad">{{ stats.dead }}</div>
        <div class="stat-label">失效</div>
      </div>
      <div class="card stat-card" style="animation-delay: 160ms">
        <div class="stat-icon tile-violet">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 2l2.4 5.9 6.3.5-4.8 4.1 1.5 6.2L12 15.6 6.6 18.7l1.5-6.2-4.8-4.1 6.3-.5L12 2z"></path>
          </svg>
        </div>
        <div class="stat-num c-primary">{{ stats.credits }}</div>
        <div class="stat-label">总积分</div>
      </div>
      <div class="card stat-card" style="animation-delay: 200ms">
        <div class="stat-icon tile-muted">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <rect x="8" y="8" width="12" height="12" rx="2.5"></rect>
            <path d="M16 8V6a2.5 2.5 0 0 0-2.5-2.5H6A2.5 2.5 0 0 0 3.5 6v7.5A2.5 2.5 0 0 0 6 16h2"></path>
          </svg>
        </div>
        <div class="stat-num" :class="dupCount > 0 ? 'c-bad' : ''">{{ dupCount }}</div>
        <div class="stat-label">重复账号</div>
      </div>
    </div>

    <!-- 账号表格 -->
    <div class="card table-card">
      <div v-if="loading" class="loading-box">
        <div class="spinner spinner-lg"></div>
        <div class="muted">加载中...</div>
      </div>

      <div v-else-if="accounts.length === 0" class="empty-box">
        <div class="empty-title">暂无账号</div>
        <div class="muted">分享注册链接给好友，邀请他们加入共享计划</div>
      </div>

      <div v-else class="table-scroll">
        <table class="data-table">
          <thead>
            <tr>
              <th>手机号</th>
              <th>积分</th>
              <th>状态</th>
              <th>注册日期</th>
              <th>令牌过期</th>
              <th>Refresh</th>
              <th class="th-op">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in accounts" :key="a.phone" :class="{ 'row-dup': isDup(a.phone) }">
              <td class="mono">
                {{ a.phone }}
                <span v-if="isDup(a.phone)" class="pill bad dup-tag">重复</span>
              </td>
              <td class="mono num">{{ a.credits }}</td>
              <td>
                <span class="pill" :class="statusMap[a.status]?.cls || 'muted'">
                  {{ statusMap[a.status]?.label || a.status }}
                </span>
              </td>
              <td>{{ a.registered || '-' }}</td>
              <td>{{ fmtExp(a.expires_at) }}</td>
              <td>
                <span class="pill" :class="a.has_refresh ? 'ok' : 'muted'">
                  {{ a.has_refresh ? '有' : '无' }}
                </span>
              </td>
              <td class="op-cell">
                <button class="btn btn-ghost btn-sm" :disabled="rowBusy[a.phone] || cooling[a.phone] > 0" @click="refreshOne(a.phone)">{{ cooling[a.phone] > 0 ? `${cooling[a.phone]}s` : '刷新' }}</button>
                <button class="btn btn-ghost btn-sm danger" :disabled="rowBusy[a.phone]" @click="askRemove(a.phone)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 删除确认弹窗（自绘，替代原生 confirm） -->
    <Teleport to="body">
      <div v-if="confirmDel" class="modal-mask" @click.self="confirmDel = null">
        <div class="modal">
          <div class="modal-head">
            <div class="modal-title">删除账号</div>
            <button class="btn btn-ghost btn-sm" @click="confirmDel = null" title="关闭">
              <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>
            </button>
          </div>
          <p style="margin:0 0 6px;">确认删除账号 <b class="mono">{{ confirmDel }}</b>？</p>
          <p class="muted" style="margin:0 0 18px;">删除后将从号池移除，不可恢复。</p>
          <div style="display:flex;gap:10px;justify-content:flex-end;">
            <button class="btn btn-outline" @click="confirmDel = null">取消</button>
            <button class="btn btn-danger-solid" @click="confirmRemove">确认删除</button>
          </div>
        </div>
      </div>
    </Teleport>

  </div>
</template>


<style scoped>
.page { max-width: 1080px; margin: 0 auto; animation: fadeInUp .32s var(--ease); }

.page-head {
  display: flex; align-items: flex-end; justify-content: space-between;
  gap: 14px; flex-wrap: wrap; margin-bottom: 22px;
}
.page-title-wrap { position: relative; padding-left: 14px; }
.page-title-wrap::before {
  content: '';
  position: absolute;
  left: 0; top: 5px; bottom: 5px;
  width: 4px;
  border-radius: 999px;
  background: var(--c-grad);
}
.page-title { font-size: 26px; font-weight: 600; letter-spacing: -.02em; margin: 0 0 3px; color: var(--c-text); }
.page-sub { color: var(--c-text-muted); margin: 0; font-size: 14px; }
.head-actions { display: flex; gap: 10px; flex-wrap: wrap; }
.checkin-tip { font-size: 12.5px; margin: -10px 0 16px; }

.stat-grid {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 14px;
  margin-bottom: 20px;
}
.stat-card {
  text-align: center;
  padding: 18px 10px 16px;
  animation: fadeInUp .45s var(--ease) backwards;
  transition: transform var(--dur) var(--ease), box-shadow var(--dur) var(--ease);
}
.stat-card:hover { transform: translateY(-2px); box-shadow: var(--shadow); }
.stat-icon {
  width: auto; height: auto;
  margin: 0 auto 10px;
  display: flex; align-items: center; justify-content: center;
  background: none;
  color: var(--c-text-secondary, var(--c-text-muted));
}
.stat-icon svg { width: 24px; height: 24px; stroke-width: 1.6; }
.tile-primary { color: var(--c-primary); }
.tile-success { color: var(--c-success); }
.tile-warning { color: var(--c-warning); }
.tile-danger { color: var(--c-danger); }
.tile-violet { color: var(--c-violet); }
.tile-muted { color: var(--c-text-muted); }
.stat-num {
  font-size: 28px; font-weight: 600; line-height: 1.15;
  letter-spacing: -.02em;
  font-variant-numeric: tabular-nums;
  color: var(--c-text);
}
.stat-label { color: var(--c-text-muted); font-size: 12px; margin-top: 5px; font-weight: 500; letter-spacing: .02em; }
.c-ok { color: var(--c-success); }
.c-warn { color: var(--c-warning); }
.c-bad { color: var(--c-danger); }
.c-primary { color: var(--c-primary); }

.table-card { padding: 8px 0 6px; animation: fadeInUp .45s .12s var(--ease) backwards; }
/* 表头灰条与卡片同宽铺满：两侧保留与卡片圆角匹配的小留白，避免形成偏移色块 */
.table-scroll { overflow-x: auto; padding: 0 12px; }
.mono { font-family: var(--font-mono); font-size: 12.5px; }
.num { font-weight: 600; }
.th-op { width: 190px; }
.op-cell { white-space: nowrap; }
.op-cell .danger { color: var(--c-danger); }
.op-cell .danger:hover:not(:disabled) { background: var(--c-danger-bg); }

.row-dup td { background: var(--c-danger-bg) !important; }
.row-dup:hover td { background: #fde8ea !important; }
.dup-tag { margin-left: 6px; font-size: 11px; }

.btn-danger-solid {
  background: var(--c-danger); color: #fff; border: none;
  padding: 10px 18px; border-radius: var(--radius-sm);
  font-size: 13.5px; font-weight: 600; cursor: pointer;
  font-family: var(--font);
  box-shadow: 0 1px 2px rgba(215, 0, 21, .3), 0 6px 16px rgba(215, 0, 21, .22);
  transition: all var(--dur) var(--ease);
}
.btn-danger-solid:hover { background: #c20012; transform: scale(1.02); }
.btn-danger-solid:active { transform: scale(.98); }

.loading-box, .empty-box { padding: 64px 20px; text-align: center; }
.empty-title { font-size: 15px; font-weight: 600; margin-bottom: 6px; }
.muted { color: var(--c-text-muted); font-size: 13px; }

.modal-mask {
  position: fixed; inset: 0;
  background: rgba(29, 29, 31, .4);
  -webkit-backdrop-filter: blur(6px);
  backdrop-filter: blur(6px);
  display: flex; align-items: center; justify-content: center;
  z-index: 1000; padding: 20px;
  animation: fadeIn .2s var(--ease);
}
.modal {
  width: 100%; max-width: 420px;
  background: var(--c-surface);
  border-radius: var(--radius-xl);
  padding: 24px 24px 22px;
  box-shadow: var(--shadow-lg);
  animation: scaleIn .26s var(--ease);
}
.modal-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.modal-title { font-size: 17px; font-weight: 700; letter-spacing: -.01em; }
.reg-output {
  margin-top: 14px;
  background: var(--c-log-bg);
  border: 1px solid var(--c-log-border);
  color: var(--c-log-text);
  border-radius: var(--radius-sm);
  padding: 12px;
  font-size: 12px;
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
}

@media (max-width: 768px) {
  .page-title { font-size: 22px; }
  .stat-grid { grid-template-columns: repeat(3, 1fr); gap: 10px; }
  .stat-card { padding: 14px 6px 12px; }
  .stat-icon { margin-bottom: 8px; }
  .stat-icon svg { width: 20px; height: 20px; }
  .stat-num { font-size: 22px; }
}
</style>
