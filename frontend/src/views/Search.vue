<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import request from '../utils/request'
import { normalizeFeedItem, resolveAvatar } from '../utils/video'
import VideoCard from '../components/VideoCard.vue'

const route = useRoute()
const router = useRouter()

const keyword = computed(() => {
  const raw = Array.isArray(route.query.q) ? route.query.q[0] : route.query.q
  return typeof raw === 'string' ? raw.trim() : ''
})

const user = ref(null)
const videos = ref([])
const loading = ref(false)
const errorMsg = ref('')

const userAvatar = computed(() => resolveAvatar(user.value?.avatar_url))
const userInitial = computed(() => (user.value?.user_name || '?').slice(0, 1).toUpperCase())

const hasResult = computed(() => Boolean(user.value) || videos.value.length > 0)

let searchSeq = 0

function getErrorMessage(error) {
  return error.response?.data?.message || error.message || '搜索失败'
}

function matchesKeyword(video) {
  const query = keyword.value.toLowerCase()
  return (
    (video.title || '').toLowerCase().includes(query) ||
    (video.desc || '').toLowerCase().includes(query)
  )
}

async function runSearch() {
  const query = keyword.value
  const seq = ++searchSeq

  user.value = null
  videos.value = []
  errorMsg.value = ''
  if (!query) return

  loading.value = true
  try {
    // 后端没有独立的视频关键词搜索：用「标签流 + 最新流标题/简介过滤」组合实现。
    // 用户搜索为精确用户名匹配，查不到时静默降级，不打断搜索结果展示。
    // 允许用户带 @ / # 前缀输入，查询时去掉前缀更贴近实际用户名与标签。
    const userQuery = query.replace(/^@/, '')
    const tagQuery = query.replace(/^#/, '')

    const [userResult, tagResult, latestResult] = await Promise.all([
      request
        .post('/user/find_by_username', { user_name: userQuery }, { silent: true })
        .catch(() => null),
      request
        .get('/feed/listByTag', { params: { tag: tagQuery, limit: 20 }, silent: true })
        .catch(() => null),
      request
        .get('/feed/list', { params: { limit: 20 }, silent: true })
        .catch(() => null)
    ])

    if (seq !== searchSeq) return

    user.value = userResult?.data || null

    const byTag = (tagResult?.data?.list || []).map(normalizeFeedItem).filter(Boolean)
    const byKeyword = (latestResult?.data?.list || [])
      .map(normalizeFeedItem)
      .filter((video) => video && matchesKeyword(video))

    // 同一条视频在标签流和关键词过滤中可能重复，按 vid 去重并保持标签流优先
    const merged = new Map()
    for (const video of [...byTag, ...byKeyword]) {
      const key = String(video.vid)
      if (!merged.has(key)) merged.set(key, video)
    }
    videos.value = [...merged.values()]
  } catch (error) {
    if (seq === searchSeq) errorMsg.value = getErrorMessage(error)
  } finally {
    if (seq === searchSeq) loading.value = false
  }
}

function openUser(userId) {
  const id = Number(userId)
  if (id > 0) router.push(`/user/${id}`)
}

function onLikeChange({ vid, isLiked, likeCount }) {
  const item = videos.value.find((video) => String(video.vid) === String(vid))
  if (item) {
    item.isLiked = isLiked
    item.likeCount = likeCount
  }
}

watch(keyword, runSearch, { immediate: true })
</script>

<template>
  <div class="search-page">
    <header class="page-head">
      <h1>搜索结果</h1>
      <p v-if="keyword" class="query-text">“{{ keyword }}”</p>
    </header>

    <el-alert
      v-if="errorMsg"
      class="error"
      type="error"
      show-icon
      :title="errorMsg"
      closable
      @close="errorMsg = ''"
    />

    <div v-loading="loading" class="search-body">
      <template v-if="!loading">
        <el-empty
          v-if="!keyword"
          description="输入关键词，搜索用户或视频"
          :image-size="90"
        />

        <el-empty
          v-else-if="!hasResult"
          description="没有找到相关用户或视频，换个关键词试试"
          :image-size="90"
        />

        <template v-else>
          <section v-if="user" class="result-section">
            <h2 class="section-title">用户</h2>
            <div class="user-card" @click="openUser(user.id)">
              <el-avatar :size="56" :src="userAvatar">{{ userInitial }}</el-avatar>
              <div class="user-info">
                <div class="user-name">{{ user.user_name }}</div>
                <div class="user-bio">{{ user.bio || '这个人很懒，还没有填写简介' }}</div>
              </div>
              <span class="user-arrow">查看主页 ›</span>
            </div>
          </section>

          <section v-if="videos.length" class="result-section">
            <h2 class="section-title">视频 <span class="count">{{ videos.length }}</span></h2>
            <div class="video-grid">
              <VideoCard
                v-for="video in videos"
                :key="video.vid"
                :video="video"
                @like-change="onLikeChange"
              />
            </div>
          </section>
        </template>
      </template>
    </div>
  </div>
</template>

<style scoped>
.search-page {
  width: 100%;
  max-width: 1200px;
  margin: 0 auto;
  padding: 24px;
}

.page-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 18px;
}

.page-head h1 {
  margin: 0;
  color: var(--app-text);
  font-size: 22px;
}

.query-text {
  margin: 0;
  color: var(--app-text-muted);
  font-size: 14px;
}

.error {
  margin-bottom: 16px;
}

.search-body {
  min-height: 240px;
}

.result-section + .result-section {
  margin-top: 28px;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 14px;
  color: var(--app-text);
  font-size: 16px;
}

.count {
  color: var(--app-text-muted);
  font-size: 13px;
  font-weight: 400;
}

.user-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 16px 20px;
  background: var(--app-surface);
  border: 1px solid var(--app-border);
  border-radius: 10px;
  cursor: pointer;
  transition: background-color 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease;
}

.user-card:hover {
  border-color: var(--app-primary);
  box-shadow: 0 4px 16px var(--app-shadow);
}

.user-info {
  flex: 1;
  min-width: 0;
}

.user-name {
  color: var(--app-text);
  font-size: 16px;
  font-weight: 600;
}

.user-bio {
  margin-top: 4px;
  overflow: hidden;
  color: var(--app-text-muted);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-arrow {
  flex: none;
  color: var(--app-primary);
  font-size: 13px;
}

.video-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 20px;
}
</style>
