<script setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ChatDotRound } from '@element-plus/icons-vue'
import request from '../utils/request'
import { resolveAsset } from '../utils/asset'

const route = useRoute()
const router = useRouter()

const videoId = Number(route.params.id)
const myName = localStorage.getItem('user_name') || '我'

const video = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const likePending = ref(false)

const showComments = ref(false)
const comments = ref([])
const commentTotal = ref(0)
const commentsLoading = ref(false)
const commentContent = ref('')
const replyTarget = ref(null) // { id: 顶级评论ID, userName: 被回复人 }
const commentSending = ref(false)
const commentInputRef = ref(null)

function formatTime(value) {
  if (!value) return ''
  // Go 的 time.Time 带纳秒精度，Safari 解析会失败，截断到毫秒
  const input = typeof value === 'string' ? value.replace(/(\.\d{3})\d+/, '$1') : value
  const date = new Date(input)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString('zh-CN', { hour12: false })
}

function initial(name) {
  return (name || '?').slice(0, 1).toUpperCase()
}

function backHome() {
  router.push('/')
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
  } catch (error) {
    video.value.is_liked = !nextLiked
    video.value.like_count = prevCount
  } finally {
    likePending.value = false
  }
}

// ------------------------- 评论区 -------------------------

const commentThreads = computed(() => {
  const all = comments.value
  const byId = new Map(all.map((item) => [item.id, item]))

  // 后端只存 parent_id，这里沿父链回溯到顶级评论，保证多级回复也能归到同一条主评论下
  function rootIdOf(comment) {
    let current = comment
    for (let i = 0; i < 20 && current?.parent_id; i += 1) {
      const parent = byId.get(current.parent_id)
      if (!parent) return current.parent_id
      current = parent
    }
    return current?.id
  }

  const replies = new Map()
  for (const comment of all) {
    if (!comment.parent_id) continue
    const rootId = rootIdOf(comment)
    if (!replies.has(rootId)) replies.set(rootId, [])
    replies.get(rootId).push(comment)
  }

  return all
    .filter((comment) => !comment.parent_id)
    .map((comment) => ({
      ...comment,
      replies: (replies.get(comment.id) || []).slice().sort((a, b) => {
        return new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
      })
    }))
})

async function loadComments() {
  if (!Number.isInteger(videoId) || videoId <= 0) return

  commentsLoading.value = true
  try {
    const res = await request.get('/comment/list', {
      params: { video_id: videoId, page_size: 50 }
    })
    const data = res.data || {}
    comments.value = data.comments || []
    commentTotal.value = Number(data.total || 0)
  } catch (error) {
    errorMsg.value = error.response?.data?.message || error.message || '评论加载失败'
  } finally {
    commentsLoading.value = false
  }
}

function openComments() {
  showComments.value = true
  if (comments.value.length === 0) loadComments()
}

function closeComments() {
  showComments.value = false
  cancelReply()
}

function startReply(comment, rootId) {
  replyTarget.value = { id: rootId, userName: comment.user_name || '该用户' }
  nextTick(() => commentInputRef.value?.focus())
}

function cancelReply() {
  replyTarget.value = null
}

function onCommentEnter(event) {
  if (event.isComposing) return
  publishComment()
}

async function publishComment() {
  const content = commentContent.value.trim()
  if (!content || commentSending.value) return

  const parentId = replyTarget.value?.id || 0
  commentSending.value = true
  try {
    await request.post('/comment/publish', {
      video_id: videoId,
      parent_id: parentId,
      content
    })

    // 评论通过 MQ 异步入库，接口不返回评论体：先本地回显，稍后从服务端同步
    comments.value.unshift({
      id: `local-${Date.now()}`,
      video_id: videoId,
      user_id: Number(localStorage.getItem('user_id')) || 0,
      user_name: myName,
      parent_id: parentId,
      content,
      created_at: new Date().toISOString()
    })
    commentTotal.value += 1
    commentContent.value = ''
    cancelReply()
    setTimeout(loadComments, 1000)
  } finally {
    commentSending.value = false
  }
}

onMounted(() => {
  loadVideo()
  loadComments()
})
</script>

