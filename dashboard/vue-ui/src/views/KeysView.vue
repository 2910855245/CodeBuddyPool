<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '@/api'
import { useAppStore } from '@/stores/app'

const store = useAppStore()

interface KeyUsage {
  requests: number
  success: number
  failures: number
  tokens_in: number
  tokens_out: number
  rate_limited: number
  last_request: string
}
interface ApiKey {
  key: string
  name: string
  enabled: boolean
  rate_limit: number
  created_at: string
  last_used: string
  usage: KeyUsage
}

const loading = ref(true)
const keys = ref<ApiKey[]>([])
const proxyOnline = ref(true)
const acting = ref(false)

// 新建弹窗
const showCreate = ref(false)
const newName = ref('')
const creating = ref(false)
// 创建成功后展示明文 key（仅此一次）
const createdKey = ref('')
const createdName = ref('')
// 删除确认弹窗
const confirmDel = ref<ApiKey | null>(null)

let timer: ReturnType<typeof setInterval> | null = null

async function load() {
  try {
    const res = await api.keys.list()
    const d = res.data
    if (d && Array.isArray(d.keys)) {
      keys.value = d.keys
      proxyOnline.value = true
    } else if (d && d.online === false) {
      proxyOnline.value = false
    }
  } catch { /* 静默 */ }
}

async function createKey() {
  creating.value = true
  try {
    const res = await api.keys.create(newName.value.trim())
    const d = res.data
    if (d && d.key) {
      createdKey.value = d.key
      createdName.value = newName.value.trim()
      newName.value = ''
      showCreate.value = false
      store.toast('已创建，请立即保存明文 Key', 'success')
    } else {
      store.toast(d?.error || res.message || '创建失败', 'error')
    }
    await load()
  } catch (e: any) {
    store.toast(e.message || '创建失败', 'error')
  } finally {
    creating.value = false
  }
}

async function toggleKey(k: ApiKey) {
  acting.value = true
  try {
    await api.keys.toggle(k.key)
    store.toast(k.enabled ? '已禁用' : '已启用', 'success')
    await load()
  } catch (e: any) {
    store.toast(e.message || '操作失败', 'error')
  } finally {
    acting.value = false
  }
}

function askRemove(k: ApiKey) {
  confirmDel.value = k
}

async function confirmRemove() {
  const k = confirmDel.value
  if (!k) return
  acting.value = true
  try {
    await api.keys.remove(k.key)
    store.toast('已删除', 'success')
    confirmDel.value = null
    await load()
  } catch (e: any) {
    store.toast(e.message || '删除失败', 'error')
  } finally {
    acting.value = false
  }
}

function copy(text: string) {
  // 明文 http 或非安全上下文下 navigator.clipboard 不可用，降级用 execCommand
  const fallback = () => {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.setAttribute('readonly', '')
      ta.style.position = 'fixed'
      ta.style.left = '-9999px'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(ta)
      if (ok) store.toast('已复制', 'success')
      else store.toast('复制失败，请手动复制', 'warning')
    } catch {
      store.toast('复制失败，请手动复制', 'warning')
    }
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text)
      .then(() => store.toast('已复制', 'success'))
      .catch(fallback)
  } else {
    fallback()
  }
}

function closeCreated() {
  createdKey.value = ''
  createdName.value = ''
}

function openCreate() {
  newName.value = ''
  showCreate.value = true
}

const fmtNum = (n: number | undefined) => (n ?? 0).toLocaleString()
const fmtTime = (s: string) => (s ? s : '-')

