<script setup>
import { onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useSseStore } from './store/sse'
import { useMessageStore } from './store/message'
import { useNotificationStore } from './store/notification'

const route = useRoute()
const sseStore = useSseStore()
// 两个业务 store 在 App 中实例化，确保 SSE 事件处理器在任意页面都已注册
const messageStore = useMessageStore()
const notificationStore = useNotificationStore()

// 登录后在任意页面都保持 SSE 长连接；退出登录 / 回到登录页时断开并清理状态
function syncConnection() {
  const loggedIn = Boolean(localStorage.getItem('token'))

  if (!loggedIn) {
    sseStore.stop()
    messageStore.reset()
    notificationStore.reset()
    return
  }
  if (route.path === '/login') {
    sseStore.stop()
    return
  }
  sseStore.start()
  // SSE 只会推送后续的增量通知，导航栏红点需要通过接口初始化一次
  notificationStore.refreshUnreadCount()
}

onMounted(syncConnection)
watch(() => route.fullPath, syncConnection)
onUnmounted(() => sseStore.stop())
</script>

<template>
  <router-view />
</template>
