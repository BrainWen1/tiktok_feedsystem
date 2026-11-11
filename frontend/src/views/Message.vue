<script setup>
import { nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute, useRouter } from 'vue-router'
import request from '../utils/request'
import { resolveAvatar } from '../utils/video'
import { useMessageStore } from '../store/message'

const route = useRoute()
const router = useRouter()
const messageStore = useMessageStore()
// SSE 连接由 App.vue 全局维护，这里只消费全局状态
const { latestMessage, sseStatus } = storeToRefs(messageStore)

const myName = localStorage.getItem('user_name') || '我'
// 当前登录用户 ID：登录成功后由 Login.vue 写入 localStorage。
// 兼容修复前已登录的旧会话（localStorage 里还没有 user_id）：回退解析 JWT。
const myUserId = Number(localStorage.getItem('user_id')) || decodeUserId(localStorage.getItem('token'))

const sessions = ref([])
const currentSession = ref(null)
const messages = ref([])
const inputContent = ref('')

// 头像缓存：历史消息只带 sender_id，需要按用户查资料补齐头像
const avatarMap = reactive({})
const avatarLoading = new Set()

const sessionsLoading = ref(false)
const historyLoading = ref(false)
const sending = ref(false)
const errorMsg = ref('')

const messageListRef = ref(null)

// ------------------------- 工具函数 -------------------------

// 从 JWT payload 里取 user_id（仅用于本地回显消息的归属判断，不做鉴权）
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

