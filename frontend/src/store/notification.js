import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ElNotification } from 'element-plus'
import request from '../utils/request'
import { useSseStore } from './sse'

// 后端 SSE 的 notification 事件只带一段拼好的中文文案，这里按关键字区分类型。
// 文案示例：用户 xxx 点赞了你的视频 / 用户 xxx 评论了你: yyy / 用户 xxx 关注了你
const NOTIFICATION_META = [
  { keyword: '点赞', title: '收到新点赞', type: 'success' },
  { keyword: '评论', title: '收到新评论', type: 'info' },
  { keyword: '关注', title: '新增关注', type: 'success' }
]

function getNotificationMeta(content) {
  return (
    NOTIFICATION_META.find((item) => content.includes(item.keyword)) || {
      title: '新通知',
      type: 'info'
    }
  )
}

export const useNotificationStore = defineStore('notification', () => {
  // 未读通知总数：SSE 推送会带最新值，页面初始进入时再拉一次接口校准
  const unreadCount = ref(0)
  // 本次登录是否已经从接口拉过未读数，避免每次路由切换都重复请求
  const countLoaded = ref(false)

  const sse = useSseStore()
  sse.on('notification', handleNotification)

  // 登录后由 App.vue 调用一次：用接口结果初始化导航栏红点
  async function refreshUnreadCount() {
    if (countLoaded.value) return
    countLoaded.value = true
    try {
      const res = await request.get('/notification/unread_count', { silent: true })
      setUnreadCount(res.data?.count)
    } catch {
      // 拉取失败允许下次重试
      countLoaded.value = false
    }
  }

  // 本地直接覆盖未读数（标记已读、一键已读后同步红点）
  function setUnreadCount(count) {
    unreadCount.value = Math.max(Number(count) || 0, 0)
  }

  function decreaseUnread(count = 1) {
    setUnreadCount(unreadCount.value - count)
  }

  // 统一的全站弹窗出口：私信提醒和互动通知都从这里弹出，任何页面都能看到
  function notify({ title, message, type = 'info', duration = 4000 }) {
    ElNotification({
      title,
      message,
      type,
      position: 'top-right',
      duration
    })
  }

  // 点赞、评论、关注通知：后端 content 已拼好完整文案，直接弹出即可
  function handleNotification(data) {
    const content = data.content || ''
    if (!content) return

    if (typeof data.unread_count === 'number') {
      unreadCount.value = data.unread_count
    }

    const { title, type } = getNotificationMeta(content)
    notify({ title, message: content, type })
  }

  function reset() {
    unreadCount.value = 0
    countLoaded.value = false
  }

  return {
    unreadCount,
    notify,
    refreshUnreadCount,
    setUnreadCount,
    decreaseUnread,
    reset
  }
})
