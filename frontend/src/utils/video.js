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
    coverUrl: item.cover_url || '',
    authorId: Number(item.author_uid) || 0,
    authorName: item.author_name || '',
    avatarUrl: item.avatar_url || '',
    likeCount: Number(item.likes_cnt) || 0,
    isLiked: Boolean(item.is_like)
  }
}

// /video/list、/like/list_liked 返回的 VideoDetailResponse 结构（author 嵌套）
export function normalizeVideoDetail(item) {
  if (!item) return null
  return {
    vid: item.video_id,
    title: item.title || '',
    coverUrl: item.cover_url || '',
    authorId: Number(item.author?.user_id) || 0,
    authorName: item.author?.user_name || '',
    avatarUrl: item.author?.avatar_url || '',
    likeCount: Number(item.like_count) || 0,
    isLiked: Boolean(item.is_liked)
  }
}
