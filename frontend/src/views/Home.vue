<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../utils/request'

// 后端返回的封面/视频地址是相对路径（如 /upload/covers/...），
// 浏览器需要直连后端资源域名，否则会请求 Vite 服务器导致 404。
const ASSET_BASE = 'http://127.0.0.1:8080'
const PAGE_SIZE = 10

const router = useRouter()
const userName = localStorage.getItem('user_name') || '用户'

const videos = ref([])
const nextCursor = ref(null)
const loading = ref(false)
const loadingMore = ref(false)
const errorMsg = ref('')
// vid -> 是否正在请求点赞，防止重复点击
const pendingLikes = reactive({})

function resolveAsset(url) {
  if (!url) return ''
  if (/^https?:\/\//i.test(url)) return url
  return `${ASSET_BASE}${url.startsWith('/') ? '' : '/'}${url}`
}

// next_cursor 的字段需要带 cursor. 前缀回传，如 cursor.last_vid
function cursorToParams(cursor) {
  const params = {}
  if (!cursor) return params
  for (const [key, value] of Object.entries(cursor)) {
    if (value !== undefined && value !== null && value !== '') {
      params[`cursor.${key}`] = value
    }
  }
  return params
}

function getErrorMessage(error) {
  return error.response?.data?.message || error.message || '请求失败'
}

async function fetchFeed(cursor) {
  const res = await request.get('/feed/list', {
    params: { limit: PAGE_SIZE, ...cursorToParams(cursor) }
  })
  return res.data || {}
}

async function loadFirstPage() {
  loading.value = true
  errorMsg.value = ''
  try {
    const payload = await fetchFeed(null)
    videos.value = payload.list || []
    nextCursor.value = payload.next_cursor || null
    // 不足一页说明已经到底，避免多请求一次
    if (videos.value.length < PAGE_SIZE) nextCursor.value = null
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loadingMore.value) return

  loadingMore.value = true
  errorMsg.value = ''
  try {
    const payload = await fetchFeed(nextCursor.value)
    const list = payload.list || []
    videos.value = videos.value.concat(list)
    nextCursor.value = payload.next_cursor || null
    if (list.length < PAGE_SIZE) {
      nextCursor.value = null
      if (list.length === 0) ElMessage.info('没有更多了')
    }
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    loadingMore.value = false
  }
}

async function handleLike(video) {
  const videoId = Number(video.vid)
  if (video.is_like || pendingLikes[video.vid] || !Number.isInteger(videoId) || videoId <= 0) {
    return
  }

  pendingLikes[video.vid] = true
  try {
    await request.post('/like/like', { video_id: videoId })
    video.is_like = true
    video.likes_cnt = Number(video.likes_cnt || 0) + 1
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    delete pendingLikes[video.vid]
  }
}

// 私信页在下一步实现，路由就绪后自动跳转
function goMessage() {
  if (router.hasRoute('Message')) {
    router.push('/message')
  } else {
    ElMessage.info('私信功能将在下一步实现')
  }
}

function handleLogout() {
  localStorage.removeItem('token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('user_id')
  localStorage.removeItem('user_name')
  router.replace('/login')
}

onMounted(loadFirstPage)
</script>

<template>
  <div class="home-page">
    <header class="navbar">
      <span class="brand">TikTok Feed 演示</span>
      <div class="actions">
        <span class="user">{{ userName }}</span>
        <el-button size="small" @click="goMessage">私信</el-button>
        <el-button size="small" @click="handleLogout">退出登录</el-button>
      </div>
    </header>

    <main class="content">
      <el-alert
        v-if="errorMsg"
        class="error"
        type="error"
        show-icon
        :title="errorMsg"
        closable
        @close="errorMsg = ''"
      />

      <div v-loading="loading" class="feed">
        <el-empty
          v-if="!loading && videos.length === 0"
          description="暂无视频，等待后端插入数据"
        />

        <div v-else class="video-grid">
          <el-card v-for="video in videos" :key="video.vid" class="video-card" shadow="hover">
            <el-image
              class="cover"
              :src="resolveAsset(video.cover_url)"
              fit="cover"
              lazy
            >
              <template #error>
                <div class="cover-fallback">暂无封面</div>
              </template>
            </el-image>

            <h3 class="video-title" :title="video.title">{{ video.title || '无标题' }}</h3>
            <p class="video-desc" :title="video.desc">{{ video.desc || '暂无描述' }}</p>
            <div class="author">@{{ video.author_name || '匿名用户' }}</div>

            <div class="toolbar">
              <span class="stat">点赞 {{ video.likes_cnt || 0 }}</span>
              <span class="stat">评论 {{ video.comments_cnt || 0 }}</span>
              <el-button
                size="small"
                :type="video.is_like ? 'danger' : 'default'"
                :plain="!video.is_like"
                :disabled="video.is_like || !!pendingLikes[video.vid]"
                :loading="!!pendingLikes[video.vid]"
                @click="handleLike(video)"
              >
                {{ video.is_like ? '已赞' : '点赞' }}
              </el-button>
            </div>
          </el-card>
        </div>
      </div>

      <div v-if="nextCursor" class="load-more">
        <el-button :loading="loadingMore" @click="loadMore">
          {{ loadingMore ? '加载中' : '加载更多' }}
        </el-button>
      </div>
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

.brand {
  font-size: 18px;
  font-weight: 600;
}

.actions {
  display: flex;
  align-items: center;
  gap: 12px;
  color: #606266;
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

.feed {
  min-height: 200px;
}

.video-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 20px;
}

.video-card {
  display: flex;
  flex-direction: column;
}

.cover {
  width: 100%;
  aspect-ratio: 16 / 9;
  border-radius: 6px;
  background: #f0f2f5;
}

.cover-fallback {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  color: #909399;
  font-size: 13px;
  background: #f0f2f5;
}

.video-title {
  margin: 12px 0 6px;
  font-size: 16px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.video-desc {
  margin: 0 0 8px;
  min-height: 20px;
  color: #606266;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.author {
  margin-bottom: 12px;
  color: #909399;
  font-size: 13px;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid #f0f0f0;
}

.stat {
  color: #606266;
  font-size: 13px;
}

.toolbar .el-button {
  margin-left: auto;
}

.load-more {
  padding: 24px 0 8px;
  text-align: center;
}
</style>
