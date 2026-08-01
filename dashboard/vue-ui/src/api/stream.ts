// SSE 实时监控流：替代各页面 setInterval 轮询。
// 后端 /api/stream/monitor 有变化才推送；EventSource 自带断线重连。
import { useAppStore } from '@/stores/app'

export interface MonitorFrame {
  status: Record<string, any>
  accounts: Record<string, any>
  logs: Record<string, any>
  pool: Record<string, any>
}

export function connectMonitor(onFrame: (f: MonitorFrame) => void, onError?: () => void): () => void {
  const store = useAppStore()
  const url = `/api/stream/monitor?token=${encodeURIComponent(store.adminToken)}`
  const es = new EventSource(url)

  es.onmessage = (ev) => {
    try {
      onFrame(JSON.parse(ev.data) as MonitorFrame)
    } catch { /* 忽略坏帧 */ }
  }
  es.onerror = () => { onError?.() } // EventSource 会自动重连

  return () => es.close()
}
