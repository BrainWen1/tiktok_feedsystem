<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import request from '../utils/request'
import { resolveAvatar, normalizeVideoDetail } from '../utils/video'
import VideoCard from '../components/VideoCard.vue'

const PAGE_SIZE = 10

const route = useRoute()
const router = useRouter()

const userId = computed(() => Number(route.params.id) || 0)
const profile = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const counts = reactive({ follow: 0, fans: 0 })
const activeTab = ref('works')

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
  resetTabs()

  try {
    // 资料和统计互不阻塞：任一失败时其余部分仍可展示
    const [profileResult, countResult] = await Promise.allSettled([
      request.post('/user/find_by_id', { user_id: userId.value }),
      request.get('/social/count', { params: { target_id: userId.value } })
    ])

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

    if (profile.value) await loadTab(activeTab.value)
  } finally {
    loading.value = false
  }
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
    <header class="navbar">
      <div class="nav-left">
        <el-button link @click="router.push('/')">← 返回主页</el-button>
        <span class="brand">个人主页</span>
      </div>
    </header>

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

.navbar {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 56px;
  padding: 0 24px;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
}

.nav-left {
  display: flex;
  align-items: center;
  gap: 16px;
}

.brand {
  font-size: 18px;
  font-weight: 600;
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
  background: #fff;
  border-radius: 8px;
}

.avatar {
  flex: none;
  color: #fff;
  background: #409eff;
}

.profile-info {
  flex: 1;
  min-width: 0;
}

.profile-name {
  margin: 0;
  color: #303133;
  font-size: 22px;
  line-height: 1.2;
}

.bio {
  margin: 8px 0 0;
  color: #909399;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.stats {
  display: flex;
  gap: 20px;
  margin-top: 12px;
  color: #606266;
  font-size: 14px;
}

.stat strong {
  margin-right: 2px;
  color: #303133;
  font-size: 16px;
}

.profile-tabs {
  margin-top: 20px;
  padding: 0 16px;
  background: #fff;
  border-radius: 8px;
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
  color: #c0c4cc;
  font-size: 13px;
}
</style>
