<script setup>
import { computed, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../utils/request'
import VideoCard from '../components/VideoCard.vue'
import { normalizeFeedItem, resolveAvatar } from '../utils/video'
import { useMessageStore } from '../store/message'
import { useNotificationStore } from '../store/notification'
import { useSseStore } from '../store/sse'

const PAGE_SIZE = 10

const router = useRouter()
const messageStore = useMessageStore()
const notificationStore = useNotificationStore()
const sseStore = useSseStore()
const { unreadCount } = storeToRefs(messageStore)
const userName = localStorage.getItem('user_name') || '用户'
const myUserId = Number(localStorage.getItem('user_id')) || 0
// 导航栏头像需要单独拉一次自己的资料，失败时回退默认头像
const myAvatar = ref('')
const myAvatarSrc = computed(() => resolveAvatar(myAvatar.value))

const videos = ref([])
const nextCursor = ref(null)
const loading = ref(false)
const loadingMore = ref(false)
const errorMsg = ref('')

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
    videos.value = (payload.list || []).map(normalizeFeedItem).filter(Boolean)
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
    const list = (payload.list || []).map(normalizeFeedItem).filter(Boolean)
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

function goPublish() {
  router.push('/publish')
}

function goMyProfile() {
  if (!myUserId) return
  router.push(`/user/${myUserId}`)
}

// 卡片内点赞成功后同步列表中的同一视频，避免切换页面回来状态不一致
function onLikeChange({ vid, isLiked, likeCount }) {
  const item = videos.value.find((video) => String(video.vid) === String(vid))
  if (item) {
    item.isLiked = isLiked
    item.likeCount = likeCount
  }
}

async function loadMyProfile() {
  if (!myUserId) return
  try {
    const res = await request.get('/user/profile')
    myAvatar.value = res.data?.avatar_url || ''
  } catch (error) {
    // 头像加载失败不影响 Feed 展示
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
  sseStore.stop()
  messageStore.reset()
  notificationStore.reset()
  localStorage.removeItem('token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('user_id')
  localStorage.removeItem('user_name')
  router.replace('/login')
}

onMounted(() => {
  loadFirstPage()
  loadMyProfile()
})
</script>

<template>
  <div class="home-page">
    <header class="navbar">
      <span class="brand">TikTok Feed 演示</span>
      <div class="actions">
        <el-button size="small" type="primary" @click="goPublish">发布视频</el-button>
        <button class="user-chip" title="我的主页" @click="goMyProfile">
          <el-avatar class="user-avatar" :size="28" :src="myAvatarSrc">
            {{ userName.slice(0, 1).toUpperCase() }}
          </el-avatar>
          <span class="user">{{ userName }}</span>
        </button>
        <el-badge
          :value="unreadCount"
          :max="99"
          :hidden="unreadCount === 0"
          class="message-badge"
        >
          <el-button size="small" @click="goMessage">私信</el-button>
        </el-badge>
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
          <VideoCard
            v-for="video in videos"
            :key="video.vid"
            :video="video"
            @like-change="onLikeChange"
          />
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

.user-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 2px 8px 2px 2px;
  color: #606266;
  background: none;
  border: none;
  border-radius: 16px;
  cursor: pointer;
}

.user-chip:hover {
  background: #f5f7fa;
}

.user-avatar {
  flex: none;
  background: #409eff;
}

.user-chip .user {
  font-size: 14px;
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

.load-more {
  padding: 24px 0 8px;
  text-align: center;
}
</style>
