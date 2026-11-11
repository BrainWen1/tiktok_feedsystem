<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import {
  Bell,
  ChatDotRound,
  Compass,
  HomeFilled,
  Plus,
  Search,
  Star,
  User
} from '@element-plus/icons-vue'
import request from '../utils/request'
import { resolveAvatar } from '../utils/video'
import { useMessageStore } from '../store/message'
import { useNotificationStore } from '../store/notification'
import { useSseStore } from '../store/sse'

const route = useRoute()
const router = useRouter()
const messageStore = useMessageStore()
const notificationStore = useNotificationStore()
const sseStore = useSseStore()

const { unreadCount: messageUnreadCount } = storeToRefs(messageStore)
const { unreadCount: notificationUnreadCount } = storeToRefs(notificationStore)

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

const myUserId =
  Number(localStorage.getItem('user_id')) || decodeUserId(localStorage.getItem('token'))
const userName = localStorage.getItem('user_name') || '用户'
const initial = userName.slice(0, 1).toUpperCase()
const myAvatar = ref('')
const myAvatarSrc = computed(() => resolveAvatar(myAvatar.value))

// 推荐流 / 关注流使用沉浸式布局：导航栏与侧边栏悬浮在视频之上
const isImmersive = computed(() => Boolean(route.meta.immersive))

const searchKeyword = ref('')

const myProfilePath = computed(() => (myUserId ? `/user/${myUserId}` : '/login'))
const menuItems = computed(() => [
  { to: '/', label: '推荐', icon: Compass, active: route.path === '/' },
  { to: '/following', label: '关注', icon: Star, active: route.path === '/following' },
  {
    to: myProfilePath.value,
    label: '我的',
    icon: User,
    active: myUserId > 0 && route.path === myProfilePath.value
  }
])

function submitSearch() {
  const keyword = searchKeyword.value.trim()
  if (!keyword) return
  router.push({ path: '/search', query: { q: keyword } })
}

// 中文输入法选词时的回车不触发搜索
function onSearchEnter(event) {
  if (event.isComposing) return
  submitSearch()
}

function goMessage() {
  router.push('/message')
}

function goNotification() {
  router.push('/notification')
}

function goMyProfile() {
  if (!myUserId) return
  router.push(myProfilePath.value)
}

function handleLogout() {
  sseStore.stop()
  messageStore.reset()
  notificationStore.reset()
  localStorage.removeItem('token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('user_id')
  localStorage.removeItem('user_name')
  router.replace('/login')
}

function onUserCommand(command) {
  if (command === 'profile') goMyProfile()
  else if (command === 'publish') router.push('/publish')
  else if (command === 'logout') handleLogout()
}

async function loadMyProfile() {
  if (!myUserId) return
  try {
    const res = await request.get('/user/profile', { silent: true })
    myAvatar.value = res.data?.avatar_url || ''
  } catch {
    // 头像加载失败不影响导航栏使用，回退默认头像
  }
}

// 从搜索结果页回到其它页面时，搜索框保留上次关键词；进入搜索页则与 URL 同步
watch(
  () => route.query.q,
  (q) => {
    if (route.path === '/search') {
      searchKeyword.value = typeof q === 'string' ? q : ''
    }
  },
  { immediate: true }
)

onMounted(loadMyProfile)
</script>

<template>
  <div class="layout" :class="{ 'layout--immersive': isImmersive }">
    <header class="topbar" :class="{ 'topbar--dark': isImmersive }">
      <router-link to="/" class="home-btn" title="返回推荐流">
        <el-icon :size="22"><HomeFilled /></el-icon>
      </router-link>

      <div class="search-wrap">
        <div class="search-box">
          <el-icon class="search-icon"><Search /></el-icon>
          <input
            v-model="searchKeyword"
            class="search-input"
            type="text"
            placeholder="搜索用户/视频"
            maxlength="64"
            @keyup.enter="onSearchEnter"
          />
          <button class="search-btn" type="button" @click="submitSearch">搜索</button>
        </div>
      </div>

      <div class="topbar-actions">
        <el-badge
          :value="notificationUnreadCount"
          :max="99"
          :hidden="notificationUnreadCount === 0"
        >
          <button class="nav-action" type="button" title="通知中心" @click="goNotification">
            <el-icon :size="20"><Bell /></el-icon>
            <span>通知</span>
          </button>
        </el-badge>

        <el-badge :value="messageUnreadCount" :max="99" :hidden="messageUnreadCount === 0">
          <button class="nav-action" type="button" title="私信" @click="goMessage">
            <el-icon :size="20"><ChatDotRound /></el-icon>
            <span>私信</span>
          </button>
        </el-badge>

        <el-dropdown trigger="click" @command="onUserCommand">
          <button class="avatar-btn" type="button" title="账号菜单">
            <el-avatar :size="34" :src="myAvatarSrc">{{ initial }}</el-avatar>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="profile">我的主页</el-dropdown-item>
              <el-dropdown-item command="publish">发布视频</el-dropdown-item>
              <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </header>

    <aside class="sidebar" :class="{ 'sidebar--dark': isImmersive }">
      <nav class="side-nav">
        <router-link
          v-for="item in menuItems"
          :key="item.label"
          :to="item.to"
          class="side-item"
          :class="{ active: item.active }"
        >
          <el-icon :size="20"><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </router-link>
      </nav>

      <router-link to="/publish" class="publish-entry">
        <el-icon :size="18"><Plus /></el-icon>
        <span>发布视频</span>
      </router-link>
    </aside>

    <main class="layout-main" :class="{ 'layout-main--immersive': isImmersive }">
      <router-view :key="route.path" />
    </main>
  </div>
</template>

<style scoped>
.layout {
  min-height: 100vh;
  background: #f5f5f5;
}

/* ---------- 顶部导航 ---------- */
.topbar {
  position: fixed;
  top: 0;
  right: 0;
  left: 0;
  z-index: 300;
  display: flex;
  align-items: center;
  gap: 16px;
  height: 56px;
  padding: 0 20px;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
  transition: background 0.2s, border-color 0.2s;
}

.topbar--dark {
  background: rgba(16, 16, 20, 0.72);
  border-bottom-color: rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(10px);
}

.home-btn {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  color: #303133;
  text-decoration: none;
  background: #f2f3f5;
  border-radius: 50%;
  transition: background 0.15s, color 0.15s;
}

.home-btn:hover {
  background: #e6e8eb;
}

.topbar--dark .home-btn {
  color: #fff;
  background: rgba(255, 255, 255, 0.12);
}

.topbar--dark .home-btn:hover {
  background: rgba(255, 255, 255, 0.2);
}

.search-wrap {
  display: flex;
  flex: 1;
  justify-content: center;
  min-width: 0;
}

.search-box {
  display: flex;
  align-items: center;
  width: min(460px, 100%);
  height: 38px;
  padding: 0 4px 0 14px;
  background: #f2f3f5;
  border: 1px solid transparent;
  border-radius: 19px;
  transition: background 0.15s, border-color 0.15s;
}

.search-box:focus-within {
  background: #fff;
  border-color: #fe2c55;
}

.topbar--dark .search-box {
  background: rgba(255, 255, 255, 0.1);
}

.topbar--dark .search-box:focus-within {
  background: rgba(0, 0, 0, 0.45);
  border-color: rgba(254, 44, 85, 0.8);
}

.search-icon {
  flex: none;
  color: #909399;
}

.search-input {
  flex: 1;
  min-width: 0;
  padding: 0 10px;
  color: #303133;
  font-size: 14px;
  background: transparent;
  border: none;
  outline: none;
}

.search-input::placeholder {
  color: #a8abb2;
}

.topbar--dark .search-input {
  color: #fff;
}

.search-btn {
  flex: none;
  height: 30px;
  padding: 0 16px;
  color: #fff;
  font-size: 13px;
  background: #fe2c55;
  border: none;
  border-radius: 15px;
  cursor: pointer;
}

.search-btn:hover {
  background: #e6254b;
}

.topbar-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 18px;
}

