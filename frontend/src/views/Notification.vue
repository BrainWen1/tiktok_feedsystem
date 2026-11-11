<script setup>
import { computed, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { ElMessage } from 'element-plus'
import request from '../utils/request'
import { useNotificationStore } from '../store/notification'

// 后端默认 20 条一页，上限 100
const PAGE_SIZE = 20

// 顶部筛选：按通知类型划分，全部对应后端返回的所有类型
const FILTERS = [
  { key: 'all', label: '全部' },
  { key: 'like', label: '点赞' },
  { key: 'comment', label: '评论' },
  { key: 'follow', label: '关注' }
]

// 后端 Type 是英文枚举，这里映射成界面上的中文标签
const TYPE_META = {
  like: { label: '点赞', tag: 'success' },
  comment: { label: '评论', tag: 'primary' },
  follow: { label: '关注', tag: 'warning' }
}

const notificationStore = useNotificationStore()
const { unreadCount } = storeToRefs(notificationStore)

const activeFilter = ref('all')
const notifications = ref([])
// 游标：后端按 (created_at, id) 翻页，指向当前已加载的最后一条
const cursor = ref(null)

const loading = ref(false)
const loadingMore = ref(false)
const markingAll = ref(false)
const errorMsg = ref('')

// 类型筛选在前端做：后端 /notification/list 不提供 type 参数
const filteredList = computed(() =>
  activeFilter.value === 'all'
    ? notifications.value
    : notifications.value.filter((item) => item.Type === activeFilter.value)
)

const hasMore = computed(() => cursor.value !== null)

const typeMeta = (type) => TYPE_META[type] || { label: '通知', tag: 'info' }

// 已取消的互动通知保留在历史里，用灰色标签区分
const isCancelled = (item) => item.Status === 'cancelled'

function getErrorMessage(error) {
  return error.response?.data?.message || error.message || '请求失败'
}

function formatTime(value) {
  if (!value) return ''
  // Go 的 time.Time 带纳秒精度（如 .123456789），Safari 解析会失败，截断到毫秒
  const input = typeof value === 'string' ? value.replace(/(\.\d{3})\d+/, '$1') : value
  const date = new Date(input)
  if (Number.isNaN(date.getTime())) return ''

  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  })
}

// 后端响应：{ data: { notifications, next_last_time, next_last_id } }
async function fetchPage(last) {
  const params = { page_size: PAGE_SIZE }
  if (last) {
    params.last_time = last.last_time
    params.last_id = last.last_id
  }
  const res = await request.get('/notification/list', { params })
  return res.data?.data || {}
}

function toCursor(payload) {
  if (payload.next_last_time == null || payload.next_last_id == null) return null
  return {
    last_time: payload.next_last_time,
    last_id: payload.next_last_id
  }
}

async function loadFirstPage() {
  loading.value = true
  errorMsg.value = ''
  try {
    const payload = await fetchPage(null)
    notifications.value = Array.isArray(payload.notifications) ? payload.notifications : []
    cursor.value = toCursor(payload)
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!cursor.value || loadingMore.value) return

  loadingMore.value = true
  errorMsg.value = ''
  try {
    const payload = await fetchPage(cursor.value)
    const list = Array.isArray(payload.notifications) ? payload.notifications : []
    notifications.value = notifications.value.concat(list)
    cursor.value = toCursor(payload)
    if (list.length === 0) ElMessage.info('没有更多了')
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    loadingMore.value = false
  }
}

// 接口初始化未读数，保证导航栏红点和本页一致
async function syncUnreadCount() {
  try {
    const res = await request.get('/notification/unread_count', { silent: true })
    notificationStore.setUnreadCount(res.data?.count)
  } catch {
    // 未读数失败不影响列表展示
  }
}

async function markRead(item) {
  if (item.IsRead) return

  try {
    await request.post('/notification/read', { notification_id: item.ID })
    item.IsRead = true
    notificationStore.decreaseUnread(1)
  } catch {
    // 拦截器已统一提示错误
  }
}

async function markAllRead() {
  if (markingAll.value) return

  markingAll.value = true
  try {
    await request.post('/notification/read_all')
    notifications.value.forEach((item) => {
      item.IsRead = true
    })
    notificationStore.setUnreadCount(0)
    ElMessage.success('已全部标记为已读')
  } catch {
    // 拦截器已统一提示错误
  } finally {
    markingAll.value = false
  }
}

