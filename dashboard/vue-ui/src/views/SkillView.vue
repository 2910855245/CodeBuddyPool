<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api'
import { useAppStore } from '@/stores/app'

const store = useAppStore()

const loading = ref(true)
const markdown = ref('')
const proxyBase = ref('')

async function load() {
  loading.value = true
  try {
    const res = await api.skill.deployDoc()
    if (res.success && res.data) {
      markdown.value = res.data.markdown || ''
      proxyBase.value = res.data.proxy_base || ''
    } else {
      store.toast(res.message || '加载失败', 'error')
    }
  } catch (e: any) {
    store.toast(e.message || '加载失败', 'error')
  } finally {
    loading.value = false
  }
}

function copy() {
  const text = markdown.value
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
      if (ok) store.toast('已复制到剪贴板', 'success')
      else store.toast('复制失败，请手动复制', 'warning')
    } catch {
      store.toast('复制失败，请手动复制', 'warning')
    }
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text)
      .then(() => store.toast('已复制到剪贴板', 'success'))
      .catch(fallback)
  } else {
    fallback()
  }
}

function download() {
  const blob = new Blob([markdown.value], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'codebuddy-pool-skill.md'
  a.click()
  URL.revokeObjectURL(url)
  store.toast('已下载 skill.md', 'success')
}

onMounted(load)
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div class="page-title-wrap">
        <h1 class="page-title">部署文档</h1>
        <p class="page-sub">一键生成 skill.md，供其他 Agent 直接对接 API</p>
      </div>
      <div class="head-actions">
        <button class="btn btn-outline" :disabled="loading" @click="load">刷新</button>
        <button class="btn btn-outline" :disabled="loading || !markdown" @click="copy">复制文档</button>
        <button class="btn btn-primary" :disabled="loading || !markdown" @click="download">下载 skill.md</button>
      </div>
    </header>

    <div v-if="loading" class="card loading-box">
      <div class="spinner spinner-lg"></div>
      <div class="muted">生成中...</div>
    </div>

    <template v-else>
      <div class="card info-card">
        <div class="info-row">
          <span class="pill primary">Proxy</span>
          <code class="mono">{{ proxyBase }}</code>
        </div>
        <div class="muted" style="margin-top: 8px;">
          本文档仅包含服务器地址、API 协议与调用示例，可直接发给其他 Agent 完成对接，不含内部部署与源码细节。
        </div>
      </div>

      <div class="card doc-card">
        <pre class="doc-box">{{ markdown }}</pre>
      </div>
    </template>
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
.head-actions { display: flex; gap: 10px; flex-wrap: wrap; }

.loading-box { padding: 56px 0; text-align: center; }

.info-card { margin-bottom: 16px; animation: fadeInUp .45s var(--ease) backwards; }
.info-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.mono {
  font-family: var(--font-mono); font-size: 13px;
  background: var(--c-primary-bg);
  color: var(--c-primary);
  padding: 4px 10px;
  border-radius: 8px;
  word-break: break-all;
}
.muted { color: var(--c-text-muted); font-size: 13px; }

.doc-card { padding: 0; overflow: hidden; animation: fadeInUp .45s .08s var(--ease) backwards; }
.doc-box {
  background: #1d1d1f;
  border-radius: var(--radius-lg);
  padding: 22px 24px;
  margin: 0;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.75;
  color: rgba(235, 235, 245, .85);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 70vh;
  overflow: auto;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, .06);
}
.doc-box::-webkit-scrollbar-thumb { background: rgba(255, 255, 255, .2); }

@media (max-width: 768px) {
  .page-title { font-size: 22px; }
}
</style>
