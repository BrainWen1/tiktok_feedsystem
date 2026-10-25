package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	socialUidsTTL      = 24 * 60 * 60 * time.Second // 关注列表缓存的过期时间，单位秒
	socialBloggersKey  = "social:bloggers:uid:"     // 关注列表缓存的key模板
	socialFollowersKey = "social:followers:uid:"    // 粉丝列表缓存的key模板

	socialCacheWriteConcurrency = 8 // 缓存回写 goroutine 的全局并发上限
)

var (
	// socialCacheWriteSem 限制缓存回写 goroutine 的并发数量，避免高并发缓存miss时 goroutine 爆炸
	socialCacheWriteSem = make(chan struct{}, socialCacheWriteConcurrency)
	// socialCacheWriteInflight 记录正在回写的 ZSet key，保证同一个 key 同时只有一个 goroutine 刷写
	socialCacheWriteInflight sync.Map
)

type SocialService struct {
	SocialRepo  *repo.SocialRepo
	cache       *cache.RedisCache
	UserService *UserService
}

func NewSocialService(socialRepo *repo.SocialRepo, cache *cache.RedisCache, UserService *UserService) *SocialService {
	return &SocialService{SocialRepo: socialRepo, cache: cache, UserService: UserService}
}

// Follow 关注博主
func (s *SocialService) Follow(ctx context.Context, uid, bloggerID uint) error {
	if uid == 0 || bloggerID == 0 {
		return errors.New("user id and blogger id must be non-zero")
	}
	// 过滤掉用户自己关注自己的情况
	if uid == bloggerID {
		return errors.New("user cannot follow themselves")
	}

	// 先检查是否已经关注，避免重复写入并让接口语义更清晰。
	// 先去Redis查该用户的关注列表ZSet里是否有该博主的uid
	keyb := fmt.Sprintf("%s%d", socialBloggersKey, uid)
	exists, err := s.cache.ZIsMember(ctx, keyb, bloggerID)
	if err != nil {
		log.Printf("Failed to check follow relation in cache: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}
	if exists {
		return errors.New("user already follows this blogger")
	}

	// 查库
	isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, bloggerID)
	if err != nil {
		log.Printf("Failed to check follow relation: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}
	if isFollowing {
		return errors.New("user already follows this blogger")
	}

	// 创建关注关系
	err = s.SocialRepo.CreateFollow(ctx, uid, bloggerID)
	if err != nil {
		log.Printf("Failed to create follow relation: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}

	// 写入ZSet缓存
	social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, uid, bloggerID) // 查找刚创建的关注关系，为了获取CreatedAt时间戳
	if err != nil {
		log.Printf("Failed to find follow relation: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}
	// bloggers
	err = s.cache.ZAdd(ctx, keyb, []redis.Z{
		{
			Score:  float64(social.CreatedAt.Unix()),
			Member: bloggerID,
		},
	}, socialUidsTTL)
	if err != nil {
		log.Printf("Failed to add follow relation to cache: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
	}

	// followers
	keyf := fmt.Sprintf("%s%d", socialFollowersKey, bloggerID)
	err = s.cache.ZAdd(ctx, keyf, []redis.Z{
		{
			Score:  float64(social.CreatedAt.Unix()),
			Member: uid,
		},
	}, socialUidsTTL)
	if err != nil {
		log.Printf("Failed to add follower relation to cache: bloggerID=%d uid=%d err=%v", bloggerID, uid, err)
	}

	return nil
}

// Unfollow 取消关注博主
func (s *SocialService) Unfollow(ctx context.Context, uid, bloggerID uint) error {
	if uid == 0 || bloggerID == 0 {
		return errors.New("user id and blogger id must be non-zero")
	}
	if uid == bloggerID {
		return errors.New("user cannot unfollow themselves")
	}

	// 先确认关系存在，避免前端重复点击时出现“删除成功但实际没有记录”的歧义。
	isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, bloggerID)
	if err != nil {
		log.Printf("Failed to check follow relation before unfollow: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}
	if !isFollowing {
		return errors.New("user is not following this blogger")
	}

	deleted, err := s.SocialRepo.DeleteFollow(ctx, uid, bloggerID)
	if err != nil {
		log.Printf("Failed to delete follow relation: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}
	if !deleted {
		return errors.New("follow relation not found")
	}

	// 删除ZSet缓存
	// bloggers
	keyb := fmt.Sprintf("%s%d", socialBloggersKey, uid)
	err = s.cache.ZRem(ctx, keyb, bloggerID)
	if err != nil {
		log.Printf("Failed to remove follow relation from cache: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
	}
	// followers
	keyf := fmt.Sprintf("%s%d", socialFollowersKey, bloggerID)
	err = s.cache.ZRem(ctx, keyf, uid)
	if err != nil {
		log.Printf("Failed to remove follower relation from cache: bloggerID=%d uid=%d err=%v", bloggerID, uid, err)
	}

	return nil
}

// GetBloggers 获取用户的关注列表，使用 (created_at, blogger_id) 复合游标分页。
func (s *SocialService) GetBloggers(ctx context.Context, targetUid, uid uint, cursor dto.FeedCursor, pageSize uint) ([]*dto.UserSocialSimpleResponse, int64, *dto.FeedCursor, error) {
	if pageSize == 0 {
		pageSize = 20
	}
	lastUid, hasCursor, err := parseFeedCursorVid(cursor.LastUid)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("get bloggers: %w", err)
	}
	var lastCreatedAt time.Time
	if hasCursor {
		if cursor.LastCreatedAt.IsZero() {
			return nil, 0, nil, errors.New("incomplete social cursor: last_created_at is required when last_uid is set")
		}
		lastCreatedAt = cursor.LastCreatedAt
	}

	key := fmt.Sprintf("%s%d", socialBloggersKey, targetUid)
	// 尝试从Redis ZSet拿：按 (score, member) 复合游标读取，避免 ZRange 的 offset 分页
	if exists, err := s.cache.Exists(ctx, key); err == nil && exists {
		total, zErr := s.cache.ZCard(ctx, key)
		uidStrList, pageErr := s.socialCachePageByCursor(ctx, key, lastCreatedAt, lastUid, int(pageSize))
		nextCursor, cursorErr := s.socialCursorFromCache(ctx, key, uidStrList, int(pageSize))
		if zErr == nil && pageErr == nil && cursorErr == nil {
			// 转uint，批量拿用户信息
			uidList := strSlice2UintSlice(uidStrList)
			userList, err := s.batchGetUserSimple(ctx, uidList)
			if err == nil {
				// 根据实际情况处理IsFollow字段
				if uid != 0 {
					if uid == targetUid {
						// 如果是查看自己的关注列表，所有人都是已关注
						for _, user := range userList {
							user.IsFollow = true
						}
					} else {
						// 如果是查看别人的关注列表，检查当前用户是否关注了这些博主
						for _, user := range userList {
							isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, user.Uid)
							if err != nil {
								log.Printf("Failed to check follow relation for uid=%d and bloggerID=%d: %v", uid, user.Uid, err)
								return nil, 0, nil, err
							}
							user.IsFollow = isFollowing
						}
					}
				}
				return userList, total, nextCursor, nil
			}
			log.Printf("Failed to batch get user simple info: %v", err)
		} else {
			log.Printf("social cache read failed, fallback to db: key=%s zErr=%v pageErr=%v cursorErr=%v", key, zErr, pageErr, cursorErr)
		}
	}

	// Cache Miss，查MySQL
	socials, err := s.SocialRepo.ListAllBloggers(ctx, targetUid, true) // 按关注时间倒序
	if err != nil {
		return nil, 0, nil, err
	}
	total := int64(len(socials))
	pageSocials := socialPageByCursor(socials, lastCreatedAt, lastUid, int(pageSize), func(social *model.Social) uint {
		return social.BloggerID
	})

	// 将socials转换为uidList
	uidlist := make([]uint, len(pageSocials))
	for i, social := range pageSocials { // 保持倒序关系不变
		uidlist[i] = social.BloggerID
	}

	// 批量获取用户简单信息
	userList, err := s.batchGetUserSimple(ctx, uidlist)
	if err != nil {
		log.Printf("Failed to batch get user simple info: %v", err)
		return nil, 0, nil, err
	}

	// 根据实际情况处理IsFollow字段
	if uid != 0 {
		if uid == targetUid {
			// 如果是查看自己的关注列表，所有人都是已关注
			for _, user := range userList {
				user.IsFollow = true
			}
		} else {
			// 如果是查看别人的关注列表，检查当前用户是否关注了这些博主
			for _, user := range userList {
				isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, user.Uid)
				if err != nil {
					log.Printf("Failed to check follow relation for uid=%d and bloggerID=%d: %v", uid, user.Uid, err)
					return nil, 0, nil, err
				}
				user.IsFollow = isFollowing
			}
		}
	}

	// 异步回写ZSet缓存：每条关注记录，member=博主uid，score=关注创建时间戳
	zList := make([]redis.Z, 0, len(socials))
	for _, social := range socials { // 全量写入
		zList = append(zList, redis.Z{
			Score:  float64(social.CreatedAt.Unix()),
			Member: social.BloggerID,
		})
	}
	s.asyncRewriteSocialZSet(key, zList)

	nextCursor := socialCursorFromDBPage(pageSocials, int(pageSize), func(social *model.Social) uint {
		return social.BloggerID
	})
	return userList, total, nextCursor, nil
}

// asyncRewriteSocialZSet 异步全量回写关注/粉丝 ZSet。
// 同一个 key 同时只允许一个回写 goroutine，且回写总数不超过 socialCacheWriteConcurrency，
// 避免高并发缓存miss时每个请求都启动 goroutine 刷写同一个 ZSet。
func (s *SocialService) asyncRewriteSocialZSet(key string, zList []redis.Z) {
	// 同 key 已有回写任务时直接跳过，等当前任务完成即可覆盖本次数据
	if _, loaded := socialCacheWriteInflight.LoadOrStore(key, struct{}{}); loaded {
		return
	}

	// 全局并发限流，信号量满时放弃本次回写，读取端后续仍可触发
	select {
	case socialCacheWriteSem <- struct{}{}:
	default:
		socialCacheWriteInflight.Delete(key)
		return
	}

	go func() {
		// 注册recover，防止goroutine panic导致程序崩溃
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ZAdd goroutine panic: %v", r)
			}
			<-socialCacheWriteSem
			socialCacheWriteInflight.Delete(key)
		}()

		cacheCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := s.cache.ZAdd(cacheCtx, key, zList, socialUidsTTL); err != nil {
			log.Printf("batch zadd social cache failed, key=%s, err:%v", key, err)
		}
	}()
}

// socialPageByCursor 从全量社交记录中，按 (created_at 秒, uid) 复合游标截取一页。
// 这里把排序键统一到秒 + uid，与 Redis ZSet 的 score(秒) + member 顺序保持一致，
// 避免缓存与数据库两条路径的游标语义不同导致同一秒内的记录重复或丢失。
func socialPageByCursor(socials []*model.Social, lastCreatedAt time.Time, lastUid uint, pageSize int, uidOf func(*model.Social) uint) []*model.Social {
	sort.SliceStable(socials, func(i, j int) bool {
		leftSec, rightSec := socials[i].CreatedAt.Unix(), socials[j].CreatedAt.Unix()
		if leftSec != rightSec {
			return leftSec > rightSec
		}
		return uidOf(socials[i]) > uidOf(socials[j])
	})

	page := make([]*model.Social, 0, pageSize)
	cursorSec := lastCreatedAt.Unix()
	for _, social := range socials {
		if lastUid != 0 {
			sec := social.CreatedAt.Unix()
			if sec > cursorSec {
				continue
			}
			if sec == cursorSec && uidOf(social) >= lastUid {
				continue
			}
		}
		page = append(page, social)
		if len(page) == pageSize {
			break
		}
	}
	return page
}

// socialCursorFromDBPage 用数据库中一页的最后一条记录生成下一页游标；未取满一页表示没有下一页。
// 时间统一截断到秒，与 ZSet score 精度一致。
func socialCursorFromDBPage(pageSocials []*model.Social, pageSize int, uidOf func(*model.Social) uint) *dto.FeedCursor {
	if pageSize <= 0 || len(pageSocials) < pageSize {
		return nil
	}
	last := pageSocials[len(pageSocials)-1]
	return &dto.FeedCursor{
		LastUid:       strconv.FormatUint(uint64(uidOf(last)), 10),
		LastCreatedAt: time.Unix(last.CreatedAt.Unix(), 0),
	}
}

// socialCachePageByCursor 按 (score, member) 复合游标从 ZSet 读取一页 uid。
// score 为关注时间戳（秒），同一秒内按 member 倒序排列；先定位游标 member 在同分桶中的位置，
// 再取更早分数的成员，避免 ZRange offset 分页在数据变动时重复或漏数据。
func (s *SocialService) socialCachePageByCursor(ctx context.Context, key string, lastCreatedAt time.Time, lastUid uint, pageSize int) ([]string, error) {
	limit := int64(pageSize)
	if lastUid == 0 {
		return s.cache.ZRange(ctx, key, 0, limit-1, true)
	}

	scoreStr := strconv.FormatInt(lastCreatedAt.Unix(), 10)
	tieMembers, err := s.cache.ZRangeByScore(ctx, key, scoreStr, scoreStr, 0, -1, true)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, pageSize)
	cursorMember := strconv.FormatUint(uint64(lastUid), 10)
	for i, member := range tieMembers {
		if member == cursorMember {
			result = append(result, tieMembers[i+1:]...)
			break
		}
	}
	if int64(len(result)) < limit {
		// reverse=true 时 Start 是分数上限，Stop 是分数下限
		older, err := s.cache.ZRangeByScore(ctx, key, "("+scoreStr, "-inf", 0, limit-int64(len(result)), true)
		if err != nil {
			return nil, err
		}
		result = append(result, older...)
	}
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result, nil
}

// socialCursorFromCache 用缓存页的最后一条 uid + ZSet score 生成下一页游标。
func (s *SocialService) socialCursorFromCache(ctx context.Context, key string, uids []string, pageSize int) (*dto.FeedCursor, error) {
	if pageSize <= 0 || len(uids) < pageSize {
		return nil, nil
	}
	last := uids[len(uids)-1]
	score, err := s.cache.ZScore(ctx, key, last)
	if err != nil {
		return nil, err
	}
	return &dto.FeedCursor{
		LastUid:       last,
		LastCreatedAt: time.Unix(int64(score), 0),
	}, nil
}

// strSlice2UintSlice 将字符串切片转换为uint切片
func strSlice2UintSlice(strs []string) []uint {
	uints := make([]uint, len(strs))
	for i, s := range strs {
		var u uint
		fmt.Sscanf(s, "%d", &u)
		uints[i] = u
	}
	return uints
}

// batchGetUserSimple 批量获取用户的简单信息
func (s *SocialService) batchGetUserSimple(ctx context.Context, uidList []uint) ([]*dto.UserSocialSimpleResponse, error) {
	if len(uidList) == 0 {
		return []*dto.UserSocialSimpleResponse{}, nil
	}

	simpleList := make([]*dto.UserSocialSimpleResponse, 0, len(uidList))
	for _, uid := range uidList {
		if uid == 0 {
			return nil, errors.New("invalid uid in list")
		}

		user, err := s.UserService.FindByID(ctx, uid)
		if err != nil {
			return nil, err
		}
		if user == nil {
			return nil, errors.New("user not found")
		}

		simpleList = append(simpleList, &dto.UserSocialSimpleResponse{
			Uid:       user.ID,
			UserName:  user.Username,
			AvatarUrl: user.AvatarURL,
			IsFollow:  false, // 后面根据实际情况处理
		})
	}

	if len(simpleList) != len(uidList) {
		return nil, errors.New("inconsistent user list length")
	}

	return simpleList, nil
}

// GetFollowers 获取用户的粉丝列表，使用 (created_at, follower_id) 复合游标分页。
func (s *SocialService) GetFollowers(ctx context.Context, targetUid, uid uint, cursor dto.FeedCursor, pageSize uint) ([]*dto.UserSocialSimpleResponse, int64, *dto.FeedCursor, error) {
	if pageSize == 0 {
		pageSize = 20
	}
	lastUid, hasCursor, err := parseFeedCursorVid(cursor.LastUid)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("get followers: %w", err)
	}
	var lastCreatedAt time.Time
	if hasCursor {
		if cursor.LastCreatedAt.IsZero() {
			return nil, 0, nil, errors.New("incomplete social cursor: last_created_at is required when last_uid is set")
		}
		lastCreatedAt = cursor.LastCreatedAt
	}

	key := fmt.Sprintf("%s%d", socialFollowersKey, targetUid)
	// 尝试从Redis ZSet拿：按 (score, member) 复合游标读取，避免 ZRange 的 offset 分页
	if exists, err := s.cache.Exists(ctx, key); err == nil && exists {
		total, zErr := s.cache.ZCard(ctx, key)
		uidStrList, pageErr := s.socialCachePageByCursor(ctx, key, lastCreatedAt, lastUid, int(pageSize))
		nextCursor, cursorErr := s.socialCursorFromCache(ctx, key, uidStrList, int(pageSize))
		if zErr == nil && pageErr == nil && cursorErr == nil {
			// 转uint，批量拿用户信息
			uidList := strSlice2UintSlice(uidStrList)
			userList, err := s.batchGetUserSimple(ctx, uidList)
			if err == nil {
				// 根据实际情况处理IsFollow字段
				if uid != 0 {
					if uid == targetUid {
						// 查看自己的粉丝列表：IsFollow = 这个粉丝有没有关注我
						for _, user := range userList {
							isFollowing, err := s.SocialRepo.IsFollowing(ctx, user.Uid, uid)
							if err != nil {
								log.Printf("Failed to check follow relation for fan=%d target=%d: %v", user.Uid, uid, err)
								return nil, 0, nil, err
							}
							user.IsFollow = isFollowing
						}
					} else {
						// 看别人的粉丝列表：当前登录用户uid，是否关注这个粉丝user.Uid
						for _, user := range userList {
							isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, user.Uid)
							if err != nil {
								log.Printf("Failed to check follow relation for uid=%d and fanID=%d: %v", uid, user.Uid, err)
								return nil, 0, nil, err
							}
							user.IsFollow = isFollowing
						}
					}
				}
				return userList, total, nextCursor, nil
			}
			log.Printf("Failed to batch get user simple info: %v", err)
		} else {
			log.Printf("social cache read failed, fallback to db: key=%s zErr=%v pageErr=%v cursorErr=%v", key, zErr, pageErr, cursorErr)
		}
	}

	// Cache Miss，查MySQL
	socials, err := s.SocialRepo.ListAllFollowers(ctx, targetUid, true) // 按关注时间倒序
	if err != nil {
		return nil, 0, nil, err
	}
	total := int64(len(socials))
	pageSocials := socialPageByCursor(socials, lastCreatedAt, lastUid, int(pageSize), func(social *model.Social) uint {
		return social.FollowerID
	})

	// 将socials转换为uidList：粉丝ID是FollowerID
	uidlist := make([]uint, len(pageSocials))
	for i, social := range pageSocials {
		uidlist[i] = social.FollowerID
	}
	// 批量获取用户简单信息
	userList, err := s.batchGetUserSimple(ctx, uidlist)
	if err != nil {
		log.Printf("Failed to batch get user simple info: %v", err)
		return nil, 0, nil, err
	}
	// 根据实际情况处理IsFollow字段
	if uid != 0 {
		if uid == targetUid {
			// 查看自己的粉丝列表：IsFollow = 粉丝有没有关注我
			for _, user := range userList {
				isFollowing, err := s.SocialRepo.IsFollowing(ctx, user.Uid, uid)
				if err != nil {
					log.Printf("Failed to check follow relation for fan=%d target=%d: %v", user.Uid, uid, err)
					return nil, 0, nil, err
				}
				user.IsFollow = isFollowing
			}
		} else {
			// 看别人的粉丝列表：登录用户是否关注这个粉丝
			for _, user := range userList {
				isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, user.Uid)
				if err != nil {
					log.Printf("Failed to check follow relation for uid=%d and fanID=%d: %v", uid, user.Uid, err)
					return nil, 0, nil, err
				}
				user.IsFollow = isFollowing
			}
		}
	}
	// 异步回写ZSet缓存：member=粉丝uid，score=关注创建时间戳
	zList := make([]redis.Z, 0, len(socials))
	for _, social := range socials {
		zList = append(zList, redis.Z{
			Score:  float64(social.CreatedAt.Unix()),
			Member: social.FollowerID,
		})
	}
	s.asyncRewriteSocialZSet(key, zList)

	nextCursor := socialCursorFromDBPage(pageSocials, int(pageSize), func(social *model.Social) uint {
		return social.FollowerID
	})
	return userList, total, nextCursor, nil
}

// IsFollow 获取两者双向关注关系
func (s *SocialService) IsFollow(ctx context.Context, uid, targetID uint) (bool, bool, error) {
	if uid == 0 || targetID == 0 {
		return false, false, errors.New("user id and target id must be non-zero")
	}
	if uid == targetID {
		return false, false, errors.New("user cannot follow themselves")
	}

	// uid 是否关注 targetID
	keyUIDBloggers := fmt.Sprintf("%s%d", socialBloggersKey, uid)
	keyUIDFollowers := fmt.Sprintf("%s%d", socialFollowersKey, uid)
	keyTIDBloggers := fmt.Sprintf("%s%d", socialBloggersKey, targetID)
	keyTIDFollowers := fmt.Sprintf("%s%d", socialFollowersKey, targetID)

	isFollow, err := s.cache.ZIsMember(ctx, keyUIDBloggers, targetID)
	if err != nil {
		log.Printf("Failed to check follow relation in cache: uid=%d targetID=%d err=%v", uid, targetID, err)
		return false, false, err
	}
	// 查库并回写缓存
	if !isFollow {
		isFollow, err = s.SocialRepo.IsFollowing(ctx, uid, targetID)
		if err != nil {
			log.Printf("Failed to check follow relation in DB: uid=%d targetID=%d err=%v", uid, targetID, err)
			return false, false, err
		}
		// 数据库查到结果，回填缓存
		if isFollow {
			social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, uid, targetID)
			if err != nil {
				log.Printf("Failed to find follow relation in DB: uid=%d targetID=%d err=%v", uid, targetID, err)
				return false, false, err
			}

			// 关注缓存
			if err := s.cache.ZAdd(ctx, keyUIDBloggers, []redis.Z{{Score: float64(social.CreatedAt.Unix()), Member: targetID}}, socialUidsTTL); err != nil {
				log.Printf("ZAdd follow cache failed, key=%s uid=%d targetID=%d err=%v", keyUIDBloggers, uid, targetID, err)
			}
			// 粉丝缓存
			if err := s.cache.ZAdd(ctx, keyTIDFollowers, []redis.Z{{Score: float64(social.CreatedAt.Unix()), Member: uid}}, socialUidsTTL); err != nil {
				log.Printf("ZAdd follower cache failed, key=%s targetID=%d uid=%d err=%v", keyTIDFollowers, targetID, uid, err)
			}
		}
	}

	// targetID是否关注uid
	isFan, err := s.cache.ZIsMember(ctx, keyUIDFollowers, targetID)
	if err != nil {
		log.Printf("Failed to check fan relation in cache: uid=%d targetID=%d err=%v", uid, targetID, err)
		return false, false, err
	}
	// 查库并回写缓存
	if !isFan {
		isFan, err = s.SocialRepo.IsFollowing(ctx, targetID, uid)
		if err != nil {
			log.Printf("Failed to check fan relation in DB: uid=%d targetID=%d err=%v", uid, targetID, err)
			return false, false, err
		}

		if isFan {
			social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, targetID, uid)
			if err != nil {
				log.Printf("Failed to find fan relation in DB: uid=%d targetID=%d err=%v", uid, targetID, err)
				return false, false, err
			}

			// 关注缓存
			if err := s.cache.ZAdd(ctx, keyTIDBloggers, []redis.Z{{Score: float64(social.CreatedAt.Unix()), Member: uid}}, socialUidsTTL); err != nil {
				log.Printf("ZAdd follow cache failed, key=%s targetID=%d uid=%d err=%v", keyTIDBloggers, targetID, uid, err)
			}
			// 粉丝缓存
			if err := s.cache.ZAdd(ctx, keyUIDFollowers, []redis.Z{{Score: float64(social.CreatedAt.Unix()), Member: targetID}}, socialUidsTTL); err != nil {
				log.Printf("ZAdd follower cache failed, key=%s uid=%d targetID=%d err=%v", keyUIDFollowers, uid, targetID, err)
			}
		}
	}

	return isFollow, isFan, nil
}

