<script setup>
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ChatDotRound } from '@element-plus/icons-vue'
import request from '../utils/request'
import { resolveAsset } from '../utils/asset'
import { DEFAULT_AVATAR, formatDuration, formatPublishTime } from '../utils/video'

const props = defineProps({
  video: {
    type: Object,
    required: true
  },
  // 当前是否处于视口内：只有激活的视频才允许自动播放
  active: {
    type: Boolean,
    default: false
  },
  muted: {
    type: Boolean,
    default: true
  }
})

const emit = defineEmits(['like-change', 'open-comments', 'toggle-mute'])

const router = useRouter()

const videoEl = ref(null)
const playing = ref(false)
// 用户主动暂停后，即使触发 canplay 也不自动恢复播放
const userPaused = ref(false)
const currentTime = ref(0)
const duration = ref(0)

const liked = ref(Boolean(props.video.isLiked))
const likeCount = ref(Number(props.video.likeCount) || 0)
const likePending = ref(false)

watch(
  () => [props.video.isLiked, props.video.likeCount],
  ([isLiked, count]) => {
    liked.value = Boolean(isLiked)
    likeCount.value = Number(count) || 0
  }
)

const avatarSrc = computed(() => resolveAsset(props.video.avatarUrl) || DEFAULT_AVATAR)
const initial = computed(() => (props.video.authorName || '?').slice(0, 1).toUpperCase())
const displayTitle = computed(() => props.video.title || props.video.desc || '')
const displayDesc = computed(() => {
  const desc = props.video.desc || ''
  return desc && desc !== displayTitle.value ? desc : ''
})
const publishTime = computed(() => formatPublishTime(props.video.createdAt))
const progressPercent = computed(() => {
  if (!duration.value) return 0
  return Math.min(100, (currentTime.value / duration.value) * 100)
})

// 后端已返回标签；兼容旧数据时从标题/简介里的 #xxx 兜底解析
const tags = computed(() => {
  if (Array.isArray(props.video.tags) && props.video.tags.length) return props.video.tags
  const matches = `${props.video.title || ''} ${props.video.desc || ''}`.match(/#[^\s#]+/g) || []
  return matches.map((tag) => tag.slice(1))
})

function openAuthor() {
  const id = Number(props.video.authorId)
  if (id > 0) router.push(`/user/${id}`)
}

function onMetadata() {
  const el = videoEl.value
  if (!el) return
  duration.value = Number.isFinite(el.duration) ? el.duration : 0
  currentTime.value = el.currentTime || 0
}

function onTimeUpdate() {
  const el = videoEl.value
  if (!el) return
  currentTime.value = el.currentTime || 0
  duration.value = Number.isFinite(el.duration) ? el.duration : 0
}

async function syncPlayback() {
  const el = videoEl.value
  if (!el) return

  el.muted = props.muted
  if (props.active) {
    userPaused.value = false
    try {
      await el.play()
    } catch {
      // 浏览器可能拦截自动播放，保持暂停态，用户点击后即可播放
      playing.value = false
    }
  } else {
    el.pause()
  }
}

function togglePlay() {
  if (!props.active) return
  const el = videoEl.value
  if (!el) return
  if (el.paused) {
    userPaused.value = false
    el.play().catch(() => {})
  } else {
    userPaused.value = true
    el.pause()
  }
}

function seek(event) {
  const el = videoEl.value
  if (!el || !duration.value) return

  const rect = event.currentTarget.getBoundingClientRect()
  const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width))
  el.currentTime = ratio * duration.value
  currentTime.value = el.currentTime
}

async function toggleLike() {
  if (likePending.value) return

  const nextLiked = !liked.value
  const prevCount = likeCount.value
  // 先改本地状态保证点击即时反馈，请求失败再回滚
  liked.value = nextLiked
  likeCount.value = Math.max(0, prevCount + (nextLiked ? 1 : -1))
  likePending.value = true

  try {
    await request.post(nextLiked ? '/like/like' : '/like/unlike', {
      video_id: Number(props.video.vid)
    })
    emit('like-change', {
      vid: props.video.vid,
      isLiked: liked.value,
      likeCount: likeCount.value
    })
  } catch {
    liked.value = !nextLiked
    likeCount.value = prevCount
  } finally {
    likePending.value = false
  }
}

function openComments() {
  emit('open-comments')
}

function onCanPlay() {
  // 视频元数据/首帧就绪后，如果已经滑到这一屏且用户没有主动暂停，则继续自动播放
  if (props.active && !userPaused.value && videoEl.value?.paused) syncPlayback()
}

watch(
  () => props.active,
  () => nextTick(syncPlayback)
)

watch(
  () => props.muted,
  (value) => {
    if (videoEl.value) videoEl.value.muted = value
  }
)

onMounted(() => nextTick(syncPlayback))
</script>