// 会话/SSE 的时间是 Unix 秒，聊天历史是 RFC3339 字符串，这里统一处理
function formatTime(value, short = false) {
  if (value === undefined || value === null || value === '') return ''
  // Go 的 time.Time 会带纳秒精度（如 .123456789），Safari 解析会失败，截断到毫秒
  const input =
    typeof value === 'string' ? value.replace(/(\.\d{3})\d+/, '$1') : value
  const date = typeof input === 'number' ? new Date(input * 1000) : new Date(input)
  if (Number.isNaN(date.getTime())) return ''

  if (short) {
    return date.toLocaleString('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    })
  }
  return date.toLocaleString('zh-CN', { hour12: false })
}

function getErrorMessage(error) {
  return error.response?.data?.message || error.message || '请求失败'
}

// 消息归属只看发送者是否为当前登录用户，不能拿 currentSession.target_id 反推
function isMine(message) {
  return Number(message.sender_id) === Number(myUserId)
}

// 对方消息展示昵称，自己的消息统一显示“我”
function senderLabel(message) {
  if (isMine(message)) return '我'
  return message.sender_name || `用户${message.sender_id}`
}

function avatarSrc(userId) {
  return resolveAvatar(avatarMap[Number(userId)])
}

function senderInitial(message) {
  return (senderLabel(message) || '?').slice(0, 1).toUpperCase()
}

// 按需拉取并缓存用户头像，失败时回退默认头像且不重复请求
async function loadAvatar(userId) {
  const id = Number(userId)
  if (!id || avatarMap[id] !== undefined || avatarLoading.has(id)) return

  avatarLoading.add(id)
  try {
    const res = await request.post('/user/find_by_id', { user_id: id })
    avatarMap[id] = res.data?.avatar_url || ''
  } catch {
    avatarMap[id] = ''
  } finally {
    avatarLoading.delete(id)
  }
}

// 历史消息与 SSE 消息都只带 sender_id，统一批量补齐头像
function ensureAvatars(list) {
  const ids = new Set()
  for (const message of list) {
    const id = Number(message.sender_id)
    if (id) ids.add(id)
  }
  ids.forEach((id) => loadAvatar(id))
}

// 点击头像进入对应用户的主页
function goUserProfile(userId) {
  const id = Number(userId)
  if (!id) return
  router.push(`/user/${id}`)
}

async function scrollToBottom(smooth = true) {
  await nextTick()
  const el = messageListRef.value
  if (el) {
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
  }
}

function backHome() {
  router.push('/')
}

// ------------------------- 会话 & 聊天 -------------------------

// 从路由 query 中取目标用户 ID（主页点击“私信”时携带）
function targetIdFromQuery() {
  const raw = Array.isArray(route.query.target_id)
    ? route.query.target_id[0]
    : route.query.target_id
  return Number(raw) || 0
}

// 打开指定用户的会话：已存在则直接选中，从未聊过则先构造会话再拉历史
async function openTargetSession(targetId) {
  if (!targetId || targetId === Number(myUserId)) return

  const existing = sessions.value.find((item) => Number(item.target_id) === targetId)
  if (existing) {
    await selectSession(existing)
    return
  }

  let targetName = `用户${targetId}`
  try {
    const res = await request.post('/user/find_by_id', { user_id: targetId })
    targetName = res.data?.user_name || targetName
    avatarMap[targetId] = res.data?.avatar_url || ''
  } catch {
    // 资料查询失败时使用兜底名称，不影响打开聊天窗口
  }

  const session = {
    target_id: targetId,
    target_name: targetName,
    content: '',
    created_at: null,
    unread_count: 0
  }
  sessions.value = [session, ...sessions.value]
  await selectSession(session)
}

async function loadSessions() {
  sessionsLoading.value = true
  errorMsg.value = ''
  try {
    const res = await request.get('/message/sessions', { params: { limit: 50 } })
    sessions.value = Array.isArray(res.data) ? res.data : []

    const targetId = targetIdFromQuery()
    if (targetId) {
      // 从主页跳转进来：优先打开指定用户，而不是默认第一个会话
      await openTargetSession(targetId)
    } else if (!currentSession.value && sessions.value.length > 0) {
      // 首次进入默认打开第一个会话
      selectSession(sessions.value[0])
    }
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    sessionsLoading.value = false
  }
}

async function selectSession(session) {
  if (currentSession.value?.target_id === session.target_id) return

  currentSession.value = session
  messageStore.setActiveChat(session.target_id)
  // 打开会话即视为已读：同步清零左侧徽标和全局未读数
  messageStore.reduceUnread(session.unread_count)
  session.unread_count = 0
  messages.value = []
  errorMsg.value = ''
  historyLoading.value = true

  const targetId = session.target_id
  try {
    const res = await request.get('/message/history', {
      params: { target_id: targetId, page_size: 20 }
    })
    // 快速切换会话时，丢弃过期响应
    if (currentSession.value?.target_id !== targetId) return

    // 后端返回新 -> 旧，聊天窗口需要旧 -> 新
    const list = res.data?.messages || []
    messages.value = list.slice().reverse()
    ensureAvatars(messages.value)
    scrollToBottom(false)
  } catch (error) {
    if (currentSession.value?.target_id === targetId) {
      errorMsg.value = getErrorMessage(error)
    }
  } finally {
    if (currentSession.value?.target_id === targetId) {
      historyLoading.value = false
    }
  }
}

async function sendMessage() {
  const content = inputContent.value.trim()
  if (!content || !currentSession.value || sending.value) return

  const targetId = currentSession.value.target_id
  const now = Math.floor(Date.now() / 1000)
  sending.value = true
  errorMsg.value = ''

  try {
    await request.post('/message/send', { receiver_id: targetId, content })

    // 发送接口不回消息体，前端本地回显
    messages.value.push({
      id: `local-${Date.now()}-${messages.value.length}`,
      sender_id: myUserId,
      receiver_id: targetId,
      sender_name: myName,
      content,
      created_at: new Date(now * 1000).toISOString(),
      is_read: false
    })
    inputContent.value = ''
    updateSessionSummary(targetId, content, now)
    scrollToBottom()
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    sending.value = false
  }
}

// 更新左侧会话摘要，并把该会话移到最前面
function updateSessionSummary(targetId, content, createdAt) {
  const session = sessions.value.find((item) => item.target_id === targetId)
  if (!session) return

  session.content = content
  session.created_at = createdAt
  sessions.value = [session, ...sessions.value.filter((item) => item !== session)]
}

function onEnter(event) {
  // 中文输入法选词时的回车不触发发送
  if (event.isComposing) return
  sendMessage()
}

// ------------------------- 全局 SSE 消息 -------------------------

// 全局连接收到新消息后，App.vue 里的 store 会更新 latestMessage。
// 私信页只负责把它反映到会话列表和当前聊天窗口。
watch(latestMessage, (message) => {
  if (!message) return

  const senderId = Number(message.sender_id)
  loadAvatar(senderId)

  const session = sessions.value.find((item) => Number(item.target_id) === senderId)

  if (session) {
    updateSessionSummary(senderId, message.content, message.created_ts)
  } else {
    sessions.value.unshift({
      target_id: senderId,
      target_name: message.sender_name,
      content: message.content,
      created_at: message.created_ts,
      unread_count: 0
    })
  }

  if (Number(currentSession.value?.target_id) === senderId) {
    // 发送者正是当前聊天对象，直接追加进聊天窗口
    messages.value.push(message)
    scrollToBottom()
  } else {
    // 其他会话只更新左侧预览和未读徽标，不污染当前聊天窗口
    const target = sessions.value.find((item) => Number(item.target_id) === senderId)
    if (target) target.unread_count = Number(target.unread_count || 0) + 1
  }
})

onMounted(() => {
  messageStore.setMessagePageActive(true)
  // 进入私信页即清空导航栏的全局未读徽标
  messageStore.clearUnread()
  loadAvatar(myUserId)
  loadSessions()
})

onUnmounted(() => {
  messageStore.setMessagePageActive(false)
})
</script>

<template>
  <el-container class="message-page">
    <el-header height="56px" class="topbar">
      <span class="brand">私信</span>
      <div class="topbar-right">
        <span class="sse-dot" :class="sseStatus" />
        <span class="sse-text">
          {{ sseStatus === 'connected' ? '实时连接已建立' : sseStatus === 'connecting' ? '连接中…' : '连接已断开' }}
        </span>
        <el-button size="small" @click="backHome">返回主页</el-button>
      </div>
    </el-header>

    <el-container class="body">
      <el-aside width="300px" class="session-aside">
        <div class="aside-title">会话列表</div>

        <div v-loading="sessionsLoading" class="session-list">
          <el-empty
            v-if="!sessionsLoading && sessions.length === 0"
            description="暂无私信会话"
            :image-size="60"
          />

          <div
            v-for="session in sessions"
            :key="session.target_id"
            class="session-item"
            :class="{ active: currentSession?.target_id === session.target_id }"
            @click="selectSession(session)"
          >
            <div class="session-line">
              <span class="session-name">{{ session.target_name }}</span>
              <span class="session-time">{{ formatTime(session.created_at, true) }}</span>
            </div>
            <div class="session-line">
              <span class="session-content">{{ session.content }}</span>
              <el-badge
                v-if="session.unread_count"
                :value="session.unread_count"
                class="unread"
              />
            </div>
          </div>
        </div>
      </el-aside>

      <el-main class="chat-main">
        <el-alert
          v-if="errorMsg"
          class="error"
          type="error"
          show-icon
          :title="errorMsg"
          closable
          @close="errorMsg = ''"
        />

        <template v-if="currentSession">
          <div class="chat-header">
            <span class="chat-name">{{ currentSession.target_name }}</span>
            <span class="chat-sub">用户 ID: {{ currentSession.target_id }}</span>
          </div>

          <div ref="messageListRef" v-loading="historyLoading" class="message-list">
            <div
              v-if="!historyLoading && messages.length === 0"
              class="chat-empty"
            >
              还没有聊天记录，发条消息试试
            </div>

            <div
              v-for="message in messages"
              :key="message.id"
              class="message-row"
              :class="{ mine: isMine(message) }"
            >
              <el-avatar
                class="message-avatar"
                :size="36"
                :src="avatarSrc(message.sender_id)"
                :title="senderLabel(message)"
                @click="goUserProfile(message.sender_id)"
              >
                {{ senderInitial(message) }}
              </el-avatar>

              <div class="bubble-wrap">
                <div class="bubble-meta">
                  <span class="sender-name">{{ senderLabel(message) }}</span>
                  <span>{{ formatTime(message.created_at) }}</span>
                </div>
                <div class="bubble">{{ message.content }}</div>
              </div>
            </div>
          </div>

          <div class="chat-footer">
            <el-input
              v-model="inputContent"
              placeholder="输入消息，按 Enter 发送"
              maxlength="1000"
              clearable
              @keyup.enter="onEnter"
            />
            <el-button
              type="primary"
              :loading="sending"
              :disabled="!inputContent.trim()"
              @click="sendMessage"
            >
              发送
            </el-button>
          </div>
        </template>

        <div v-else class="no-chat">
          <el-empty description="请选择左侧会话开始聊天" />
        </div>
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped>
.message-page {
  height: 100%;
  background: #f5f5f5;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
}

.brand {
  font-size: 18px;
  font-weight: 600;
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.sse-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #c0c4cc;
}

.sse-dot.connected {
  background: #67c23a;
}

.sse-dot.disconnected {
  background: #f56c6c;
}

.sse-text {
  color: #909399;
  font-size: 12px;
}

.body {
  overflow: hidden;
}

.session-aside {
  display: flex;
  flex-direction: column;
  background: #fff;
  border-right: 1px solid #e4e7ed;
}

.aside-title {
  padding: 12px 16px;
  color: #303133;
  font-size: 14px;
  font-weight: 600;
  border-bottom: 1px solid #f0f0f0;
}

.session-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.session-item {
  padding: 12px 16px;
  border-bottom: 1px solid #f5f5f5;
  cursor: pointer;
}

.session-item:hover {
  background: #f5f7fa;
}

.session-item.active {
  background: #ecf5ff;
}

.session-line {
  display: flex;
  align-items: center;
  gap: 8px;
}

.session-line + .session-line {
  margin-top: 6px;
}

.session-name {
  flex: 1;
  overflow: hidden;
  color: #303133;
  font-size: 14px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-time {
  flex: none;
  color: #c0c4cc;
  font-size: 12px;
}

.session-content {
  flex: 1;
  overflow: hidden;
  color: #909399;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.unread {
  flex: none;
}

.chat-main {
  display: flex;
  flex-direction: column;
  padding: 0;
}

.error {
  flex: none;
  margin: 12px 20px 0;
}

.chat-header {
  flex: none;
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 16px 20px;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
}

.chat-name {
  color: #303133;
  font-size: 16px;
  font-weight: 600;
}

.chat-sub {
  color: #c0c4cc;
  font-size: 12px;
}

.message-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
}

.chat-empty {
  padding-top: 40px;
  color: #c0c4cc;
  font-size: 13px;
  text-align: center;
}

.message-row {
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
  gap: 10px;
  margin-bottom: 14px;
}

/* 自己的消息整体右对齐，头像落在右边界 */
.message-row.mine {
  flex-direction: row-reverse;
}

/* 双方头像都贴靠聊天区左右边界，点击可进入用户主页 */
.message-avatar {
  flex: none;
  color: #fff;
  background: #409eff;
  cursor: pointer;
}

.message-avatar:hover {
  opacity: 0.85;
}

.bubble-wrap {
  max-width: 60%;
}

.bubble-meta {
  display: flex;
  gap: 8px;
  margin-bottom: 4px;
  color: #909399;
  font-size: 12px;
}

/* 昵称始终紧挨头像、位于靠画面中心的一侧 */
.message-row.mine .bubble-meta {
  flex-direction: row-reverse;
}

.sender-name {
  color: #606266;
  font-weight: 500;
}

.bubble {
  padding: 8px 12px;
  border-radius: 8px;
  background: #fff;
  color: #303133;
  font-size: 14px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.message-row.mine .bubble {
  background: #409eff;
  color: #fff;
}

.chat-footer {
  flex: none;
  display: flex;
  gap: 12px;
  padding: 12px 20px;
  background: #fff;
  border-top: 1px solid #e4e7ed;
}

.chat-footer .el-input {
  flex: 1;
}

.no-chat {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
