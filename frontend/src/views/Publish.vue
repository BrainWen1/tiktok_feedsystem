<script setup>
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../utils/request'

const router = useRouter()

// 与后端 UploadSpec / 分片上传限制保持一致
const ALLOWED_VIDEO_EXTS = ['.mp4', '.avi', '.mov', '.mkv']
const ALLOWED_IMAGE_EXTS = ['.jpg', '.jpeg', '.png', '.webp']
const MAX_VIDEO_SIZE = 2 * 1024 * 1024 * 1024 // 分片上传成品文件上限 2GB
const MAX_COVER_SIZE = 10 * 1024 * 1024
const DEFAULT_CHUNK_SIZE = 5 * 1024 * 1024
// 自动截取的封面最长边，按原始比例缩放，避免 4K 视频生成超大封面
const MAX_COVER_SIDE = 1920

const videoInputRef = ref(null)
const coverInputRef = ref(null)

const videoFile = ref(null)
const autoCoverBlob = ref(null)
const autoCoverUrl = ref('')
const customCoverFile = ref(null)
const customCoverUrl = ref('')

const extracting = ref(false)
const title = ref('')
const description = ref('')
const tagsText = ref('')

const publishing = ref(false)
const publishFailed = ref(false)
const progress = ref(0)
const progressText = ref('')

// 每次选择视频自增，丢弃过期视频的截帧结果
let captureSeq = 0

const coverPreviewUrl = computed(() => customCoverUrl.value || autoCoverUrl.value)
const coverSourceText = computed(() => {
  if (customCoverUrl.value) return '当前使用自定义封面'
  if (autoCoverUrl.value) return '当前使用视频第 1 秒画面（自动截取）'
  return '未选择图片时，将使用自动截取的视频画面'
})
const canPublish = computed(
  () =>
    Boolean(videoFile.value && coverPreviewUrl.value && title.value.trim()) &&
    !publishing.value &&
    !extracting.value
)

function extOf(name) {
  const index = name.lastIndexOf('.')
  return index >= 0 ? name.slice(index).toLowerCase() : ''
}

