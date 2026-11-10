import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ElNotification } from 'element-plus'
import request from '../utils/request'

// 后端 SSE 推送的 content 形如 "用户名: 正文"，与其他页面的展示保持一致
function stripSenderPrefix(content, senderName) {
  const prefix = `${senderName}: `
  return content.startsWith(prefix) ? content.slice(prefix.length) : content
}

export const useMessageStore = defineStore('message', () => {
  // ------------------------- 全局状态 -------------------------
  const unreadCount = ref(0)
  const latestMessage = ref(null)
  // connecting | connected | disconnected
  const sseStatus = ref('disconnected')
  // 私信页当前打开的是哪个用户；不在私信页时为 null
  const activeChatUserId = ref(null)
  const messagePageActive = ref(false)

  // ------------------------- SSE 内部状态 -------------------------
  let eventSource = null
  let reconnectTimer = null
  let connecting = false
  let stopped = true
  let generation = 0
  let messageSeq = 0

  function getMyUserId() {
    return (
      Number(localStorage.getItem('user_id')) ||
      decodeUserId(localStorage.getItem('token'))
    )
  }

  // 兼容旧会话：localStorage 里没有 user_id 时从 JWT 里取
  function decodeUserId(token) {
    try {
      const part = token.split('.')[1]
      const base64 = part.replace(/-/g, '+').replace(/_/g, '/')
      const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4)
      const bytes = Uint8Array.from(atob(padded), (c) => c.charCodeAt(0))
      const payload = JSON.parse(new TextDecoder().decode(bytes))
      return Number(payload.user_id) || 0
    } catch {
      return 0
    }
  }

  // ------------------------- 连接管理 -------------------------

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
    sseStatus.value = 'disconnected'
  }

  function reset() {
    stop()
    unreadCount.value = 0
    latestMessage.value = null
    activeChatUserId.value = null
    messagePageActive.value = false
  }

  async function connect() {
    if (stopped || connecting) return
    const currentGeneration = ++generation
    connecting = true
    sseStatus.value = 'connecting'

    try {
      // 自动重连期间不弹全局错误提示，连接状态由私信页的指示灯展示
      const res = await request.post('/sse/ticket', null, { silent: true })
      const ticket = res.data?.ticket
      if (!ticket) throw new Error('未获取到 SSE ticket')
      if (stopped || currentGeneration !== generation) return

      eventSource = new EventSource(`/sse?ticket=${encodeURIComponent(ticket)}`)
      eventSource.onopen = () => {
        sseStatus.value = 'connected'
      }
      eventSource.onmessage = handleSseMessage
      eventSource.onerror = () => {
        if (stopped) return
        // ticket 是一次性的，EventSource 自动重连会带旧 ticket 失败，这里主动重建
        sseStatus.value = 'disconnected'
        closeEventSource()
        scheduleReconnect()
      }
    } catch {
      if (stopped || currentGeneration !== generation) return
      sseStatus.value = 'disconnected'
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

  // ------------------------- 消息处理 -------------------------

  function handleSseMessage(event) {
    let data
    try {
      data = JSON.parse(event.data)
    } catch {
      return
    }
    if (data.type !== 'message') return

    const senderId = Number(data.sender_id)
    const senderName = data.sender_name || `用户${senderId}`
    const content = stripSenderPrefix(data.content || '', senderName)
    const createdTs = Number(data.created_at) || Math.floor(Date.now() / 1000)

    const message = {
      id: `sse-${Date.now()}-${++messageSeq}`,
      sender_id: senderId,
      receiver_id: getMyUserId(),
      sender_name: senderName,
      content,
      created_at: new Date(createdTs * 1000).toISOString(),
      created_ts: createdTs,
      is_read: false
    }

    // 私信页通过 watch latestMessage 把消息追加进当前聊天窗口
    latestMessage.value = message

    // 正在看这个人的聊天窗口时，消息即时可见，不算未读、不弹通知
    const viewingThisChat =
      messagePageActive.value && Number(activeChatUserId.value) === senderId
    if (viewingThisChat) return

    unreadCount.value += 1
    ElNotification({
      title: '收到新私信',
      message: `${senderName}：${content}`,
      type: 'info',
      position: 'top-right',
      duration: 4000
    })
  }

  // ------------------------- 对外操作 -------------------------

  function clearUnread() {
    unreadCount.value = 0
  }

  function reduceUnread(count) {
    const amount = Number(count) || 0
    if (amount <= 0) return
    unreadCount.value = Math.max(0, unreadCount.value - amount)
  }

  function setMessagePageActive(active) {
    messagePageActive.value = Boolean(active)
    if (!active) activeChatUserId.value = null
  }

  function setActiveChat(userId) {
    activeChatUserId.value = userId === null || userId === undefined ? null : Number(userId)
  }

  return {
    unreadCount,
    latestMessage,
    sseStatus,
    activeChatUserId,
    messagePageActive,
    start,
    stop,
    reset,
    clearUnread,
    reduceUnread,
    setMessagePageActive,
    setActiveChat
  }
})
