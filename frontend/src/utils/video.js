import { resolveAsset } from './asset'

// 后端内置的默认头像，由 Go 的 /static 路由提供
export const DEFAULT_AVATAR = '/static/default_avatar.png'

export function resolveAvatar(url) {
  return resolveAsset(url) || DEFAULT_AVATAR
}

// Feed 接口的返回结构（flat 字段）
export function normalizeFeedItem(item) {
  if (!item) return null
  return {
    vid: item.vid,
    title: item.title || '',
    desc: item.desc || '',
    videoUrl: item.video_url || '',
    coverUrl: item.cover_url || '',
    authorId: Number(item.author_uid) || 0,
    authorName: item.author_name || '',
    avatarUrl: item.avatar_url || '',
    likeCount: Number(item.likes_cnt) || 0,
    commentsCount: Number(item.comments_cnt) || 0,
    isLiked: Boolean(item.is_like),
    isFollow: Boolean(item.is_follow),
    tags: Array.isArray(item.tags) ? item.tags.filter(Boolean) : [],
    createdAt: item.created_at || ''
  }
}

// /video/list、/like/list_liked 返回的 VideoDetailResponse 结构（author 嵌套）
export function normalizeVideoDetail(item) {
  if (!item) return null
  return {
    vid: item.video_id,
    title: item.title || '',
    desc: item.description || '',
    videoUrl: item.video_url || '',
    coverUrl: item.cover_url || '',
    authorId: Number(item.author?.user_id) || 0,
    authorName: item.author?.user_name || '',
    avatarUrl: item.author?.avatar_url || '',
    likeCount: Number(item.like_count) || 0,
    commentsCount: 0,
    isLiked: Boolean(item.is_liked),
    tags: [],
    createdAt: item.create_time || item.created_at || ''
  }
}

// Go 的 time.Time 带纳秒精度，Safari 直接解析会失败，截断到毫秒
function toDate(value) {
  if (!value) return null
  const input = typeof value === 'string' ? value.replace(/(\.\d{3})\d+/, '$1') : value
  const date = new Date(input)
  return Number.isNaN(date.getTime()) ? null : date
}

// 视频卡片 / 播放页的发布时间：近期用相对时间，超过 30 天显示日期
export function formatPublishTime(value) {
  const date = toDate(value)
  if (!date) return ''

  const diff = Date.now() - date.getTime()
  if (diff < 60 * 1000) return '刚刚'
  if (diff < 60 * 60 * 1000) return `${Math.floor(diff / (60 * 1000))}分钟前`
  if (diff < 24 * 60 * 60 * 1000) return `${Math.floor(diff / (60 * 60 * 1000))}小时前`
  if (diff < 30 * 24 * 60 * 60 * 1000) {
    return `${Math.floor(diff / (24 * 60 * 60 * 1000))}天前`
  }

  return date.toLocaleDateString('zh-CN')
}

// 播放进度：秒 -> mm:ss / h:mm:ss
export function formatDuration(seconds) {
  const value = Number(seconds)
  if (!Number.isFinite(value) || value <= 0) return '00:00'

  const total = Math.floor(value)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const secs = total % 60
  const pad = (n) => String(n).padStart(2, '0')

  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(secs)}` : `${pad(minutes)}:${pad(secs)}`
}
