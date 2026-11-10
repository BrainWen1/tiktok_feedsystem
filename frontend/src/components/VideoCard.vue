<script setup>
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import request from '../utils/request'
import { resolveAsset } from '../utils/asset'
import { DEFAULT_AVATAR } from '../utils/video'

const props = defineProps({
  // 统一后的视频对象，见 utils/video.js 的 normalizeFeedItem / normalizeVideoDetail
  video: {
    type: Object,
    required: true
  }
})

const emit = defineEmits(['like-change'])

const router = useRouter()

const liked = ref(Boolean(props.video.isLiked))
const likeCount = ref(Number(props.video.likeCount) || 0)
const likePending = ref(false)

// 父组件同步点赞状态后回填本地状态
watch(
  () => [props.video.isLiked, props.video.likeCount],
  ([isLiked, count]) => {
    liked.value = Boolean(isLiked)
    likeCount.value = Number(count) || 0
  }
)

const avatarSrc = computed(() => resolveAsset(props.video.avatarUrl) || DEFAULT_AVATAR)
const initial = computed(() => (props.video.authorName || '?').slice(0, 1).toUpperCase())
const canOpenAuthor = computed(() => Number(props.video.authorId) > 0)

function openVideo() {
  const vid = props.video.vid
  if (vid === undefined || vid === null || vid === '') return
  router.push(`/video/${vid}`)
}

function openAuthor() {
  if (!canOpenAuthor.value) return
  router.push(`/user/${props.video.authorId}`)
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
  } catch (error) {
    liked.value = !nextLiked
    likeCount.value = prevCount
  } finally {
    likePending.value = false
  }
}
</script>

<template>
  <el-card class="video-card" shadow="hover" @click="openVideo">
    <el-image class="cover" :src="resolveAsset(video.coverUrl)" fit="cover" lazy>
      <template #error>
        <div class="cover-fallback">暂无封面</div>
      </template>
    </el-image>

    <div class="card-title" :title="video.title || '无标题'">
      {{ video.title || '无标题' }}
    </div>

    <div class="card-meta">
      <div
        class="author"
        :class="{ clickable: canOpenAuthor }"
        :title="video.authorName || '匿名用户'"
        @click.stop="openAuthor"
      >
        <el-avatar class="author-avatar" :size="28" :src="avatarSrc">
          {{ initial }}
        </el-avatar>
        <span class="author-name">@{{ video.authorName || '匿名用户' }}</span>
      </div>

      <button
        class="like-btn"
        :class="{ liked }"
        :disabled="likePending"
        :title="liked ? '取消点赞' : '点赞'"
        @click.stop="toggleLike"
      >
        <svg class="heart" viewBox="0 0 24 24" aria-hidden="true">
          <path
            d="M12 21s-7.5-4.9-9.5-9.2C1 8.3 3.2 4.5 7 4.5c2 0 3.4 1.1 4.3 2.4l.7 1 .7-1c.9-1.3 2.3-2.4 4.3-2.4 3.8 0 6 3.8 4.5 7.3C19.5 16.1 12 21 12 21z"
            fill="currentColor"
          />
        </svg>
        {{ likeCount }}
      </button>
    </div>
  </el-card>
</template>

<style scoped>
.video-card {
  display: flex;
  flex-direction: column;
  cursor: pointer;
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

.card-title {
  margin-top: 10px;
  overflow: hidden;
  color: #303133;
  font-size: 14px;
  font-weight: 500;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 10px;
}

.author {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: #606266;
  font-size: 13px;
}

.author.clickable:hover .author-name {
  color: #409eff;
}

.author-avatar {
  flex: none;
  background: #409eff;
}

.author-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.like-btn {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 4px;
  padding: 0;
  color: #909399;
  font-size: 13px;
  background: none;
  border: none;
  cursor: pointer;
}

.like-btn:disabled {
  cursor: default;
}

.like-btn.liked {
  color: #fe2c55;
}

.heart {
  width: 15px;
  height: 15px;
}
</style>
