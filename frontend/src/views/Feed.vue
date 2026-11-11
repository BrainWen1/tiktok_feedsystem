<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Loading } from '@element-plus/icons-vue'
import request from '../utils/request'
import { normalizeFeedItem } from '../utils/video'
import VideoSlide from '../components/VideoSlide.vue'
import CommentPanel from '../components/CommentPanel.vue'

const props = defineProps({
  mode: {
    type: String,
    default: 'recommend'
  }
})

const PAGE_SIZE = 8

const router = useRouter()

const videos = ref([])
const nextCursor = ref(null)
const loading = ref(false)
const loadingMore = ref(false)
const errorMsg = ref('')
const activeIndex = ref(0)
const muted = ref(true)

const scroller = ref(null)
const commentVisible = ref(false)
const commentVideoId = ref(0)

let observer = null
// 每张 slide 的最新可见比例，IntersectionObserver 回调只给变化项，这里维护全量快照
const ratios = new Map()

const isFollowing = () => props.mode === 'following'

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
  const endpoint = isFollowing() ? '/feed/listByFollowing' : '/feed/list'
  const res = await request.get(endpoint, {
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
    activeIndex.value = 0

    await nextTick()
    scroller.value?.scrollTo({ top: 0 })
    setupObserver()
    if (videos.value.length <= 3) loadMore()
  } catch (error) {
    videos.value = []
    nextCursor.value = null
    errorMsg.value = getErrorMessage(error)
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loadingMore.value || loading.value) return

  loadingMore.value = true
  try {
    const payload = await fetchFeed(nextCursor.value)
    const list = (payload.list || []).map(normalizeFeedItem).filter(Boolean)
    const existing = new Set(videos.value.map((video) => String(video.vid)))
    videos.value = videos.value.concat(
      list.filter((video) => !existing.has(String(video.vid)))
    )
    nextCursor.value = payload.next_cursor || null
    if (list.length < PAGE_SIZE) nextCursor.value = null

    await nextTick()
    setupObserver()
  } catch (error) {
    errorMsg.value = getErrorMessage(error)
  } finally {
    loadingMore.value = false
  }
}

// 监听 slide 的可见比例，只有过半进入视口的视频才算当前播放项
function setupObserver() {
  observer?.disconnect()
  ratios.clear()

  if (!scroller.value) return

  observer = new IntersectionObserver(
    (entries) => {
      let bestIndex = activeIndex.value
      let bestRatio = 0

      for (const entry of entries) {
        ratios.set(entry.target, entry.intersectionRatio)
      }
      for (const [el, ratio] of ratios) {
        if (ratio > bestRatio) {
          bestRatio = ratio
          bestIndex = Number(el.dataset.index)
        }
      }

      if (bestRatio >= 0.5 && Number.isInteger(bestIndex)) {
        activeIndex.value = bestIndex
      }
    },
    { root: scroller.value, threshold: [0, 0.5, 0.75, 1] }
  )

  scroller.value
    .querySelectorAll('.feed-slide')
    .forEach((slide) => observer.observe(slide))
}

function goToSlide(index) {
  const slides = scroller.value?.querySelectorAll('.feed-slide')
  if (!slides?.length) return
  const target = Math.max(0, Math.min(index, slides.length - 1))
  scroller.value.scrollTo({ top: slides[target].offsetTop, behavior: 'smooth' })
}

function isTypingTarget(target) {
  const tag = target?.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target?.isContentEditable
}

// 方向键 / PageUp / PageDown 逐条切换视频；输入框内不拦截
function onKeydown(event) {
  if (commentVisible.value || isTypingTarget(event.target)) return

  if (event.key === 'ArrowDown' || event.key === 'PageDown') {
    event.preventDefault()
    goToSlide(activeIndex.value + 1)
  } else if (event.key === 'ArrowUp' || event.key === 'PageUp') {
    event.preventDefault()
    goToSlide(activeIndex.value - 1)
  }
}