<template>
  <div class="video-slide">
    <video
      ref="videoEl"
      class="player"
      :src="resolveAsset(video.videoUrl)"
      :poster="resolveAsset(video.coverUrl)"
      :muted="muted"
      loop
      playsinline
      preload="metadata"
      @loadedmetadata="onMetadata"
      @timeupdate="onTimeUpdate"
      @play="playing = true"
      @pause="playing = false"
      @canplay="onCanPlay"
      @click="togglePlay"
    />

    <div class="shade shade-top" />
    <div class="shade shade-bottom" />

    <div v-if="active && !playing" class="center-play">
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M8 5.5v13l11-6.5z" fill="currentColor" />
      </svg>
    </div>

    <aside class="rail">
      <button class="avatar-btn" type="button" title="查看作者主页" @click="openAuthor">
        <el-avatar :size="52" :src="avatarSrc">{{ initial }}</el-avatar>
      </button>

      <button
        class="rail-action"
        :class="{ liked }"
        type="button"
        :disabled="likePending"
        :title="liked ? '取消点赞' : '点赞'"
        @click="toggleLike"
      >
        <svg class="rail-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path
            d="M12 21s-7.5-4.9-9.5-9.2C1 8.3 3.2 4.5 7 4.5c2 0 3.4 1.1 4.3 2.4l.7 1 .7-1c.9-1.3 2.3-2.4 4.3-2.4 3.8 0 6 3.8 4.5 7.3C19.5 16.1 12 21 12 21z"
            :fill="liked ? 'currentColor' : 'rgba(255,255,255,0.22)'"
            stroke="currentColor"
            stroke-width="1.6"
          />
        </svg>
        <span class="rail-count">{{ likeCount }}</span>
      </button>

      <button class="rail-action" type="button" title="查看评论" @click="openComments">
        <el-icon class="rail-icon" :size="32"><ChatDotRound /></el-icon>
        <span class="rail-count">{{ video.commentsCount || 0 }}</span>
      </button>
    </aside>

    <div class="bottom-info">
      <div class="author-line">
        <button class="author-name" type="button" @click="openAuthor">
          @{{ video.authorName || '匿名用户' }}
        </button>
        <span v-if="publishTime" class="publish-time">· {{ publishTime }}</span>
      </div>

      <p v-if="displayTitle" class="video-title">{{ displayTitle }}</p>
      <p v-if="displayDesc" class="video-desc">{{ displayDesc }}</p>

      <div v-if="tags.length" class="tag-row">
        <span v-for="tag in tags" :key="tag" class="tag">#{{ tag }}</span>
      </div>
    </div>

    <div class="control-bar">
      <button
        class="round-control play-control"
        type="button"
        :title="playing ? '暂停' : '播放'"
        @click="togglePlay"
      >
        <svg v-if="playing" viewBox="0 0 24 24" aria-hidden="true">
          <rect x="6.5" y="5" width="4" height="14" rx="1" fill="currentColor" />
          <rect x="13.5" y="5" width="4" height="14" rx="1" fill="currentColor" />
        </svg>
        <svg v-else viewBox="0 0 24 24" aria-hidden="true">
          <path d="M8 5.5v13l11-6.5z" fill="currentColor" />
        </svg>
      </button>

      <div class="progress-track" title="点击跳转播放进度" @click="seek">
        <div class="progress-fill" :style="{ width: `${progressPercent}%` }">
          <span class="progress-knob" />
        </div>
      </div>

      <span class="time-text">
        {{ formatDuration(currentTime) }} / {{ formatDuration(duration) }}
      </span>
    </div>

    <button
      class="volume-btn"
      type="button"
      :title="muted ? '取消静音' : '静音'"
      @click="emit('toggle-mute')"
    >
      <svg v-if="muted" viewBox="0 0 24 24" aria-hidden="true">
        <path d="M4 9v6h4l5 4V5L8 9H4z" fill="currentColor" />
        <path
          d="M16 9.5l5 5m0-5l-5 5"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
        />
      </svg>
      <svg v-else viewBox="0 0 24 24" aria-hidden="true">
        <path d="M4 9v6h4l5 4V5L8 9H4z" fill="currentColor" />
        <path
          d="M16 8.5a5 5 0 010 7M18.5 6a8.5 8.5 0 010 12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
        />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.video-slide {
  position: relative;
  width: 100%;
  height: 100vh;
  height: 100dvh;
  overflow: hidden;
  background: #000;
}

.player {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
  cursor: pointer;
}

.shade {
  position: absolute;
  right: 0;
  left: 0;
  pointer-events: none;
}

.shade-top {
  top: 0;
  height: 110px;
  background: linear-gradient(rgba(0, 0, 0, 0.45), transparent);
}

