<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../utils/request'
import { resolveAvatar, normalizeVideoDetail } from '../utils/video'
import VideoCard from '../components/VideoCard.vue'

const PAGE_SIZE = 10

const route = useRoute()
const router = useRouter()

// 当前登录用户 ID：优先取登录时写入的 user_id，旧会话回退解析 JWT
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

const userId = computed(() => Number(route.params.id) || 0)
const isSelf = computed(() => myUserId > 0 && userId.value === myUserId)
const profile = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const counts = reactive({ follow: 0, fans: 0 })
const activeTab = ref('works')

// 关注关系与交互状态
const isFollow = ref(false)
const followLoading = ref(false)

function isLoggedIn() {
  return Boolean(localStorage.getItem('token'))
}

// 未登录时统一拦截并引导登录，保留当前页面作为登录后的回跳地址
function requireLogin() {
  if (isLoggedIn()) return true
  ElMessage.warning('请先登录')
  router.push({ path: '/login', query: { redirect: route.fullPath } })
  return false
}

function createTabState() {
  return {
    items: [],
    cursor: null,
    loading: false,
    loadingMore: false,
    loaded: false,
    done: false,
    error: ''
  }
}

const tabs = {
  works: reactive(createTabState()),
  liked: reactive(createTabState())
}
const currentTab = computed(() => tabs[activeTab.value])

const avatarSrc = computed(() => resolveAvatar(profile.value?.avatar_url))
const initial = computed(() => (profile.value?.user_name || '?').slice(0, 1).toUpperCase())

function errorText(error, fallback) {
  return error?.response?.data?.message || error?.message || fallback
}

async function fetchWorks(cursor) {
  const params = { author_id: userId.value, page_size: PAGE_SIZE }
  if (cursor?.last_vid) params['cursor.last_vid'] = cursor.last_vid
  if (cursor?.last_created_at) params['cursor.last_created_at'] = cursor.last_created_at

  const res = await request.get('/video/list', { params })
  const data = res.data || {}
  return {
    list: (data.videos || []).map(normalizeVideoDetail).filter(Boolean),
    cursor: data.next_cursor || null
  }
}

async function fetchLiked(cursor) {
  const body = { user_id: userId.value, page_size: PAGE_SIZE }
  if (cursor?.last_vid) {
    body.cursor = {
      last_vid: cursor.last_vid,
      last_created_at: cursor.last_created_at
    }
  }

  const res = await request.post('/like/list_liked', body)
  const data = res.data || {}
  return {
    list: (data.videos || []).map(normalizeVideoDetail).filter(Boolean),
    cursor: data.next_cursor || null
  }
}

async function loadTab(key) {
  const state = tabs[key]
  if (state.loading) return

  state.loading = true
  state.error = ''
  try {
    const { list, cursor } = key === 'works' ? await fetchWorks(null) : await fetchLiked(null)
    state.items = list
    state.cursor = cursor
    state.done = !cursor
    state.loaded = true
  } catch (error) {
    state.error = errorText(error, '列表加载失败')
  } finally {
    state.loading = false
  }
}

async function loadMore(key) {
  const state = tabs[key]
  if (state.loading || state.loadingMore || !state.cursor) return

  state.loadingMore = true
  state.error = ''
  try {
    const { list, cursor } =
      key === 'works' ? await fetchWorks(state.cursor) : await fetchLiked(state.cursor)
    state.items = state.items.concat(list)
    state.cursor = cursor
    state.done = !cursor
  } catch (error) {
    state.error = errorText(error, '加载更多失败')
  } finally {
    state.loadingMore = false
  }
}

function resetTabs() {
  for (const key of Object.keys(tabs)) {
    Object.assign(tabs[key], createTabState())
  }
}

