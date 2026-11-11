import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ElNotification } from 'element-plus'
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
  // 后端随 SSE 下发的通知未读数；预留，后续可接到导航栏徽标
  const unreadCount = ref(0)

  const sse = useSseStore()
  sse.on('notification', handleNotification)

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
  }

  return { unreadCount, notify, reset }
})