// GetCounts 获取关注和粉丝数量
func (s *SocialService) GetCounts(ctx context.Context, targetID uint) (int64, int64, error) {
	if targetID == 0 {
		return 0, 0, errors.New("target id must be non-zero")
	}

	// 尝试从Redis ZSet拿
	keyBloggers := fmt.Sprintf("%s%d", socialBloggersKey, targetID)
	keyFollowers := fmt.Sprintf("%s%d", socialFollowersKey, targetID)

	followCount, err := s.cache.ZCard(ctx, keyBloggers)
	if err != nil {
		log.Printf("Failed to get follow count from cache: targetID=%d err=%v", targetID, err)
		return 0, 0, err
	}

	fanCount, err := s.cache.ZCard(ctx, keyFollowers)
	if err != nil {
		log.Printf("Failed to get fan count from cache: targetID=%d err=%v", targetID, err)
		return 0, 0, err
	}

	// 如果缓存中没有数据，查库并回写缓存
	if followCount == 0 || fanCount == 0 {
		followCountDB, fanCountDB, err := s.SocialRepo.GetCounts(ctx, targetID)
		if err != nil {
			log.Printf("Failed to get counts from DB: targetID=%d err=%v", targetID, err)
			return 0, 0, err
		}

		// 回写缓存
		if followCount == 0 {
			socials, err := s.SocialRepo.ListAllBloggers(ctx, targetID, true)
			if err != nil {
				log.Printf("Failed to list all bloggers for cache: targetID=%d err=%v", targetID, err)
			} else {
				var zList []redis.Z
				for _, social := range socials {
					zList = append(zList, redis.Z{
						Score:  float64(social.CreatedAt.Unix()),
						Member: social.BloggerID,
					})
				}
				err = s.cache.ZAdd(ctx, keyBloggers, zList, socialUidsTTL)
				if err != nil {
					log.Printf("Failed to write follow count to cache: targetID=%d err=%v", targetID, err)
				}
			}
		}

		if fanCount == 0 {
			socials, err := s.SocialRepo.ListAllFollowers(ctx, targetID, true)
			if err != nil {
				log.Printf("Failed to list all followers for cache: targetID=%d err=%v", targetID, err)
			} else {
				var zList []redis.Z
				for _, social := range socials {
					zList = append(zList, redis.Z{
						Score:  float64(social.CreatedAt.Unix()),
						Member: social.FollowerID,
					})
				}
				err = s.cache.ZAdd(ctx, keyFollowers, zList, socialUidsTTL)
				if err != nil {
					log.Printf("Failed to write fan count to cache: targetID=%d err=%v", targetID, err)
				}
			}
		}

		return followCountDB, fanCountDB, nil
	}

	return followCount, fanCount, nil
}