function formatSize(bytes) {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${bytes} B`
}

function revokeAutoCover() {
  if (autoCoverUrl.value) URL.revokeObjectURL(autoCoverUrl.value)
  autoCoverUrl.value = ''
  autoCoverBlob.value = null
}

function revokeCustomCover() {
  if (customCoverUrl.value) URL.revokeObjectURL(customCoverUrl.value)
  customCoverUrl.value = ''
  customCoverFile.value = null
}

// 使用隐藏 video 加载本地文件，seek 到第 1 秒后绘制到 canvas，再转成图片 Blob。
// Blob URL 与页面同源，canvas 不会被标记为跨域污染。
function extractVideoFrame(file) {
  return new Promise((resolve, reject) => {
    const video = document.createElement('video')
    video.style.display = 'none'
    video.muted = true
    video.playsInline = true
    video.preload = 'auto'
    document.body.appendChild(video)

    const objectUrl = URL.createObjectURL(file)
    let settled = false

    const finish = (callback, value) => {
      if (settled) return
      settled = true
      video.onloadedmetadata = null
      video.onseeked = null
      video.onerror = null
      URL.revokeObjectURL(objectUrl)
      video.removeAttribute('src')
      video.load()
      video.remove()
      callback(value)
    }

    video.onloadedmetadata = () => {
      const duration = Number(video.duration)
      // 优先取第 1 秒，短视频退回到接近开头的位置，避免黑帧
      video.currentTime = Number.isFinite(duration) && duration > 1 ? 1 : 0.1
    }

    video.onseeked = () => {
      try {
        const sourceWidth = video.videoWidth
        const sourceHeight = video.videoHeight
        if (!sourceWidth || !sourceHeight) {
          finish(reject, new Error('无法获取视频画面尺寸'))
          return
        }

        // 等比缩放，canvas 尺寸与视频原始比例一致，drawImage 不拉伸
        const scale = Math.min(1, MAX_COVER_SIDE / Math.max(sourceWidth, sourceHeight))
        const canvas = document.createElement('canvas')
        canvas.width = Math.max(1, Math.round(sourceWidth * scale))
        canvas.height = Math.max(1, Math.round(sourceHeight * scale))

        const ctx = canvas.getContext('2d')
        if (!ctx) {
          finish(reject, new Error('当前浏览器不支持 Canvas'))
          return
        }
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height)

        canvas.toBlob(
          (blob) => {
            if (!blob) {
              finish(reject, new Error('封面生成失败'))
              return
            }
            finish(resolve, blob)
          },
          'image/jpeg',
          0.9
        )
      } catch (error) {
        finish(reject, error)
      }
    }

    video.onerror = () => finish(reject, new Error('视频解析失败，请确认文件可以正常播放'))
    video.src = objectUrl
    video.load()
  })
}

async function onVideoChange(event) {
  const file = event.target.files?.[0]
  // 清空 value，允许用户重新选择同一个文件
  event.target.value = ''
  if (!file) return

  if (!ALLOWED_VIDEO_EXTS.includes(extOf(file.name))) {
    ElMessage.warning('仅支持 mp4 / avi / mov / mkv 格式的视频')
    return
  }
  if (file.size <= 0) {
    ElMessage.warning('视频文件为空，请重新选择')
    return
  }
  if (file.size > MAX_VIDEO_SIZE) {
    ElMessage.warning(`视频不能超过 ${formatSize(MAX_VIDEO_SIZE)}`)
    return
  }

  videoFile.value = file
  revokeAutoCover()

  const seq = ++captureSeq
  extracting.value = true
  progress.value = 0
  progressText.value = ''
  publishFailed.value = false

  try {
    const blob = await extractVideoFrame(file)
    if (seq !== captureSeq) return
    autoCoverBlob.value = blob
    autoCoverUrl.value = URL.createObjectURL(blob)
  } catch (error) {
    if (seq !== captureSeq) return
    ElMessage.warning(error.message || '封面自动截取失败，请手动选择封面图片')
  } finally {
    if (seq === captureSeq) extracting.value = false
  }
}

function onCoverChange(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return

  if (!ALLOWED_IMAGE_EXTS.includes(extOf(file.name))) {
    ElMessage.warning('封面仅支持 jpg / jpeg / png / webp 格式')
    return
  }
  if (file.size <= 0 || file.size > MAX_COVER_SIZE) {
    ElMessage.warning(`封面大小需在 10MB 以内`)
    return
  }

  revokeCustomCover()
  customCoverFile.value = file
  customCoverUrl.value = URL.createObjectURL(file)
}

function clearCustomCover() {
  revokeCustomCover()
  if (coverInputRef.value) coverInputRef.value.value = ''
}

// 分片上传：create -> 逐片 upload -> merge，返回后端可访问的 video_url。
// 任一步失败都会尽力取消会话，避免分片长期占用存储。
async function uploadVideoChunked(file) {
  progressText.value = '创建上传会话…'
  const created = await request.post('/video/chunk/create', {
    filename: file.name,
    file_size: file.size
  })

  const session = created.data || {}
  const uploadId = session.upload_id
  if (!uploadId) throw new Error('创建上传会话失败')

  const chunkSize = Number(session.chunk_size) || DEFAULT_CHUNK_SIZE
  const totalChunks = Number(session.total_chunks) || Math.ceil(file.size / chunkSize)

  try {
    for (let index = 0; index < totalChunks; index += 1) {
      const start = index * chunkSize
      const end = Math.min(start + chunkSize, file.size)
      const chunk = file.slice(start, end)

      progressText.value = `上传视频分片 ${index + 1}/${totalChunks}`
      await request.post(
        `/video/chunk/upload?upload_id=${encodeURIComponent(uploadId)}&index=${index}`,
        chunk,
        {
          headers: { 'Content-Type': 'application/octet-stream' },
          timeout: 120000
        }
      )
      progress.value = Math.round(((index + 1) / totalChunks) * 90)
    }

    progressText.value = '合并视频分片…'
    const merged = await request.post(
      `/video/chunk/merge?upload_id=${encodeURIComponent(uploadId)}`,
      null,
      { timeout: 600000 }
    )
    const videoUrl = merged.data?.video_url
    if (!videoUrl) throw new Error('合并视频失败')
    return videoUrl
  } catch (error) {
    await cancelChunkSession(uploadId)
    throw error
  }
}

async function cancelChunkSession(uploadId) {
  try {
    await request.post(`/video/chunk/cancel?upload_id=${encodeURIComponent(uploadId)}`)
  } catch (error) {
    // 清理失败不影响主流程错误提示
    console.warn('cancel chunk upload failed', error)
  }
}

async function uploadCover() {
  const formData = new FormData()
  if (customCoverFile.value) {
    formData.append('file', customCoverFile.value, customCoverFile.value.name)
  } else {
    formData.append('file', autoCoverBlob.value, 'cover.jpg')
  }

  const res = await request.post('/video/upload_cover', formData, { timeout: 60000 })
  const coverUrl = res.data?.cover_url
  if (!coverUrl) throw new Error('封面上传失败')
  return coverUrl
}

async function publishVideo(videoUrl, coverUrl) {
  const tags = tagsText.value
    .split(/[,，\s]+/)
    .map((tag) => tag.trim())
    .filter(Boolean)

  await request.post('/video/publish', {
    title: title.value.trim(),
    description: description.value.trim(),
    video_url: videoUrl,
    cover_url: coverUrl,
    tags
  })
}

async function handlePublish() {
  if (publishing.value) return
  if (!videoFile.value) {
    ElMessage.warning('请先选择视频文件')
    return
  }
  if (!coverPreviewUrl.value) {
    ElMessage.warning('封面尚未就绪，请等待自动截取完成或手动选择封面')
    return
  }
  if (!title.value.trim()) {
    ElMessage.warning('请填写视频标题')
    return
  }

  publishing.value = true
  publishFailed.value = false
  progress.value = 0
  progressText.value = '准备上传视频…'

  try {
    const videoUrl = await uploadVideoChunked(videoFile.value)

    progressText.value = '上传封面…'
    progress.value = 92
    const coverUrl = await uploadCover()

    progressText.value = '发布视频…'
    progress.value = 97
    await publishVideo(videoUrl, coverUrl)

    progress.value = 100
    progressText.value = '发布成功'
    ElMessage.success('发布成功')

    // 离开 Publish 后 Home 会重新挂载并拉取第一页，Feed 自然刷新
    await router.push('/')
  } catch (error) {
    publishFailed.value = true
    progressText.value = '发布失败，请稍后重试'
    // 具体错误已由 axios 响应拦截器统一提示
    console.error('publish video failed', error)
  } finally {
    publishing.value = false
  }
}

onBeforeUnmount(() => {
  captureSeq += 1
  revokeAutoCover()
  revokeCustomCover()
})
</script>

<template>
  <div class="publish-page">
    <main class="content">
      <el-card shadow="never" class="publish-card">
        <el-form label-width="90px" @submit.prevent>
          <el-form-item label="视频文件" required>
            <input
              ref="videoInputRef"
              class="file-input"
              type="file"
              accept="video/*"
              :disabled="publishing"
              @change="onVideoChange"
            />
            <div v-if="videoFile" class="file-hint">
              {{ videoFile.name }} · {{ formatSize(videoFile.size) }}
            </div>
            <div class="hint">
              支持 mp4 / avi / mov / mkv，最大 2GB；选择后会截取视频第 1 秒画面作为默认封面。
            </div>
          </el-form-item>

          <el-form-item label="封面" required>
            <div class="cover-block">
              <div v-loading="extracting" class="cover-preview">
                <img v-if="coverPreviewUrl" :src="coverPreviewUrl" alt="视频封面预览" />
                <div v-else class="cover-empty">
                  {{ extracting ? '正在截取封面…' : '暂无封面' }}
                </div>
              </div>

              <div class="cover-actions">
                <input
                  ref="coverInputRef"
                  class="file-input"
                  type="file"
                  accept="image/jpeg,image/png,image/webp"
                  :disabled="publishing"
                  @change="onCoverChange"
                />
                <div class="hint">{{ coverSourceText }}</div>
                <el-button
                  v-if="customCoverFile"
                  link
                  type="primary"
                  :disabled="publishing"
                  @click="clearCustomCover"
                >
                  恢复自动截取封面
                </el-button>
              </div>
            </div>
          </el-form-item>

          <el-form-item label="标题" required>
            <el-input
              v-model="title"
              maxlength="255"
              show-word-limit
              placeholder="给视频起个标题"
              :disabled="publishing"
            />
          </el-form-item>

          <el-form-item label="描述">
            <el-input
              v-model="description"
              type="textarea"
              :rows="3"
              maxlength="255"
              show-word-limit
              placeholder="补充视频简介（可选）"
              :disabled="publishing"
            />
          </el-form-item>

          <el-form-item label="标签">
            <el-input
              v-model="tagsText"
              placeholder="可选，用逗号分隔，如：旅行,美食"
              :disabled="publishing"
            />
          </el-form-item>

          <el-form-item v-if="publishing || progress > 0">
            <div class="progress-block">
              <el-progress
                :percentage="progress"
                :status="publishFailed ? 'exception' : progress >= 100 ? 'success' : undefined"
              />
              <div class="hint">{{ progressText }}</div>
            </div>
          </el-form-item>

          <el-form-item>
            <el-button type="primary" :loading="publishing" :disabled="!canPublish" @click="handlePublish">
              发布
            </el-button>
            <el-button :disabled="publishing" @click="router.push('/')">取消</el-button>
          </el-form-item>
        </el-form>
      </el-card>
    </main>
  </div>
</template>

<style scoped>
.publish-page {
  min-height: 100%;
  display: flex;
  flex-direction: column;
}

.content {
  flex: 1;
  width: 100%;
  max-width: 860px;
  margin: 0 auto;
  padding: 24px;
}

.publish-card {
  border-radius: 8px;
}

.file-input {
  display: block;
  max-width: 100%;
  color: #606266;
  font-size: 14px;
}

.file-hint {
  margin-top: 8px;
  color: #303133;
  font-size: 13px;
  line-height: 1.6;
  word-break: break-all;
}

.hint {
  margin-top: 6px;
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}

.cover-block {
  display: flex;
  align-items: flex-start;
  gap: 20px;
  width: 100%;
}

.cover-preview {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 220px;
  min-height: 124px;
  overflow: hidden;
  background: #f0f2f5;
  border: 1px solid #e4e7ed;
  border-radius: 6px;
}

.cover-preview img {
  display: block;
  width: 100%;
  height: auto;
}

.cover-empty {
  color: #909399;
  font-size: 13px;
}

.cover-actions {
  flex: 1;
  min-width: 0;
  padding-top: 4px;
}

.progress-block {
  width: 100%;
}
</style>
