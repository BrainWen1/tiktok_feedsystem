<script setup>
import { onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useMessageStore } from './store/message'

const route = useRoute()
const messageStore = useMessageStore()

// 登录后在任意页面都保持 SSE 长连接；退出登录 / 回到登录页时断开并清理状态
function syncConnection() {
  const loggedIn = Boolean(localStorage.getItem('token'))

  if (!loggedIn) {
    messageStore.reset()
    return
  }
  if (route.path === '/login') {
    messageStore.stop()
    return
  }
  messageStore.start()
}

onMounted(syncConnection)
watch(() => route.fullPath, syncConnection)
onUnmounted(() => messageStore.stop())
</script>

<template>
  <router-view />
</template>