// GetMyFollowUidList 获取当前用户关注的全部uid集合（给feed关注流使用）
func (s *SocialService) GetMyFollowUidList(ctx context.Context, uid uint) ([]uint, error) {
	key := fmt.Sprintf("%s%d", socialBloggersKey, uid)
	// 1. 先查Redis缓存
	strUids, err := s.cache.ZRange(ctx, key, 0, -1, true)
	if err == nil && len(strUids) > 0 {
		// 缓存命中，字符串转uint数组直接返回
		uidList := make([]uint, 0, len(strUids))
		for _, suid := range strUids {
			num, parseErr := strconv.ParseUint(suid, 10, 64)
			if parseErr != nil {
				continue
			}
			uidList = append(uidList, uint(num))
		}
		return uidList, nil
	}

	// 2. 缓存未命中，调用repo查DB（repo只做SQL查询）
	dbRecords, err := s.SocialRepo.QueryMyFollowList(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(dbRecords) == 0 {
		// 无关注数据，可写入空缓存防缓存穿透（可选）
		return []uint{}, nil
	}

	// 3. 查询成功，回填Redis ZSet，score=关注时间毫秒
	zPairs := make([]redis.Z, 0, len(dbRecords))
	for _, item := range dbRecords {
		zPairs = append(zPairs, redis.Z{
			Score:  float64(item.CreatedAt.UnixMilli()),
			Member: strconv.FormatUint(uint64(item.BloggerID), 10),
		})
	}

	err = s.cache.ZAdd(ctx, key, zPairs, socialUidsTTL)
	if err != nil {
		log.Printf("Failed to write follow list to cache: uid=%d err=%v", uid, err)
	}

	// 组装返回uint数组
	uidList := make([]uint, 0, len(dbRecords))
	for _, item := range dbRecords {
		uidList = append(uidList, item.BloggerID)
	}
	return uidList, nil
}

// BatchIsFollow 批量查询：loginUid 是否关注 authorUidList 里面的作者
func (s *SocialService) BatchIsFollow(ctx context.Context, loginUid uint, authorUidList []uint) (map[uint]bool, error) {
	resMap := make(map[uint]bool)
	if loginUid == 0 || len(authorUidList) == 0 {
		return resMap, nil
	}
	// 缓存禁用时直接按数据库关注关系查询
	if !s.cache.IsEnabled() {
		for _, auid := range authorUidList {
			isFollowing, err := s.SocialRepo.IsFollowing(ctx, loginUid, auid)
			if err != nil {
				return nil, fmt.Errorf("failed to query follow relation from db for user %d target %d: %w", loginUid, auid, err)
			}
			resMap[auid] = isFollowing
		}
		return resMap, nil
	}
	key := fmt.Sprintf("%s%d", socialBloggersKey, loginUid)
	for _, auid := range authorUidList {
		isMember, err := s.cache.ZIsMember(ctx, key, strconv.FormatUint(uint64(auid), 10))
		if err != nil {
			return nil, err
		}
		resMap[auid] = isMember
	}
	return resMap, nil
}
