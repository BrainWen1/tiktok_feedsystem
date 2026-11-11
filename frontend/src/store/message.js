import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useSseStore } from './sse'
import { useNotificationStore } from './notification'

// 后端 SSE 推送的 content 形如 "用户名: 正文"，与其他页面的展示保持一致
function stripSenderPrefix(content, senderName) {
  const prefix = `${senderName}: `
  return content.startsWith(prefix) ? content.slice(prefix.length) : content
}

// 私信域：只处理 SSE 的 message 事件，维护未读数和最新消息。
// 连接生命周期由 store/sse.js 统一管理，弹窗由 store/notification.js 统一渲染。
export const useMessageStore = defineStore('message', () => {
  // ------------------------- 全局状态 -------------------------
  const unreadCount = ref(0)
  const latestMessage = ref(null)
  // 私信页当前打开的是哪个用户；不在私信页时为 null
  const activeChatUserId = ref(null)
  const messagePageActive = ref(false)

  let messageSeq = 0

  const sse = useSseStore()
  const notificationStore = useNotificationStore()
  sse.on('message', handleMessage)

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

  function reset() {
    unreadCount.value = 0
    latestMessage.value = null
    activeChatUserId.value = null
    messagePageActive.value = false
  }

  // ------------------------- 消息处理 -------------------------

  function handleMessage(data) {
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
    notificationStore.notify({
      title: '收到新私信',
      message: `${senderName}：${content}`,
      type: 'info'
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
    activeChatUserId,
    messagePageActive,
    reset,
    clearUnread,
    reduceUnread,
    setMessagePageActive,
    setActiveChat
  }
})
