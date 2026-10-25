package cache

import (
	"fmt"
	"time"
)

// 点赞缓存 key 统一在本文件维护。
// service 读、mq 消费写必须复用下面的构造函数，禁止在业务代码中手写 key 字符串，
// 避免出现读写 key 命名不一致（P0-8）或同一 key 混用多种数据结构（P0-9）。
const (
	// likeUserSetPrefix 用户点赞视频集合前缀，Redis 数据结构固定为 Set，member 为 video id。
	likeUserSetPrefix = "like:user:"
	// likePlaceholderPrefix 未点赞空占位前缀，Redis 数据结构为 String "1"。
	likePlaceholderPrefix = "like:user:placeholder:"
)

const (
	// LikeUserSetTTL 点赞集合缓存过期时间。
	// 过期兜底：Redis 中的点赞集合只是加速缓存，数据库点赞表才是唯一事实来源；
	// 漏删/漏写产生的脏 member 最多存活一个 TTL 周期，到期后整个集合自动清除，
	// 后续查询回源数据库并重新写入，避免脏数据永久驻留。每次新增点赞会顺延整个集合的过期时间。
	LikeUserSetTTL = 7 * 24 * time.Hour
	// LikePlaceholderTTL 空占位缓存过期时间，仅用于短时间内拦截缓存穿透。
	LikePlaceholderTTL = 5 * time.Minute
)

// LikeUserSetKey 返回用户点赞视频集合的 Redis key，全链路统一使用 Set 结构。
func LikeUserSetKey(uid uint) string {
	return fmt.Sprintf("%s%d", likeUserSetPrefix, uid)
}

// LikePlaceholderKey 返回“用户未点赞该视频”的空占位 Redis key。
func LikePlaceholderKey(uid, vid uint) string {
	return fmt.Sprintf("%s%d:%d", likePlaceholderPrefix, uid, vid)
}
