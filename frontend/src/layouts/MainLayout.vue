<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import {
  Bell,
  ChatDotRound,
  Compass,
  HomeFilled,
  Moon,
  Plus,
  Search,
  Star,
  Sunny,
  User
} from '@element-plus/icons-vue'
import request from '../utils/request'
import { resolveAvatar } from '../utils/video'
import { isDark, toggleTheme } from '../utils/theme'
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
    <header class="topbar" :class="{ 'topbar--immersive': isImmersive }">
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

        <button
          class="nav-action theme-toggle"
          type="button"
          :title="isDark ? '切换到浅色模式' : '切换到深色模式'"
          :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'"
          @click="toggleTheme"
        >
          <el-icon :size="20">
            <Sunny v-if="isDark" />
            <Moon v-else />
          </el-icon>
        </button>

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

    <aside class="sidebar" :class="{ 'sidebar--immersive': isImmersive }">
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
  color: var(--app-text);
  background: var(--app-bg);
  transition: background-color 0.25s ease, color 0.25s ease;
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
  background: var(--app-chrome-bg);
  border-bottom: 1px solid var(--app-chrome-border);
  transition: background-color 0.25s ease, border-color 0.25s ease, color 0.25s ease;
}

/* 沉浸式页面：导航悬浮在视频上，背景使用主题相关的玻璃效果 */
.topbar--immersive {
  background: var(--app-immersive-chrome-bg);
  border-bottom-color: var(--app-immersive-chrome-border);
  backdrop-filter: blur(10px);
}

.home-btn {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  color: var(--app-text);
  text-decoration: none;
  background: var(--app-surface-input);
  border-radius: 50%;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.home-btn:hover {
  background: var(--app-surface-input-hover);
}

.topbar--immersive .home-btn {
  color: var(--app-immersive-text);
  background: var(--app-immersive-input-bg);
}

.topbar--immersive .home-btn:hover {
  background: var(--app-immersive-hover-bg);
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
  background: var(--app-surface-input);
  border: 1px solid transparent;
  border-radius: 19px;
  transition: background-color 0.2s ease, border-color 0.2s ease;
}

.search-box:focus-within {
  background: var(--app-surface);
  border-color: var(--app-primary);
}

.topbar--immersive .search-box {
  background: var(--app-immersive-input-bg);
}

.topbar--immersive .search-box:focus-within {
  background: var(--app-immersive-input-focus-bg);
  border-color: var(--app-primary);
}

.search-icon {
  flex: none;
  color: var(--app-text-muted);
}

.search-input {
  flex: 1;
  min-width: 0;
  padding: 0 10px;
  color: var(--app-text);
  font-size: 14px;
  background: transparent;
  border: none;
  outline: none;
}

.search-input::placeholder {
  color: var(--app-text-placeholder);
}

.topbar--immersive .search-input {
  color: var(--app-immersive-input-text);
}

.topbar--immersive .search-input::placeholder {
  color: var(--app-immersive-text);
  opacity: 0.55;
}

.search-btn {
  flex: none;
  height: 30px;
  padding: 0 16px;
  color: #fff;
  font-size: 13px;
  background: var(--app-primary);
  border: none;
  border-radius: 15px;
  cursor: pointer;
  transition: background-color 0.2s ease;
}

.search-btn:hover {
  background: var(--app-primary-hover);
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
  color: var(--app-text-secondary);
  font-size: 13px;
  background: none;
  border: none;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.nav-action:hover {
  color: var(--app-primary);
}

.topbar--immersive .nav-action {
  color: var(--app-immersive-text);
}

.topbar--immersive .nav-action:hover {
  color: var(--app-immersive-text-hover);
}

/* 明暗主题切换按钮，与通知/私信一起放在头像左侧 */
.theme-toggle {
  justify-content: center;
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: 50%;
}

.theme-toggle:hover {
  background: var(--app-surface-input);
}

.topbar--immersive .theme-toggle:hover {
  background: var(--app-immersive-hover-bg);
}

.theme-toggle .el-icon {
  transition: transform 0.3s ease;
}

.theme-toggle:hover .el-icon {
  transform: rotate(22deg);
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
  border-color: var(--app-primary);
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
  background: var(--app-chrome-bg);
  border-right: 1px solid var(--app-chrome-border);
  transition: background-color 0.25s ease, border-color 0.25s ease, color 0.25s ease;
}

.sidebar--immersive {
  background: var(--app-immersive-chrome-bg);
  border-right-color: var(--app-immersive-chrome-border);
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
  color: var(--app-text-secondary);
  font-size: 15px;
  text-decoration: none;
  border-radius: 10px;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.side-item:hover {
  color: var(--app-text);
  background: var(--app-surface-hover);
}

.side-item.active {
  color: var(--app-primary);
  font-weight: 600;
  background: var(--app-primary-soft);
}

.sidebar--immersive .side-item {
  color: var(--app-immersive-text);
}

.sidebar--immersive .side-item:hover {
  color: var(--app-immersive-text-hover);
  background: var(--app-immersive-hover-bg);
}

.sidebar--immersive .side-item.active {
  color: var(--app-immersive-active-text);
  background: var(--app-immersive-active-bg);
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
  background: var(--app-primary);
  border-radius: 10px;
  box-shadow: 0 6px 16px rgba(254, 44, 85, 0.28);
  transition: background-color 0.2s ease;
}

.publish-entry:hover {
  background: var(--app-primary-hover);
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
