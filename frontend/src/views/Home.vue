<script setup>
import { useRouter } from 'vue-router'

const router = useRouter()
const userName = localStorage.getItem('user_name') || '用户'

// MVP 阶段仅清理本地登录态；后端 logout 接口后续接入
function handleLogout() {
  localStorage.removeItem('token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('user_name')
  router.replace('/login')
}
</script>

<template>
  <div class="home-page">
    <header class="navbar">
      <span class="brand">TikTok Feed 演示</span>
      <span class="user">
        {{ userName }}
        <el-button size="small" @click="handleLogout">退出登录</el-button>
      </span>
    </header>

    <main class="content">
      <el-empty description="登录成功！Feed 流将在下一步实现" />
    </main>
  </div>
</template>

<style scoped>
.home-page {
  min-height: 100%;
  display: flex;
  flex-direction: column;
}

.navbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 56px;
  padding: 0 24px;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
}

.brand {
  font-size: 18px;
  font-weight: 600;
}

.user {
  display: flex;
  align-items: center;
  gap: 12px;
  color: #606266;
}

.content {
  flex: 1;
  padding: 40px 24px;
}
</style>