<template>
  <div v-loading="loading" class="video-page">
    <button class="exit-btn" title="退出到主页" @click="backHome">
      <el-icon><ArrowLeft /></el-icon>
      <span>退出</span>
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
      <div class="rail-item author-item">
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

    <section v-if="showComments" class="comment-panel">
      <header class="comment-header">
        <button class="panel-exit" title="退出到视频播放页面" @click="closeComments">
          <el-icon><ArrowLeft /></el-icon>
          <span>返回视频</span>
        </button>
        <span class="comment-title">评论 {{ commentTotal }}</span>
      </header>

      <div v-loading="commentsLoading" class="comment-scroll">
        <el-empty
          v-if="!commentsLoading && commentThreads.length === 0"
          description="还没有评论，来抢沙发"
          :image-size="60"
        />

        <div v-for="thread in commentThreads" :key="thread.id" class="comment-item">
          <el-avatar :size="36" class="comment-avatar">{{ initial(thread.user_name) }}</el-avatar>
          <div class="comment-body">
            <div class="comment-name">{{ thread.user_name || '匿名用户' }}</div>
            <div class="comment-text">{{ thread.content }}</div>
            <div class="comment-meta">
              <span>{{ formatTime(thread.created_at) }}</span>
              <el-button link size="small" @click="startReply(thread, thread.id)">回复</el-button>
            </div>

            <div v-if="thread.replies.length" class="reply-list">
              <div v-for="reply in thread.replies" :key="reply.id" class="reply-item">
                <span class="reply-name">{{ reply.user_name || '匿名用户' }}：</span>
                <span class="reply-text">{{ reply.content }}</span>
                <div class="comment-meta">
                  <span>{{ formatTime(reply.created_at) }}</span>
                  <el-button link size="small" @click="startReply(reply, thread.id)">回复</el-button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <footer class="comment-footer">
        <div v-if="replyTarget" class="reply-hint">
          <span>回复 @{{ replyTarget.userName }}</span>
          <el-button link size="small" @click="cancelReply">取消</el-button>
        </div>
        <div class="comment-input-row">
          <el-input
            ref="commentInputRef"
            v-model="commentContent"
            :placeholder="replyTarget ? `回复 @${replyTarget.userName}` : '说点什么…'"
            maxlength="500"
            clearable
            @keyup.enter="onCommentEnter"
          />
          <el-button
            type="primary"
            :loading="commentSending"
            :disabled="!commentContent.trim()"
            @click="publishComment"
          >
            发送
          </el-button>
        </div>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.video-page {
  position: relative;
  height: 100vh;
  overflow: hidden;
  background: #000;
}

.exit-btn,
.panel-exit {
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

.exit-btn {
  position: absolute;
  top: 20px;
  left: 20px;
  z-index: 20;
}

.exit-btn:hover,
.panel-exit:hover {
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

.comment-panel {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 15;
  display: flex;
  flex-direction: column;
  width: 420px;
  background: #fff;
  box-shadow: -2px 0 12px rgba(0, 0, 0, 0.25);
}

.comment-header {
  flex: none;
  display: flex;
  align-items: center;
  gap: 12px;
  height: 56px;
  padding: 0 16px;
  border-bottom: 1px solid #e4e7ed;
}

.panel-exit {
  color: #303133;
  background: #f5f7fa;
}

.panel-exit:hover {
  background: #e9ecf0;
}

.comment-title {
  color: #303133;
  font-size: 15px;
  font-weight: 600;
}

.comment-scroll {
  flex: 1;
  min-height: 0;
  padding: 16px;
  overflow-y: auto;
}

.comment-item {
  display: flex;
  gap: 10px;
  margin-bottom: 18px;
}

.comment-avatar {
  flex: none;
  color: #fff;
  background: #409eff;
}

.comment-body {
  flex: 1;
  min-width: 0;
}

.comment-name {
  margin-bottom: 4px;
  color: #909399;
  font-size: 13px;
  font-weight: 500;
}

.comment-text {
  color: #303133;
  font-size: 14px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.comment-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 4px;
  color: #c0c4cc;
  font-size: 12px;
}

.comment-meta .el-button {
  color: #909399;
}

.reply-list {
  margin-top: 10px;
  padding: 8px 10px;
  background: #f7f8fa;
  border-radius: 6px;
}

.reply-item + .reply-item {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #eef0f3;
}

.reply-name {
  color: #606266;
  font-size: 13px;
  font-weight: 500;
}

.reply-text {
  color: #303133;
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.comment-footer {
  flex: none;
  padding: 10px 16px 14px;
  background: #fff;
  border-top: 1px solid #e4e7ed;
}

.reply-hint {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
  color: #909399;
  font-size: 12px;
}

.comment-input-row {
  display: flex;
  gap: 8px;
}
</style>
