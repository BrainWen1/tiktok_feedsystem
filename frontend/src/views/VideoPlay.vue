<script setup>
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ChatDotRound } from '@element-plus/icons-vue'
import request from '../utils/request'
import { resolveAsset } from '../utils/asset'
import CommentPanel from '../components/CommentPanel.vue'

const route = useRoute()
const router = useRouter()

const videoId = Number(route.params.id)

const video = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const likePending = ref(false)

const showComments = ref(false)
const commentTotal = ref(0)

function initial(name) {
  return (name || '?').slice(0, 1).toUpperCase()
}

function backHome() {
  router.push('/')
}

// 作者/评论者头像或名字统一跳到个人主页
function openUser(id) {
  const uid = Number(id)
  if (!uid) return
  router.push(`/user/${uid}`)
}

async function loadVideo() {
  if (!Number.isInteger(videoId) || videoId <= 0) {
    errorMsg.value = '视频 ID 不合法'
    return
  }

  loading.value = true
  errorMsg.value = ''
  try {
    const res = await request.get(`/video/${videoId}`)
    video.value = res.data || null
  } catch (error) {
    errorMsg.value = error.response?.data?.message || error.message || '视频加载失败'
  } finally {
    loading.value = false
  }
}

// 详情接口不返回评论数，用最小的评论分页请求拿总数
async function loadCommentCount() {
  if (!Number.isInteger(videoId) || videoId <= 0) return
  try {
    const res = await request.get('/comment/list', {
      params: { video_id: videoId, page_size: 1 },
      silent: true
    })
    commentTotal.value = Number(res.data?.total || 0)
  } catch {
    // 计数失败不影响播放
  }
}

async function toggleLike() {
  if (!video.value || likePending.value) return

  const nextLiked = !video.value.is_liked
  const prevCount = Number(video.value.like_count || 0)
  // 先更新按钮状态，请求失败再回滚，保证点击后有即时反馈
  video.value.is_liked = nextLiked
  video.value.like_count = Math.max(0, prevCount + (nextLiked ? 1 : -1))
  likePending.value = true

  try {
    await request.post(nextLiked ? '/like/like' : '/like/unlike', { video_id: videoId })
  } catch {
    video.value.is_liked = !nextLiked
    video.value.like_count = prevCount
  } finally {
    likePending.value = false
  }
}

function openComments() {
  showComments.value = true
}

function onCommentCountChange({ total }) {
  commentTotal.value = total
}

onMounted(() => {
  loadVideo()
  loadCommentCount()
})
</script>

<template>
  <div v-loading="loading" class="video-page">
    <button class="exit-btn" title="返回推荐流" @click="backHome">
      <el-icon><ArrowLeft /></el-icon>
      <span>返回</span>
    </button>

    <el-alert
      v-if="errorMsg"
      class="page-error"
      type="error"
      show-icon
      :title="errorMsg"
      closable
      @close="errorMsg = ''"
    />

    <div class="video-stage">
      <video
        v-if="video"
        class="player"
        :src="resolveAsset(video.video_url)"
        :poster="resolveAsset(video.cover_url)"
        controls
        autoplay
        muted
        loop
        playsinline
      />
    </div>

    <aside v-if="video" class="rail">
      <div
        class="rail-item author-item"
        :class="{ clickable: video.author?.user_id }"
        title="查看作者主页"
        @click="openUser(video.author?.user_id)"
      >
        <el-avatar :size="56" :src="resolveAsset(video.author?.avatar_url)">
          {{ initial(video.author?.user_name) }}
        </el-avatar>
        <span class="rail-author-name" :title="video.author?.user_name">
          {{ video.author?.user_name || '匿名用户' }}
        </span>
      </div>

      <button
        class="rail-item rail-action like-action"
        :class="{ liked: video.is_liked }"
        :disabled="likePending"
        :title="video.is_liked ? '取消点赞' : '点赞'"
        @click="toggleLike"
      >
        <svg class="big-heart" viewBox="0 0 24 24" aria-hidden="true">
          <path
            d="M12 21s-7.5-4.9-9.5-9.2C1 8.3 3.2 4.5 7 4.5c2 0 3.4 1.1 4.3 2.4l.7 1 .7-1c.9-1.3 2.3-2.4 4.3-2.4 3.8 0 6 3.8 4.5 7.3C19.5 16.1 12 21 12 21z"
            :fill="video.is_liked ? 'currentColor' : 'rgba(255,255,255,0.2)'"
            stroke="currentColor"
            stroke-width="1.6"
          />
        </svg>
        <span class="rail-count">{{ video.like_count || 0 }}</span>
      </button>

      <button class="rail-item rail-action" title="查看评论" @click="openComments">
        <el-icon :size="30"><ChatDotRound /></el-icon>
        <span class="rail-count">{{ commentTotal }}</span>
      </button>
    </aside>

    <CommentPanel
      v-model:visible="showComments"
      :video-id="videoId"
      @count-change="onCommentCountChange"
    />
  </div>
</template>

<style scoped>
.video-page {
  position: relative;
  height: calc(100vh - 56px);
  overflow: hidden;
  background: #000;
}

.exit-btn {
  position: absolute;
  top: 20px;
  left: 20px;
  z-index: 20;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  color: #fff;
  font-size: 14px;
  background: rgba(0, 0, 0, 0.45);
  border: none;
  border-radius: 20px;
  cursor: pointer;
}

.exit-btn:hover {
  background: rgba(0, 0, 0, 0.65);
}

.page-error {
  position: absolute;
  top: 76px;
  left: 20px;
  z-index: 20;
  width: 360px;
}

.video-stage {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}

.player {
  max-width: 100%;
  max-height: 100%;
  background: #000;
}

.rail {
  position: absolute;
  right: 28px;
  bottom: 100px;
  z-index: 5;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 26px;
}

.rail-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
}

.author-item {
  max-width: 80px;
}

.author-item.clickable {
  cursor: pointer;
}

.author-item.clickable:hover .rail-author-name {
  color: #409eff;
}

.rail-author-name {
  max-width: 80px;
  overflow: hidden;
  color: #fff;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rail-action {
  padding: 0;
  color: #fff;
  background: none;
  border: none;
  cursor: pointer;
}

.rail-action:disabled {
  cursor: default;
}

.big-heart {
  width: 34px;
  height: 34px;
  transition: color 0.15s;
}

.like-action.liked {
  color: #fe2c55;
}

.rail-count {
  color: #fff;
  font-size: 13px;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.5);
}
</style>