async function loadProfile() {
  if (!userId.value) {
    errorMsg.value = '用户 ID 不合法'
    profile.value = null
    return
  }

  loading.value = true
  errorMsg.value = ''
  profile.value = null
  counts.follow = 0
  counts.fans = 0
  isFollow.value = false
  resetTabs()

  try {
    // 资料、统计与关注关系互不阻塞：任一失败时其余部分仍可展示
    const tasks = [
      request.post('/user/find_by_id', { user_id: userId.value }),
      request.get('/social/count', { params: { target_id: userId.value } })
    ]
    // 自己的主页不展示关注按钮，也无需查询关注关系
    if (!isSelf.value && isLoggedIn()) {
      tasks.push(request.get('/social/is_follow', { params: { target_id: userId.value } }))
    }

    const [profileResult, countResult, followResult] = await Promise.allSettled(tasks)

    if (profileResult.status === 'fulfilled') {
      profile.value = profileResult.value.data || null
    } else {
      errorMsg.value = errorText(profileResult.reason, '用户信息加载失败')
    }

    if (countResult.status === 'fulfilled') {
      const data = countResult.value.data || {}
      counts.follow = Number(data.follow_count) || 0
      counts.fans = Number(data.fan_count) || 0
    }

    if (followResult?.status === 'fulfilled') {
      isFollow.value = Boolean(followResult.value.data?.is_follow)
    }

    if (profile.value) await loadTab(activeTab.value)
  } finally {
    loading.value = false
  }
}

// 关注 / 取关：成功后同步按钮状态与粉丝数，请求期间禁用按钮防止重复提交
async function toggleFollow() {
  if (followLoading.value || isSelf.value || !requireLogin()) return

  const targetId = userId.value
  followLoading.value = true
  try {
    if (isFollow.value) {
      await request.post('/social/unfollow', { blogger_id: targetId })
      if (targetId !== userId.value) return
      isFollow.value = false
      counts.fans = Math.max(0, counts.fans - 1)
      ElMessage.success('已取消关注')
    } else {
      await request.post('/social/follow', { blogger_id: targetId })
      if (targetId !== userId.value) return
      isFollow.value = true
      counts.fans += 1
      ElMessage.success('关注成功')
    }
  } catch {
    // 错误提示由 request 拦截器统一处理
  } finally {
    followLoading.value = false
  }
}

// 跳转私信页，携带目标用户 ID 供私信页自动打开会话
function goMessage() {
  if (isSelf.value || !requireLogin()) return
  router.push({ path: '/message', query: { target_id: String(userId.value) } })
}

// Tab 首次切换时再加载，避免一次性请求两个列表
watch(activeTab, (key) => {
  const state = tabs[key]
  if (profile.value && !state.loaded && !state.loading) loadTab(key)
})

// 从别人的主页跳到另一个主页时组件会被复用，需要按新的 id 重新加载
watch(userId, loadProfile)

// 向下滚动自动加载下一页，同时保留“加载更多”按钮作为兜底
function onWindowScroll() {
  const state = tabs[activeTab.value]
  if (loading.value || !state.loaded || state.done || state.loading || state.loadingMore) return

  const reachedBottom =
    window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 240
  if (reachedBottom) loadMore(activeTab.value)
}

// 卡片内点赞后同步各 Tab 中同一视频的状态
function onLikeChange({ vid, isLiked, likeCount }) {
  for (const key of Object.keys(tabs)) {
    const item = tabs[key].items.find((video) => String(video.vid) === String(vid))
    if (item) {
      item.isLiked = isLiked
      item.likeCount = likeCount
    }
  }
}

onMounted(() => {
  loadProfile()
  window.addEventListener('scroll', onWindowScroll, { passive: true })
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onWindowScroll)
})
</script>

