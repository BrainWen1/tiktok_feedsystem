<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Close } from '@element-plus/icons-vue'
import request from '../utils/request'

const props = defineProps({
  videoId: {
    type: Number,
    default: 0
  },
  visible: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'count-change'])

const router = useRouter()
const myName = localStorage.getItem('user_name') || '我'

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

function openUser(id) {
  const uid = Number(id)
  if (!uid) return
  close()
  router.push(`/user/${uid}`)
}

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

function emitCount() {
  emit('count-change', {
    videoId: props.videoId,
    total: Number(commentTotal.value) || 0
  })
}

async function loadComments() {
  if (!Number.isInteger(props.videoId) || props.videoId <= 0) return

  commentsLoading.value = true
  try {
    const res = await request.get('/comment/list', {
      params: { video_id: props.videoId, page_size: 50 }
    })
    const data = res.data || {}
    comments.value = data.comments || []
    commentTotal.value = Number(data.total || 0)
    emitCount()
  } catch {
    // 错误提示由 request 拦截器统一处理
  } finally {
    commentsLoading.value = false
  }
}

function close() {
  emit('update:visible', false)
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
      video_id: props.videoId,
      parent_id: parentId,
      content
    })

    // 评论通过 MQ 异步入库，接口不返回评论体：先本地回显，稍后从服务端同步
    comments.value.unshift({
      id: `local-${Date.now()}`,
      video_id: props.videoId,
      user_id: Number(localStorage.getItem('user_id')) || 0,
      user_name: myName,
      parent_id: parentId,
      content,
      created_at: new Date().toISOString()
    })
    commentTotal.value += 1
    emitCount()
    commentContent.value = ''
    cancelReply()
    setTimeout(loadComments, 1000)
  } catch {
    // 错误提示由 request 拦截器统一处理
  } finally {
    commentSending.value = false
  }
}

// 打开面板或切换到另一条视频时加载评论；同一个面板被不同视频复用时要先清空上一轮状态
watch(
  () => [props.visible, props.videoId],
  ([visible, videoId], [prevVisible, prevVideoId]) => {
    if (!visible) {
      cancelReply()
      return
    }

    const videoChanged = videoId !== prevVideoId
    if (!prevVisible || videoChanged) {
      if (videoChanged) {
        comments.value = []
        commentTotal.value = 0
        commentContent.value = ''
        cancelReply()
      }
      loadComments()
    }
  }
)
</script>

<template>
  <Teleport to="body">
    <transition name="comment-fade">
      <div v-if="visible" class="comment-mask" @click.self="close" />
    </transition>

    <transition name="comment-slide">
      <section v-if="visible" class="comment-panel">
        <header class="comment-header">
          <span class="comment-title">评论 {{ commentTotal }}</span>
          <button class="close-btn" type="button" title="关闭评论" @click="close">
            <el-icon :size="18"><Close /></el-icon>
          </button>
        </header>

        <div v-loading="commentsLoading" class="comment-scroll">
          <el-empty
            v-if="!commentsLoading && commentThreads.length === 0"
            description="还没有评论，来抢沙发"
            :image-size="60"
          />

          <div v-for="thread in commentThreads" :key="thread.id" class="comment-item">
            <el-avatar
              :size="36"
              class="comment-avatar"
              title="查看评论者主页"
              @click.stop="openUser(thread.user_id)"
            >
              {{ initial(thread.user_name) }}
            </el-avatar>
            <div class="comment-body">
              <div class="comment-name clickable" @click.stop="openUser(thread.user_id)">
                {{ thread.user_name || '匿名用户' }}
              </div>
              <div class="comment-text">{{ thread.content }}</div>
              <div class="comment-meta">
                <span>{{ formatTime(thread.created_at) }}</span>
                <el-button link size="small" @click="startReply(thread, thread.id)">回复</el-button>
              </div>

              <div v-if="thread.replies.length" class="reply-list">
                <div v-for="reply in thread.replies" :key="reply.id" class="reply-item">
                  <span
                    class="reply-name clickable"
                    @click.stop="openUser(reply.user_id)"
                  >{{ reply.user_name || '匿名用户' }}：</span>
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
    </transition>
  </Teleport>
</template>

<style scoped>
.comment-mask {
  position: fixed;
  top: 56px;
  right: 0;
  bottom: 0;
  left: 0;
  z-index: 280;
  background: rgba(0, 0, 0, 0.35);
}

.comment-panel {
  position: fixed;
  top: 56px;
  right: 0;
  bottom: 0;
  z-index: 290;
  display: flex;
  flex-direction: column;
  width: 420px;
  background: #fff;
  box-shadow: -2px 0 16px rgba(0, 0, 0, 0.25);
}

.comment-header {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  height: 56px;
  padding: 0 16px;
  border-bottom: 1px solid #e4e7ed;
}

.comment-title {
  color: #303133;
  font-size: 15px;
  font-weight: 600;
}

.close-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  color: #606266;
  background: #f5f7fa;
  border: none;
  border-radius: 50%;
  cursor: pointer;
}

.close-btn:hover {
  background: #e9ecf0;
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
  cursor: pointer;
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

.clickable {
  cursor: pointer;
}

.comment-name.clickable:hover,
.reply-name.clickable:hover {
  color: #409eff;
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

.comment-fade-enter-active,
.comment-fade-leave-active {
  transition: opacity 0.2s;
}

.comment-fade-enter-from,
.comment-fade-leave-to {
  opacity: 0;
}

.comment-slide-enter-active,
.comment-slide-leave-active {
  transition: transform 0.24s ease;
}

.comment-slide-enter-from,
.comment-slide-leave-to {
  transform: translateX(100%);
}

@media (max-width: 560px) {
  .comment-panel {
    width: 100%;
  }
}
</style>