.nav-action {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 6px 4px;
  color: #4b4f56;
  font-size: 13px;
  background: none;
  border: none;
  cursor: pointer;
}

.nav-action:hover {
  color: #fe2c55;
}

.topbar--dark .nav-action {
  color: rgba(255, 255, 255, 0.88);
}

.topbar--dark .nav-action:hover {
  color: #fff;
}

.avatar-btn {
  display: flex;
  padding: 0;
  background: none;
  border: 2px solid transparent;
  border-radius: 50%;
  cursor: pointer;
}

.avatar-btn:hover {
  border-color: #fe2c55;
}

/* ---------- 左侧边栏 ---------- */
.sidebar {
  position: fixed;
  top: 56px;
  bottom: 0;
  left: 0;
  z-index: 250;
  display: flex;
  flex-direction: column;
  width: 220px;
  padding: 18px 14px;
  background: #fff;
  border-right: 1px solid #e4e7ed;
  transition: background 0.2s, border-color 0.2s;
}

.sidebar--dark {
  background: rgba(16, 16, 20, 0.62);
  border-right-color: rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(10px);
}

.side-nav {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.side-item {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 46px;
  padding: 0 14px;
  color: #4b4f56;
  font-size: 15px;
  text-decoration: none;
  border-radius: 10px;
  transition: background 0.15s, color 0.15s;
}

.side-item:hover {
  background: #f2f3f5;
}

.side-item.active {
  color: #fe2c55;
  font-weight: 600;
  background: #fff0f3;
}

.sidebar--dark .side-item {
  color: rgba(255, 255, 255, 0.82);
}

.sidebar--dark .side-item:hover {
  background: rgba(255, 255, 255, 0.1);
}

.sidebar--dark .side-item.active {
  color: #fff;
  background: rgba(254, 44, 85, 0.5);
}

.publish-entry {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  height: 44px;
  margin-top: auto;
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
  background: #fe2c55;
  border-radius: 10px;
  box-shadow: 0 6px 16px rgba(254, 44, 85, 0.28);
}

.publish-entry:hover {
  background: #e6254b;
}

/* ---------- 主内容区 ---------- */
.layout-main {
  min-height: 100vh;
  padding-top: 56px;
  margin-left: 220px;
}

.layout-main--immersive {
  height: 100vh;
  height: 100dvh;
  padding-top: 0;
  overflow: hidden;
}

@media (max-width: 900px) {
  .sidebar {
    width: 72px;
    padding: 14px 10px;
  }

  .side-item {
    justify-content: center;
    padding: 0;
  }

  .side-item span,
  .publish-entry span {
    display: none;
  }

  .layout-main {
    margin-left: 72px;
  }

  .nav-action span {
    display: none;
  }

  .topbar {
    padding: 0 12px;
  }
}
</style>