function onLikeChange({ vid, isLiked, likeCount }) {
  const item = videos.value.find((video) => String(video.vid) === String(vid))
  if (item) {
    item.isLiked = isLiked
    item.likeCount = likeCount
  }
}

function openComments(video) {
  commentVideoId.value = Number(video.vid)
  commentVisible.value = true
}

function onCommentCountChange({ videoId, total }) {
  const item = videos.value.find((video) => String(video.vid) === String(videoId))
  if (item) item.commentsCount = total
}

function toggleMute() {
  muted.value = !muted.value
}

function goRecommend() {
  router.push('/')
}

// 距离列表末尾 3 条时预加载下一页，保证连续下滑不中断
watch(activeIndex, (index) => {
  if (index >= videos.value.length - 3) loadMore()
})

// 同组件在两个 Feed 路由间复用时兜底重置（正常情况下路由 key 会重新挂载）
watch(
  () => props.mode,
  () => {
    videos.value = []
    nextCursor.value = null
    activeIndex.value = 0
    loadFirstPage()
  }
)

onMounted(() => {
  loadFirstPage()
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="feed-page">
    <div ref="scroller" class="feed-scroller">
      <div
        v-for="(video, index) in videos"
        :key="video.vid"
        class="feed-slide"
        :data-index="index"
      >
        <VideoSlide
          :video="video"
          :active="index === activeIndex"
          :muted="muted"
          @like-change="onLikeChange"
          @open-comments="openComments(video)"
          @toggle-mute="toggleMute"
        />
      </div>
    </div>

    <div v-if="loading && videos.length === 0" class="feed-status">
      <el-icon class="is-loading" :size="28"><Loading /></el-icon>
      <span>正在加载视频…</span>
    </div>

    <div v-else-if="errorMsg && videos.length === 0" class="feed-status">
      <p class="status-title">视频流加载失败</p>
      <p class="status-desc">{{ errorMsg }}</p>
      <el-button type="primary" round @click="loadFirstPage">重试</el-button>
    </div>

    <div v-else-if="!loading && videos.length === 0" class="feed-status">
      <p class="status-title">
        {{ isFollowing() ? '还没有关注任何博主' : '暂无视频' }}
      </p>
      <p class="status-desc">
        {{ isFollowing() ? '去推荐流看看，关注喜欢的创作者吧' : '等待创作者发布第一条视频' }}
      </p>
      <el-button v-if="isFollowing()" type="primary" round @click="goRecommend">
        去推荐流
      </el-button>
    </div>

    <div v-if="loadingMore && videos.length > 0" class="load-more-tip">
      <el-icon class="is-loading" :size="14"><Loading /></el-icon>
      加载更多
    </div>

    <CommentPanel
      v-model:visible="commentVisible"
      :video-id="commentVideoId"
      @count-change="onCommentCountChange"
    />
  </div>
</template>

<style scoped>
.feed-page {
  position: relative;
  height: 100%;
  color: #fff;
  background: #000;
}

.feed-scroller {
  height: 100%;
  overflow-y: auto;
  scroll-snap-type: y mandatory;
  scrollbar-width: none;
}

.feed-scroller::-webkit-scrollbar {
  display: none;
}

.feed-slide {
  height: 100vh;
  height: 100dvh;
  scroll-snap-align: start;
  scroll-snap-stop: always;
}

.feed-status {
  position: absolute;
  top: 50%;
  left: 50%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  width: min(420px, calc(100% - 48px));
  padding: 28px;
  color: rgba(255, 255, 255, 0.85);
  text-align: center;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 14px;
  transform: translate(-50%, -50%);
}

.status-title {
  margin: 0;
  color: #fff;
  font-size: 18px;
  font-weight: 600;
}

.status-desc {
  margin: 0 0 6px;
  font-size: 13px;
  line-height: 1.6;
}

.load-more-tip {
  position: absolute;
  top: 76px;
  right: 24px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  color: rgba(255, 255, 255, 0.85);
  font-size: 12px;
  background: rgba(0, 0, 0, 0.45);
  border-radius: 14px;
  pointer-events: none;
}
</style>