onMounted(() => {
  loadFirstPage()
  syncUnreadCount()
})
</script>

<template>
  <el-container class="notification-page">
    <main class="content">
      <div class="page-head">
        <h1>通知中心</h1>
        <span v-if="unreadCount > 0" class="unread-hint">未读 {{ unreadCount }} 条</span>
      </div>

      <div class="panel">
        <el-tabs v-model="activeFilter" class="filter-tabs">
          <el-tab-pane
            v-for="filter in FILTERS"
            :key="filter.key"
            :label="filter.label"
            :name="filter.key"
          />
        </el-tabs>

        <el-alert
          v-if="errorMsg"
          class="error"
          type="error"
          show-icon
          :title="errorMsg"
          closable
          @close="errorMsg = ''"
        />

        <div v-loading="loading" class="list">
          <el-empty
            v-if="!loading && filteredList.length === 0"
            :description="hasMore ? '当前分类暂无通知，可加载更多试试' : '暂无通知'"
          />

          <div
            v-for="item in filteredList"
            :key="item.ID"
            class="notification-item"
            :class="{ unread: !item.IsRead, cancelled: isCancelled(item) }"
          >
            <div class="item-main">
              <div class="item-head">
                <el-tag size="small" effect="plain" :type="typeMeta(item.Type).tag">
                  {{ typeMeta(item.Type).label }}
                </el-tag>
                <el-tag v-if="isCancelled(item)" size="small" type="info" effect="plain">
                  已取消
                </el-tag>
                <span class="item-time">{{ formatTime(item.CreatedAt) }}</span>
              </div>
              <div class="item-content">{{ item.Content }}</div>
            </div>

            <el-button
              class="read-btn"
              size="small"
              :type="item.IsRead ? 'info' : 'danger'"
              @click="markRead(item)"
            >
              已读
            </el-button>
          </div>

          <div v-if="hasMore" class="load-more">
            <el-button :loading="loadingMore" @click="loadMore">
              {{ loadingMore ? '加载中' : '加载更多' }}
            </el-button>
          </div>
          <div v-else-if="notifications.length > 0" class="no-more">没有更多了</div>
        </div>
      </div>

      <el-button
        class="read-all-btn"
        type="success"
        round
        :loading="markingAll"
        @click="markAllRead"
      >
        一键已读
      </el-button>
    </main>
  </el-container>
</template>

<style scoped>
.notification-page {
  min-height: 100%;
  background: #f5f5f5;
  display: flex;
  flex-direction: column;
}

.page-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 16px;
}

.page-head h1 {
  margin: 0;
  color: #303133;
  font-size: 22px;
}

.unread-hint {
  color: #f56c6c;
  font-size: 13px;
}

.content {
  flex: 1;
  width: 100%;
  max-width: 900px;
  margin: 0 auto;
  padding: 20px 24px 96px;
}

.panel {
  background: #fff;
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  overflow: hidden;
}

.filter-tabs {
  padding: 0 16px;
}

.filter-tabs :deep(.el-tabs__header) {
  margin: 0;
}

.filter-tabs :deep(.el-tabs__content) {
  display: none;
}

.error {
  margin: 12px 16px 0;
}

.list {
  min-height: 200px;
}

.notification-item {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px;
  border-bottom: 1px solid #f0f0f0;
}

.notification-item:last-child {
  border-bottom: none;
}

.notification-item.unread {
  background: #fef8f8;
}

/* 已取消的互动只作为历史展示，弱化视觉 */
.notification-item.cancelled {
  background: #fff;
}

.notification-item.cancelled .item-content {
  color: #c0c4cc;
}

.item-main {
  flex: 1;
  min-width: 0;
}

.item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.item-time {
  margin-left: auto;
  color: #c0c4cc;
  font-size: 12px;
}

.item-content {
  color: #303133;
  font-size: 14px;
  line-height: 1.5;
  word-break: break-word;
}

.notification-item.unread .item-content {
  font-weight: 500;
}

.read-btn {
  flex: none;
}

.load-more {
  padding: 20px 0;
  text-align: center;
}

.no-more {
  padding: 16px 0;
  color: #c0c4cc;
  font-size: 12px;
  text-align: center;
}

.read-all-btn {
  position: fixed;
  right: 32px;
  bottom: 32px;
  z-index: 20;
  min-width: 112px;
  box-shadow: 0 6px 16px rgba(103, 194, 58, 0.35);
}
</style>