<template>
  <div class="profile-page">
    <main v-loading="loading" class="content">
      <el-alert
        v-if="errorMsg"
        class="error"
        type="error"
        show-icon
        :title="errorMsg"
        closable
        @close="errorMsg = ''"
      />

      <el-empty
        v-if="!loading && !profile && !errorMsg"
        description="用户不存在或已注销"
        :image-size="80"
      />

      <template v-if="profile">
        <section class="profile-card">
          <el-avatar class="avatar" :size="88" :src="avatarSrc">
            {{ initial }}
          </el-avatar>

          <div class="profile-info">
            <h2 class="profile-name">{{ profile.user_name }}</h2>
            <p class="bio">{{ profile.bio || '这个人很懒，还没有填写简介' }}</p>
            <div class="stats">
              <span class="stat"><strong>{{ counts.follow }}</strong> 关注</span>
              <span class="stat"><strong>{{ counts.fans }}</strong> 粉丝</span>
            </div>
          </div>

          <!-- 自己的主页不展示关注/私信入口 -->
          <div v-if="!isSelf" class="profile-actions">
            <el-button
              :type="isFollow ? 'default' : 'primary'"
              :loading="followLoading"
              @click="toggleFollow"
            >
              {{ isFollow ? '已关注' : '关注' }}
            </el-button>
            <el-button @click="goMessage">私信</el-button>
          </div>
        </section>

        <el-tabs v-model="activeTab" class="profile-tabs">
          <el-tab-pane label="作品" name="works" />
          <el-tab-pane label="喜欢" name="liked" />
        </el-tabs>

        <div v-loading="currentTab.loading" class="tab-panel">
          <el-alert
            v-if="currentTab.error"
            class="error"
            type="error"
            show-icon
            :title="currentTab.error"
            closable
            @close="currentTab.error = ''"
          />

          <el-empty
            v-if="currentTab.loaded && !currentTab.loading && currentTab.items.length === 0"
            :description="activeTab === 'works' ? 'TA 还没有发布作品' : 'TA 还没有喜欢的视频'"
            :image-size="80"
          />

          <div v-else class="video-grid">
            <VideoCard
              v-for="video in currentTab.items"
              :key="video.vid"
              :video="video"
              @like-change="onLikeChange"
            />
          </div>

          <div v-if="currentTab.items.length" class="load-more">
            <el-button
              v-if="!currentTab.done"
              :loading="currentTab.loadingMore"
              @click="loadMore(activeTab)"
            >
              加载更多
            </el-button>
            <span v-else class="no-more">没有更多了</span>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>

<style scoped>
.profile-page {
  min-height: 100%;
  display: flex;
  flex-direction: column;
}

.content {
  flex: 1;
  width: 100%;
  max-width: 1200px;
  margin: 0 auto;
  padding: 24px;
}

.error {
  margin-bottom: 16px;
}

.profile-card {
  display: flex;
  align-items: flex-start;
  gap: 24px;
  padding: 24px;
  background: var(--app-surface);
  border-radius: 8px;
  transition: background-color 0.25s ease;
}

.avatar {
  flex: none;
  color: #fff;
  background: var(--app-info);
}

.profile-info {
  flex: 1;
  min-width: 0;
}

.profile-actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 12px;
  align-self: center;
}

.profile-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}

.profile-name {
  margin: 0;
  color: var(--app-text);
  font-size: 22px;
  line-height: 1.2;
}

.bio {
  margin: 8px 0 0;
  color: var(--app-text-muted);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.stats {
  display: flex;
  gap: 20px;
  margin-top: 12px;
  color: var(--app-text-secondary);
  font-size: 14px;
}

.stat strong {
  margin-right: 2px;
  color: var(--app-text);
  font-size: 16px;
}

.profile-tabs {
  margin-top: 20px;
  padding: 0 16px;
  background: var(--app-surface);
  border-radius: 8px;
  transition: background-color 0.25s ease;
}

.tab-panel {
  min-height: 200px;
  padding-top: 16px;
}

.video-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 20px;
}

.load-more {
  padding: 24px 0 8px;
  text-align: center;
}

.no-more {
  color: var(--app-text-faint);
  font-size: 13px;
}
</style>