.shade-bottom {
  bottom: 0;
  height: 46%;
  background: linear-gradient(transparent, rgba(0, 0, 0, 0.72));
}

.center-play {
  position: absolute;
  top: 50%;
  left: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 74px;
  height: 74px;
  color: rgba(255, 255, 255, 0.88);
  background: rgba(0, 0, 0, 0.38);
  border-radius: 50%;
  transform: translate(-50%, -50%);
  cursor: pointer;
  pointer-events: none;
}

.center-play svg {
  width: 38px;
  height: 38px;
  margin-left: 4px;
}

/* ---------- 右侧操作栏 ---------- */
.rail {
  position: absolute;
  right: 22px;
  bottom: 128px;
  z-index: 3;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 22px;
}

.avatar-btn {
  display: flex;
  padding: 0;
  background: none;
  border: 2px solid rgba(255, 255, 255, 0.9);
  border-radius: 50%;
  cursor: pointer;
}

.avatar-btn:hover {
  border-color: var(--app-primary);
}

.rail-action {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 0;
  color: var(--app-video-text);
  background: none;
  border: none;
  cursor: pointer;
}

.rail-action:disabled {
  cursor: default;
}

.rail-icon {
  width: 34px;
  height: 34px;
  filter: drop-shadow(0 2px 3px rgba(0, 0, 0, 0.35));
}

.rail-action.liked {
  color: var(--app-primary);
}

.rail-count {
  color: var(--app-video-text);
  font-size: 13px;
  font-weight: 500;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.6);
}

/* ---------- 左下角信息 ---------- */
.bottom-info {
  position: absolute;
  bottom: 88px;
  left: 24px;
  z-index: 3;
  max-width: min(620px, calc(100% - 150px));
  color: var(--app-video-text);
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.55);
  transition: color 0.25s ease;
}

.author-line {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.author-name {
  padding: 0;
  color: var(--app-video-text);
  font-size: 17px;
  font-weight: 700;
  background: none;
  border: none;
  cursor: pointer;
}

.author-name:hover {
  text-decoration: underline;
}

.publish-time {
  color: var(--app-video-text-muted);
  font-size: 13px;
}

.video-title {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.45;
  word-break: break-word;
}

.video-desc {
  display: -webkit-box;
  margin: 0 0 6px;
  overflow: hidden;
  color: var(--app-video-text-secondary);
  font-size: 14px;
  line-height: 1.5;
  word-break: break-word;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}

.tag-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.tag {
  color: var(--app-video-tag);
  font-size: 13px;
  font-weight: 500;
}

/* ---------- 底部控制条 ---------- */
.control-bar {
  position: absolute;
  right: 82px;
  bottom: 18px;
  left: 20px;
  z-index: 4;
  display: flex;
  align-items: center;
  gap: 12px;
  height: 44px;
  padding: 0 14px;
  background: var(--app-video-control-bg);
  border-radius: 22px;
  backdrop-filter: blur(6px);
  transition: background-color 0.25s ease;
}

.round-control,
.volume-btn {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  color: var(--app-video-text);
  background: var(--app-video-button-bg);
  border: none;
  border-radius: 50%;
  cursor: pointer;
  transition: background-color 0.2s ease;
}

.round-control {
  width: 30px;
  height: 30px;
}

.round-control svg {
  width: 17px;
  height: 17px;
}

.round-control:hover,
.volume-btn:hover {
  background: var(--app-video-button-hover);
}

/* 取消静音按钮固定在视频右下角，圆形独立按钮 */
.volume-btn {
  position: absolute;
  right: 20px;
  bottom: 18px;
  z-index: 5;
  width: 44px;
  height: 44px;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.35);
}

.volume-btn svg {
  width: 22px;
  height: 22px;
}

.progress-track {
  position: relative;
  flex: 1;
  height: 16px;
  cursor: pointer;
}

.progress-track::before {
  position: absolute;
  top: 7px;
  right: 0;
  left: 0;
  height: 3px;
  background: var(--app-video-track);
  border-radius: 2px;
  content: '';
}

.progress-fill {
  position: absolute;
  top: 7px;
  left: 0;
  height: 3px;
  background: var(--app-primary);
  border-radius: 2px;
}

.progress-knob {
  position: absolute;
  top: 50%;
  right: -5px;
  width: 10px;
  height: 10px;
  background: var(--app-video-text);
  border-radius: 50%;
  transform: translateY(-50%);
}

.time-text {
  flex: none;
  color: var(--app-video-text-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 900px) {
  .rail {
    right: 14px;
    bottom: 150px;
  }

  .bottom-info {
    bottom: 104px;
    left: 16px;
    max-width: calc(100% - 100px);
  }

  .control-bar {
    right: 72px;
    left: 12px;
    gap: 8px;
  }

  .volume-btn {
    right: 14px;
  }

  .time-text {
    display: none;
  }
}
</style>
