import { defineStore } from 'pinia'
import { ref } from 'vue'
import request from '../utils/request'

// SSE 连接管理器：只负责建立/重连长连接，并按事件的 type 分发给订阅方。
// 后端一个 /sse 长连接同时推送私信（type=message）和互动通知（type=notification），
// 连接本身是全局唯一的，具体业务处理放在 message / notification 两个 store 里。
export const useSseStore = defineStore('sse', () => {
  // connecting | connected | disconnected
  const status = ref('disconnected')

  // type -> Set<handler>
  const listeners = new Map()

  let eventSource = null
  let reconnectTimer = null
  let connecting = false
  let stopped = true
  let generation = 0

  // 订阅某一类事件，返回取消订阅函数
  function on(type, handler) {
    if (!listeners.has(type)) listeners.set(type, new Set())
    listeners.get(type).add(handler)
    return () => listeners.get(type)?.delete(handler)
  }

  function dispatch(data) {
    const handlers = listeners.get(data.type)
    if (!handlers) return
    handlers.forEach((handler) => {
      try {
        handler(data)
      } catch (error) {
        console.error(`[sse] handler for "${data.type}" failed`, error)
      }
    })
  }

  // 登录后由 App.vue 调用；重复调用是安全的
  function start() {
    if (!stopped || connecting) return
    stopped = false
    connect()
  }

  function stop() {
    stopped = true
    connecting = false
    // 让还在途中的 ticket 请求失效，避免 stop 后又建出一条连接
    generation += 1
    clearReconnectTimer()
    closeEventSource()
    status.value = 'disconnected'
  }

  async function connect() {
    if (stopped || connecting) return
    const currentGeneration = ++generation
    connecting = true
    status.value = 'connecting'

    try {
      // 自动重连期间不弹全局错误提示，连接状态由私信页的指示灯展示
      const res = await request.post('/sse/ticket', null, { silent: true })
      const ticket = res.data?.ticket
      if (!ticket) throw new Error('未获取到 SSE ticket')
      if (stopped || currentGeneration !== generation) return

      eventSource = new EventSource(`/sse?ticket=${encodeURIComponent(ticket)}`)
      eventSource.onopen = () => {
        status.value = 'connected'
      }
      eventSource.onmessage = (event) => {
        let data
        try {
          data = JSON.parse(event.data)
        } catch {
          return
        }
        dispatch(data)
      }
      eventSource.onerror = () => {
        if (stopped) return
        // ticket 是一次性的，EventSource 自动重连会带旧 ticket 失败，这里主动重建
        status.value = 'disconnected'
        closeEventSource()
        scheduleReconnect()
      }
    } catch {
      if (stopped || currentGeneration !== generation) return
      status.value = 'disconnected'
      scheduleReconnect()
    } finally {
      // 旧连接收尾时不要清掉新连接的 connecting 标记
      if (currentGeneration === generation) connecting = false
    }
  }

  function scheduleReconnect() {
    if (stopped || reconnectTimer) return
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      connect()
    }, 3000)
  }

  function clearReconnectTimer() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function closeEventSource() {
    if (eventSource) {
      eventSource.close()
      eventSource = null
    }
  }

  return { status, on, start, stop }
})
