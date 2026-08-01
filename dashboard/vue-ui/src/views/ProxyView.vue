<template>
  <div class="page">
    <header class="page-head">
      <div class="page-title-wrap">
        <h1 class="page-title">代理监控</h1>
        <p class="page-sub">Go 代理在线状态与代理侧账号调度</p>
      </div>
    </header>

    <div class="stat-grid">
      <div class="card stat-card" style="animation-delay: 0ms">
        <div class="stat-icon" :class="status.online ? 'tile-success' : 'tile-danger'">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M5 12.55a11 11 0 0 1 14.08 0"></path>
            <path d="M8.53 16.11a6 6 0 0 1 6.95 0"></path>
            <circle cx="12" cy="19.5" r=".6" fill="currentColor"></circle>
          </svg>
        </div>
        <div class="stat-num">
          <span class="pill" :class="status.online ? 'ok' : 'bad'">
            <span class="dot"></span>{{ status.online ? '在线' : '离线' }}
          </span>
        </div>
        <div class="stat-label">代理状态</div>
      </div>
      <div class="card stat-card" style="animation-delay: 40ms">
        <div class="stat-icon tile-primary">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="9" cy="8" r="3.2"></circle>
            <path d="M3.5 19c.6-3 2.9-4.5 5.5-4.5s4.9 1.5 5.5 4.5"></path>
            <path d="M16 5.5a3.2 3.2 0 0 1 0 5.9"></path>
            <path d="M17.5 14.9c1.7.6 2.7 1.9 3 4.1"></path>
          </svg>
        </div>
        <div class="stat-num">{{ status.available ?? 0 }}<span class="stat-sub">/ {{ status.total ?? 0 }}</span></div>
        <div class="stat-label">可用账号</div>
      </div>
      <div class="card stat-card" style="animation-delay: 80ms">
        <div class="stat-icon tile-violet">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 2l2.4 5.9 6.3.5-4.8 4.1 1.5 6.2L12 15.6 6.6 18.7l1.5-6.2-4.8-4.1 6.3-.5L12 2z"></path>
          </svg>
        </div>
        <div class="stat-num">{{ fmtCredits(status.credits_total) }}</div>
        <div class="stat-label">总积分</div>
      </div>
      <div class="card stat-card" style="animation-delay: 120ms">
        <div class="stat-icon tile-success">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M22 11.1V12a10 10 0 1 1-5.93-9.14"></path>
            <path d="M22 4L12 14.01l-3-3"></path>
          </svg>
        </div>
        <div class="stat-num">{{ status.total_success ?? 0 }}</div>
        <div class="stat-label">累计成功</div>
      </div>
      <div class="card stat-card" style="animation-delay: 160ms">
        <div class="stat-icon tile-muted">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9"></circle>
            <path d="M12 7v5l3.5 2"></path>
          </svg>
        </div>
        <div class="stat-num">{{ fmtUptime(status.uptime_sec) }}</div>
        <div class="stat-label">运行时长</div>
      </div>
    </div>

    <div class="card panel">
      <div class="panel-head">
        <h3>代理侧账号</h3>
        <span class="muted">{{ accountRows.length }} 个 · 实时推送</span>
      </div>
      <div v-if="loading" class="empty">加载中…</div>
      <div v-else-if="!accountRows.length" class="empty">暂无数据</div>
      <div v-else class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>手机号</th>
              <th>积分</th>
              <th>状态</th>
              <th>成功</th>
              <th>错误</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in accountRows" :key="i">
              <td class="mono">{{ row.phone ?? '—' }}</td>
              <td class="mono">{{ fmtCredits(row.credits) }}</td>
              <td><span class="pill" :class="acctState(row).cls">{{ acctState(row).text }}</span></td>
              <td class="mono">{{ row.success ?? 0 }}</td>
              <td class="mono" :class="{ 'err-text': num(row.consecutive_errors) > 0 }">{{ row.errors ?? 0 }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card panel">
      <div class="panel-head">
        <h3>请求日志</h3>
        <span class="muted">实时推送</span>
      </div>
      <div v-if="!logLines.length" class="empty">暂无日志</div>
      <div v-else class="log-box">
        <div v-for="(line, i) in logLines" :key="i" class="log-line">{{ line }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { connectMonitor } from '@/api/stream'

const status = ref<Record<string, any>>({})
const accountRows = ref<Record<string, any>[]>([])
const logLines = ref<string[]>([])
const loading = ref(true)
let disconnect: (() => void) | null = null

const fmtCredits = (v: any) => {
  const n = Number(v)
  if (isNaN(n)) return '0'
  return n % 1 === 0 ? String(n) : n.toFixed(1)
}
const num = (v: any) => Number(v) || 0

const fmtUptime = (sec: any) => {
  const s = num(sec)
  if (s <= 0) return '—'
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}天${h}时`
  if (h > 0) return `${h}时${m}分`
  return `${m}分`
}

const truthy = (v: any) => v === true || v === 'true' || v === '是' || v === 1
const acctState = (row: Record<string, any>) => {
  if (truthy(row.dead)) return { text: '失效', cls: 'bad' }
  if (truthy(row.exhausted)) return { text: '待恢复', cls: 'warn' }
  return { text: '可用', cls: 'ok' }
}

onMounted(() => {
  disconnect = connectMonitor((f) => {
    status.value = f.status?.data || f.status || {}
    const aData = f.accounts?.data || f.accounts || {}
    const rows = aData.accounts || aData.rows || (Array.isArray(aData) ? aData : [])
    accountRows.value = Array.isArray(rows) ? rows : []
    const lData = f.logs?.data || f.logs || {}
    const lines = lData.lines || lData.logs || (Array.isArray(lData) ? lData : [])
    logLines.value = Array.isArray(lines) ? lines : []
    loading.value = false
  })
})
onUnmounted(() => { disconnect?.(); disconnect = null })
</script>


<style scoped>
.page { max-width: 1080px; margin: 0 auto; animation: fadeInUp .32s var(--ease); }
.page-head { display: flex; align-items: flex-end; justify-content: space-between; margin-bottom: 22px; gap: 12px; flex-wrap: wrap; }
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

.stat-grid {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
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
.tile-danger { color: var(--c-danger); }
.tile-violet { color: var(--c-violet); }
.tile-muted { color: var(--c-text-muted); }
.stat-num {
  font-size: 27px; font-weight: 600; line-height: 1.15;
  letter-spacing: -.02em;
  font-variant-numeric: tabular-nums;
  color: var(--c-text);
}
.stat-sub { font-size: 15px; font-weight: 500; color: var(--c-text-muted); }
.stat-label { color: var(--c-text-muted); font-size: 12px; margin-top: 5px; font-weight: 500; letter-spacing: .02em; }

.pill .dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: currentColor;
  animation: pulseDot 2s var(--ease) infinite;
}

.panel { margin-bottom: 18px; animation: fadeInUp .45s .1s var(--ease) backwards; }
.panel-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.panel-head h3 { font-size: 16px; font-weight: 600; letter-spacing: -.01em; margin: 0; }
.muted { font-size: 12.5px; color: var(--c-text-muted); }

.table-wrap { overflow-x: auto; margin: 0 -4px; }
.mono { font-family: var(--font-mono); font-size: 12.5px; }
.err-text { color: var(--c-danger); font-weight: 600; }

.empty { color: var(--c-text-muted); font-size: 13px; padding: 28px 0; text-align: center; }

/* 日志块 · Apple 终端风格深色圆角 */
.log-box {
  background: #1d1d1f;
  border-radius: var(--radius);
  padding: 14px 16px;
  margin: 0 -4px;
  max-height: 320px;
  overflow-y: auto;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, .06);
}
.log-box::-webkit-scrollbar-thumb { background: rgba(255, 255, 255, .2); }
.log-line {
  font-family: var(--font-mono); font-size: 12px;
  color: rgba(235, 235, 245, .82); padding: 2.5px 0; word-break: break-all;
  line-height: 1.6;
}

@media (max-width: 768px) {
  .page-title { font-size: 22px; }
  .stat-grid { grid-template-columns: repeat(2, 1fr); gap: 10px; }
  .stat-card { padding: 14px 6px 12px; }
  .stat-icon { margin-bottom: 8px; }
  .stat-icon svg { width: 20px; height: 20px; }
  .stat-num { font-size: 21px; }
}
</style>