onMounted(async () => {
  await load()
  loading.value = false
  timer = setInterval(load, 4000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div class="page-title-wrap">
        <h1 class="page-title">API 密钥</h1>
        <p class="page-sub">手动创建/管理发给别人调用的 Key，可随时禁用或删除</p>
      </div>
      <button class="btn btn-primary" @click="openCreate">新建密钥</button>
    </header>

    <div v-if="loading" class="card loading-box">
      <div class="spinner spinner-lg"></div>
      <div class="muted">加载中...</div>
    </div>

    <template v-else>
      <div v-if="!proxyOnline" class="card warn-card">无法连接到 Go 代理，请先在「代理」页确认其在线。</div>

      <!-- 列表 -->
      <div class="card">
        <div class="card-title">密钥列表<span class="muted">（每 4 秒自动刷新）</span></div>
        <div v-if="keys.length === 0" class="muted empty">暂无密钥，点击右上角「新建密钥」创建。</div>
        <div v-else class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>密钥</th>
                <th>备注</th>
                <th>状态</th>
                <th>限速</th>
                <th>请求</th>
                <th>成功</th>
                <th>Token 出</th>
                <th>最近调用</th>
                <th class="op-col">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="k in keys" :key="k.key" :class="{ disabled: !k.enabled }">
                <td class="mono">{{ k.key }}</td>
                <td>{{ k.name || '-' }}</td>
                <td>
                  <span class="pill" :class="k.enabled ? 'ok' : 'bad'">{{ k.enabled ? '启用' : '禁用' }}</span>
                </td>
                <td>{{ k.rate_limit }}/分</td>
                <td>{{ fmtNum(k.usage?.requests) }}</td>
                <td>{{ fmtNum(k.usage?.success) }}</td>
                <td>{{ fmtNum(k.usage?.tokens_out) }}</td>
                <td class="mono">{{ fmtTime(k.usage?.last_request || k.last_used) }}</td>
                <td class="op-col">
                  <button class="btn btn-outline btn-sm" :disabled="acting" @click="toggleKey(k)">
                    {{ k.enabled ? '禁用' : '启用' }}
                  </button>
                  <button class="btn btn-danger btn-sm" :disabled="acting" @click="askRemove(k)">删除</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <!-- 新建密钥弹窗 -->
    <Teleport to="body">
      <div v-if="showCreate" class="modal-mask" @click.self="showCreate = false">
        <div class="modal">
          <div class="modal-head">
            <div class="modal-title">新建密钥</div>
            <button class="btn btn-ghost btn-sm" @click="showCreate = false" title="关闭">
              <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>
            </button>
          </div>
          <div class="field">
            <label>备注名</label>
            <input
              v-model="newName"
              type="text"
              maxlength="50"
              placeholder="发给谁的标识，可留空"
              @keyup.enter="createKey"
            />
          </div>
          <div style="display:flex;gap:10px;justify-content:flex-end;">
            <button class="btn btn-outline" @click="showCreate = false">取消</button>
            <button class="btn btn-primary" :disabled="creating" @click="createKey">
              {{ creating ? '创建中...' : '创建密钥' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 创建成功：明文 key 仅显示一次 -->
    <Teleport to="body">
      <div v-if="createdKey" class="modal-mask">
        <div class="modal">
          <div class="modal-head">
            <div class="modal-title">密钥已创建{{ createdName ? `（${createdName}）` : '' }}</div>
          </div>
          <p class="warn-text">明文 Key 仅此一次显示，请立即复制保存，关闭后将无法再次查看。</p>
          <div class="key-row">
            <code class="key-code">{{ createdKey }}</code>
            <button class="btn btn-primary btn-sm" @click="copy(createdKey)">复制</button>
          </div>
          <div style="display:flex;justify-content:flex-end;margin-top:18px;">
            <button class="btn btn-outline" @click="closeCreated">我已保存</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 删除确认弹窗（自绘，替代原生 confirm） -->
    <Teleport to="body">
      <div v-if="confirmDel" class="modal-mask" @click.self="confirmDel = null">
        <div class="modal">
          <div class="modal-head">
            <div class="modal-title">删除密钥</div>
            <button class="btn btn-ghost btn-sm" @click="confirmDel = null" title="关闭">
              <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>
            </button>
          </div>
          <p style="margin:0 0 6px;">确认删除密钥 <b>{{ confirmDel.name || confirmDel.key }}</b>？</p>
          <p class="muted" style="margin:0 0 18px;">删除后对方立即无法调用，不可恢复。</p>
          <div style="display:flex;gap:10px;justify-content:flex-end;">
            <button class="btn btn-outline" @click="confirmDel = null">取消</button>
            <button class="btn btn-danger-solid" :disabled="acting" @click="confirmRemove">确认删除</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>


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

.loading-box { padding: 56px 0; text-align: center; }
.card-title { font-size: 16px; font-weight: 600; letter-spacing: -.01em; margin-bottom: 14px; display: flex; align-items: center; gap: 8px; }
.muted { color: var(--c-text-muted); font-size: 13px; font-weight: 400; }
.empty { padding: 20px 0; text-align: center; }
.warn-card {
  margin-bottom: 16px;
  color: var(--c-warning);
  background: var(--c-warning-bg);
  border-color: rgba(178, 80, 0, .18);
  font-size: 13.5px;
  font-weight: 500;
}

.warn-text { color: var(--c-warning); font-size: 13px; margin: 0 0 12px; }
.key-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.key-code {
  flex: 1; min-width: 260px;
  background: var(--c-log-bg);
  border: 1px solid var(--c-log-border);
  border-radius: var(--radius-sm);
  padding: 10px 13px;
  font-family: var(--font-mono);
  font-size: 13px;
  word-break: break-all;
  color: var(--c-log-text);
  font-variant-numeric: tabular-nums;
}

.table-scroll { overflow-x: auto; margin: 0 -4px; }
.mono { font-family: var(--font-mono); font-size: 12.5px; }
.op-col { white-space: nowrap; }
.op-col .btn { margin-right: 6px; }
tr.disabled td { opacity: .5; }

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
  width: 100%; max-width: 440px;
  background: var(--c-surface);
  border-radius: var(--radius-xl);
  padding: 24px 24px 22px;
  box-shadow: var(--shadow-lg);
  animation: scaleIn .26s var(--ease);
}
.modal-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 18px; }
.modal-title { font-size: 17px; font-weight: 700; letter-spacing: -.01em; }

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
.btn-danger-solid:disabled { opacity: .5; cursor: not-allowed; transform: none; }

@media (max-width: 768px) {
  .page-title { font-size: 22px; }
}
</style>
