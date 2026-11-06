package cache

import (
	"fmt"
	"time"
)

// 缓存 key 统一在本文件维护。
// service 读、mq 消费写必须复用下面的构造函数，禁止在业务代码中手写 key 字符串，
// 避免出现读写 key 命名不一致（P0-8）或同一 key 混用多种数据结构（P0-9）。
const (
	// likeUserSetPrefix 用户点赞视频集合前缀，Redis 数据结构固定为 Set，member 为 video id。
	likeUserSetPrefix = "like:user:"
	// likePlaceholderPrefix 未点赞空占位前缀，Redis 数据结构为 String "1"。
	likePlaceholderPrefix = "like:user:placeholder:"
	// likeVideoZSetPrefix 视频点赞明细 ZSet 前缀，member 为点赞用户 id，ZCard 即点赞总数。
	likeVideoZSetPrefix = "like:video:"
	// videoRankZSetKey 视频点赞热度榜单 ZSet，member 为 video id，score 为当前点赞总数。
	videoRankZSetKey = "video:videos"
	// commentVideoCntPrefix 视频评论计数前缀，Redis 数据结构为 String 计数器。
	commentVideoCntPrefix = "comment:video:cnt:"
	// refreshTokenPrefix refresh token 前缀，每个 token 单独一个 key，TTL 到期自动失效。
	refreshTokenPrefix = "refresh_token:"
	// refreshSetPrefix 用户 refresh token 索引集合前缀，记录该用户名下全部 refresh token。
	refreshSetPrefix = "refresh_set:"
	// blacklistTokenPrefix Access Token 黑名单前缀。
	blacklistTokenPrefix = "blacklist_token:"
	// likeEventIDPrefix 点赞 MQ 消费幂等事件ID前缀。
	likeEventIDPrefix = "mq:like_event_id:"
	// commentEventIDPrefix 评论 MQ 消费幂等事件ID前缀。
	commentEventIDPrefix = "mq_comment_event_id:"
	// unreadCountPrefix 未读通知计数前缀，Redis 数据结构为 String 计数器。
	unreadCountPrefix = "unread:"
	// sseTicketPrefix SSE 临时 ticket 前缀，Redis 数据结构为 String，value 为 userID。
	sseTicketPrefix = "sse:ticket:"
	// ssePushChannel SSE 公共频道，Redis 数据结构为 Pub/Sub。
	ssePushChannel = "sse:push"
)

const (
	// LikeUserSetTTL 点赞集合缓存过期时间。
	// 过期兜底：Redis 中的点赞集合只是加速缓存，数据库点赞表才是唯一事实来源；
	// 漏删/漏写产生的脏 member 最多存活一个 TTL 周期，到期后整个集合自动清除，
	// 后续查询回源数据库并重新写入，避免脏数据永久驻留。每次新增点赞会顺延整个集合的过期时间。
	LikeUserSetTTL = 7 * 24 * time.Hour
	// LikePlaceholderTTL 空占位缓存过期时间，仅用于短时间内拦截缓存穿透。
	LikePlaceholderTTL = 5 * time.Minute
	// LikeVideoZSetTTL 视频点赞明细 ZSet 过期时间，与用户点赞集合保持统一兜底周期。
	LikeVideoZSetTTL = 7 * 24 * time.Hour
	// VideoRankZSetTTL 视频点赞热度榜单 ZSet 过期时间，与点赞明细保持统一兜底周期。
	VideoRankZSetTTL = 7 * 24 * time.Hour
	// CommentVideoCntTTL 视频评论计数缓存过期时间。
	CommentVideoCntTTL = 24 * time.Hour
	// RefreshTokenTTL refresh token 默认有效期，每个 token 独立设置 TTL。
	RefreshTokenTTL = 7 * 24 * time.Hour
	// EventIDTTL MQ 消费幂等事件ID缓存过期时间。
	EventIDTTL = 24 * time.Hour
	// UnreadCountTTL 未读通知计数缓存过期时间。
	UnreadCountTTL = 7 * 24 * time.Hour
	// SSETicketTTL SSE 临时 ticket 过期时间。
	SSETicketTTL = 30 * time.Second
)

// LikeUserSetKey 返回用户点赞视频集合的 Redis key，全链路统一使用 Set 结构。
func LikeUserSetKey(userID uint) string {
	return fmt.Sprintf("%s%d", likeUserSetPrefix, userID)
}

// LikePlaceholderKey 返回“用户未点赞该视频”的空占位 Redis key。
func LikePlaceholderKey(userID, vid uint) string {
	return fmt.Sprintf("%s%d:%d", likePlaceholderPrefix, userID, vid)
}

// LikeVideoZSetKey 返回视频点赞明细 ZSet 的 Redis key，member=user id，ZCard=点赞数。
func LikeVideoZSetKey(vid uint) string {
	return fmt.Sprintf("%s%d", likeVideoZSetPrefix, vid)
}

// VideoRankZSetKey 返回视频点赞热度榜单 ZSet 的 Redis key，member=video id，score=点赞数。
func VideoRankZSetKey() string {
	return videoRankZSetKey
}

// CommentVideoCntKey 返回视频评论计数缓存的 Redis key。
func CommentVideoCntKey(vid uint) string {
	return fmt.Sprintf("%s%d", commentVideoCntPrefix, vid)
}

// RefreshTokenKey 返回单个 refresh token 的 Redis key，TTL 到期后自动失效。
func RefreshTokenKey(token string) string {
	return fmt.Sprintf("%s%s", refreshTokenPrefix, token)
}

// RefreshSetKey 返回用户 refresh token 索引集合的 Redis key。
func RefreshSetKey(userID uint) string {
	return fmt.Sprintf("%s%d", refreshSetPrefix, userID)
}

// BlacklistTokenKey 返回 Access Token 黑名单的 Redis key。
func BlacklistTokenKey(token string) string {
	return fmt.Sprintf("%s%s", blacklistTokenPrefix, token)
}

// LikeEventIDKey 返回点赞 MQ 消费幂等事件ID的 Redis key。
func LikeEventIDKey(eventID string) string {
	return fmt.Sprintf("%s%s", likeEventIDPrefix, eventID)
}

// CommentEventIDKey 返回评论 MQ 消费幂等事件ID的 Redis key。
func CommentEventIDKey(eventID string) string {
	return fmt.Sprintf("%s%s", commentEventIDPrefix, eventID)
}

// UnreadCountKey 返回未读通知计数缓存的 Redis key。
func UnreadCountKey(userID uint) string {
	return fmt.Sprintf("%s%d", unreadCountPrefix, userID)
}

// SSETicketKey 返回 SSE 临时 ticket 的 Redis key。
func SSETicketKey(ticket string) string {
	return fmt.Sprintf("%s%s", sseTicketPrefix, ticket)
}

// SSEPushChannel 返回 SSE 公共频道的 Redis key。
func SSEPushChannel() string {
	return ssePushChannel
}
