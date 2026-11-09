package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	socialUserIDsTTL   = 24 * 60 * 60 * time.Second // 关注列表缓存的过期时间，单位秒
	socialBloggersKey  = "social:bloggers:uid:"     // 关注列表缓存的key模板
	socialFollowersKey = "social:followers:uid:"    // 粉丝列表缓存的key模板

	socialCacheWriteConcurrency = 8               // 缓存回写 goroutine 的全局并发上限
	socialCacheWriteTimeout     = 3 * time.Second // 异步回写缓存的超时时间

	// socialEmptyZSetMember 空关注/粉丝列表的占位成员。
	// Redis 无法保存空 ZSet（成员全部删除后 key 会消失），用非法的 userID=0 作为占位，
	// 让 0 关注/0 粉丝的用户也能留下缓存标记，避免每次请求都穿透到数据库。
	socialEmptyZSetMember = "0"
)

var (
	// socialCacheWriteSem 限制缓存回写 goroutine 的并发数量，避免高并发缓存miss时 goroutine 爆炸
	socialCacheWriteSem = make(chan struct{}, socialCacheWriteConcurrency)
	// socialCacheWriteInflight 记录正在回写的 ZSet key，保证同一个 key 同时只有一个 goroutine 刷写
	socialCacheWriteInflight sync.Map
)

type SocialService struct {
	SocialRepo          *repo.SocialRepo
	cache               *cache.RedisCache
	UserService         *UserService
	notificationService *NotificationService
}

func NewSocialService(socialRepo *repo.SocialRepo, cache *cache.RedisCache, UserService *UserService, notificationService *NotificationService) *SocialService {
	return &SocialService{SocialRepo: socialRepo, cache: cache, UserService: UserService, notificationService: notificationService}
}

// readSocialCacheCount 读取关注/粉丝 ZSet 的元素数量，并排除空集合占位成员。
// known=false 表示缓存 key 不存在（正常 miss，可回源数据库）；Redis 异常直接返回 error，
// 这样上层能区分“缓存没有”和“缓存故障”，不会把故障当成空列表。
func (s *SocialService) readSocialCacheCount(ctx context.Context, key string) (count int64, known bool, err error) {
	exists, err := s.cache.Exists(ctx, key)
	if err != nil {
		return 0, false, fmt.Errorf("check cache existence for key %s: %w", key, err)
	}
	if !exists {
		return 0, false, nil
	}

	count, err = s.cache.ZCard(ctx, key)
	if err != nil {
		return 0, false, fmt.Errorf("zcard cache key %s: %w", key, err)
	}
	// Redis 无法保存空 ZSet，count=0 说明 key 在 Exists 与 ZCard 之间过期，按 miss 回源
	if count == 0 {
		return 0, false, nil
	}
	hasEmptyMarker, err := s.cache.ZIsMember(ctx, key, socialEmptyZSetMember)
	if err != nil {
		return 0, false, fmt.Errorf("check empty member for key %s: %w", key, err)
	}
	if hasEmptyMarker && count > 0 {
		count--
	}
	return count, true, nil
}

// writeSocialZSet 回写关注/粉丝列表缓存；空列表写入占位成员，保证 0 关注/0 粉丝的用户也能命中缓存。
func (s *SocialService) writeSocialZSet(ctx context.Context, key string, zList []redis.Z) error {
	if len(zList) == 0 {
		zList = []redis.Z{{Score: 0, Member: socialEmptyZSetMember}}
	}
	return s.cache.ZAdd(ctx, key, zList, socialUserIDsTTL)
}

// zaddIfKeyExists 仅在缓存 key 已存在时增量写入单个成员。
// 关注/粉丝集合缺失时不能在 Follow、IsFollow 的回填里单条 ZAdd：
// 读路径会把 key 存在视为“缓存即全量”，只写一个成员会让计数和列表长期错误。
// key 不存在时直接跳过，交给 GetCounts 等全量回源路径重建。
func (s *SocialService) zaddIfKeyExists(ctx context.Context, key string, memberID uint, createdAt time.Time) error {
	exists, err := s.cache.Exists(ctx, key)
	if err != nil {
		return fmt.Errorf("check cache existence for key %s: %w", key, err)
	}
	if !exists {
		return nil
	}
	return s.cache.ZAdd(ctx, key, []redis.Z{{
		Score:  float64(createdAt.Unix()),
		Member: memberID,
	}}, socialUserIDsTTL)
}

// filterSocialMembers 过滤空集合占位成员，避免把 userID=0 当作真实用户返回。
func filterSocialMembers(members []string) []string {
	if len(members) == 0 {
		return members
	}
	filtered := members[:0]
	for _, member := range members {
		if member == socialEmptyZSetMember {
			continue
		}
		filtered = append(filtered, member)
	}
	return filtered
}

// Follow 关注博主
func (s *SocialService) Follow(ctx context.Context, userID, bloggerID uint, userName string) error {
	if userID == 0 || bloggerID == 0 {
		return errors.New("user id and blogger id must be non-zero")
	}
	// 过滤掉用户自己关注自己的情况
	if userID == bloggerID {
		return errors.New("user cannot follow themselves")
	}

	// 目标用户必须存在，避免向任意数字 ID 写入关注关系，产生脏列表/脏通知。
	if _, err := s.UserService.FindByID(ctx, bloggerID); err != nil {
		logger.Ctx(ctx).Warn("target blogger not found", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
		return fmt.Errorf("find blogger %d: %w", bloggerID, err)
	}

	// 先查缓存：已关注直接幂等返回，避免重复落库。
	keyb := fmt.Sprintf("%s%d", socialBloggersKey, userID)
	exists, err := s.cache.ZIsMember(ctx, keyb, bloggerID)
	if err != nil {
		logger.Ctx(ctx).Error("check follow relation in cache failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
		return fmt.Errorf("check follow cache for user %d blogger %d: %w", userID, bloggerID, err)
	}
	if exists {
		return nil // 重复关注：幂等成功
	}

	// 依赖唯一索引 + ON CONFLICT DO NOTHING 兜底并发/重复请求。
	created, err := s.SocialRepo.CreateFollow(ctx, userID, bloggerID)
	if err != nil {
		logger.Ctx(ctx).Error("create follow relation failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
		return fmt.Errorf("create follow relation for user %d blogger %d: %w", userID, bloggerID, err)
	}
	if !created {
		return nil // 已经关注：幂等成功，不重复发通知
	}

	// 写入ZSet缓存
	social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, userID, bloggerID) // 查找刚创建的关注关系，为了获取CreatedAt时间戳
	if err != nil {
		logger.Ctx(ctx).Error("find follow relation failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
		return fmt.Errorf("find follow relation for user %d blogger %d: %w", userID, bloggerID, err)
	}
	// 只有缓存 key 已存在（由全量回源或空占位建立）时才做单成员增量写入；
	// key 不存在时跳过，否则会把“只有 1 个成员”的残缺集合当成全量缓存。
	// bloggers
	if err := s.zaddIfKeyExists(ctx, keyb, bloggerID, social.CreatedAt); err != nil {
		logger.Ctx(ctx).Warn("add follow relation to cache failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
	}

	// followers
	keyf := fmt.Sprintf("%s%d", socialFollowersKey, bloggerID)
	if err := s.zaddIfKeyExists(ctx, keyf, userID, social.CreatedAt); err != nil {
		logger.Ctx(ctx).Warn("add follower relation to cache failed", logger.Uint("blogger_id", bloggerID), logger.Uint("user_id", userID), logger.Err(err))
	}

	// 异步发送关注通知
	go func() {
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		// err 使用 := 创建局部变量，避免覆盖外层的 err，防止以后可能发生的竞态
		err := s.notificationService.ProcessNotification(nctx, mq.NotificationEvent{
			// 目前 eventID 先传空串，后续如果把关注功能接入 mq 使用异步处理，并且在函数内部加入重试机制，则需要使用 eventID 做幂等
			// 这里不使用 uuid.NewString()，因为在函数内部也不会使用，会产生垃圾
			EventID:   "",
			Action:    "follow",
			ActorID:   userID,
			UserName:  userName,
			BloggerID: bloggerID,
		})
		if err != nil {
			logger.Ctx(nctx).Error("process notification failed", logger.Err(err))
		}
	}()

	return nil
}

// Unfollow 取消关注博主
func (s *SocialService) Unfollow(ctx context.Context, userID, bloggerID uint) error {
	if userID == 0 || bloggerID == 0 {
		return errors.New("user id and blogger id must be non-zero")
	}
	if userID == bloggerID {
		return errors.New("user cannot unfollow themselves")
	}

	// 直接删除：RowsAffected==0 说明本来就没关注，按幂等成功处理，
	// 避免重复点击/并发取关返回错误。
	deleted, err := s.SocialRepo.DeleteFollow(ctx, userID, bloggerID)
	if err != nil {
		logger.Ctx(ctx).Error("delete follow relation failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
		return fmt.Errorf("delete follow relation for user %d blogger %d: %w", userID, bloggerID, err)
	}
	if !deleted {
		logger.Ctx(ctx).Debug("follow relation already absent, idempotent unfollow", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID))
	}

	// 删除ZSet缓存
	// bloggers
	keyb := fmt.Sprintf("%s%d", socialBloggersKey, userID)
	err = s.cache.ZRem(ctx, keyb, bloggerID)
	if err != nil {
		logger.Ctx(ctx).Warn("remove follow relation from cache failed", logger.Uint("user_id", userID), logger.Uint("blogger_id", bloggerID), logger.Err(err))
	}
	// followers
	keyf := fmt.Sprintf("%s%d", socialFollowersKey, bloggerID)
	err = s.cache.ZRem(ctx, keyf, userID)
	if err != nil {
		logger.Ctx(ctx).Warn("remove follower relation from cache failed", logger.Uint("blogger_id", bloggerID), logger.Uint("user_id", userID), logger.Err(err))
	}

	return nil
}

// socialListDirection 标识关注/粉丝列表方向，两个接口共用同一套缓存+游标分页逻辑。
type socialListDirection int

const (
	socialListBloggers  socialListDirection = iota // 关注列表
	socialListFollowers                            // 粉丝列表
)

// socialListPlan 描述一个列表方向在缓存 key、SQL 查询与关联 userID 字段上的差异。
type socialListPlan struct {
	keyPrefix  string
	errorLabel string
	relatedID  func(*model.Social) uint
	// listPage 直接走数据库 keyset 分页（COUNT + LIMIT），不加载全量记录。
	listPage func(context.Context, uint, time.Time, uint, int, bool) ([]*model.Social, int64, error)
}

// plan 返回指定方向的列表配置。
func (s *SocialService) socialListPlan(dir socialListDirection) socialListPlan {
	if dir == socialListFollowers {
		return socialListPlan{
			keyPrefix:  socialFollowersKey,
			errorLabel: "get followers",
			relatedID:  func(social *model.Social) uint { return social.FollowerID },
			listPage:   s.SocialRepo.ListFollowers,
		}
	}
	return socialListPlan{
		keyPrefix:  socialBloggersKey,
		errorLabel: "get bloggers",
		relatedID:  func(social *model.Social) uint { return social.BloggerID },
		listPage:   s.SocialRepo.ListBloggers,
	}
}

// GetBloggers 获取用户的关注列表，使用 (created_at, blogger_id) 复合游标分页。
func (s *SocialService) GetBloggers(ctx context.Context, targetUserID, userID uint, cursor dto.FeedCursor, pageSize uint) ([]*dto.UserSocialSimpleResponse, int64, *dto.FeedCursor, error) {
	return s.getSocialListPage(ctx, socialListBloggers, targetUserID, userID, cursor, pageSize)
}

// GetFollowers 获取用户的粉丝列表，使用 (created_at, follower_id) 复合游标分页。
func (s *SocialService) GetFollowers(ctx context.Context, targetUserID, userID uint, cursor dto.FeedCursor, pageSize uint) ([]*dto.UserSocialSimpleResponse, int64, *dto.FeedCursor, error) {
	return s.getSocialListPage(ctx, socialListFollowers, targetUserID, userID, cursor, pageSize)
}

// getSocialListPage 关注/粉丝列表公共实现：优先读缓存，未命中回源数据库并按复合游标分页。
func (s *SocialService) getSocialListPage(ctx context.Context, dir socialListDirection, targetUserID, userID uint, cursor dto.FeedCursor, pageSize uint) ([]*dto.UserSocialSimpleResponse, int64, *dto.FeedCursor, error) {
	plan := s.socialListPlan(dir)
	if pageSize == 0 {
		pageSize = 20
	}
	lastUserID, hasCursor, err := parseFeedCursorVid(cursor.LastUserID)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%s: %w", plan.errorLabel, err)
	}
	var lastCreatedAt time.Time
	if hasCursor {
		if cursor.LastCreatedAt.IsZero() {
			return nil, 0, nil, fmt.Errorf("%s: incomplete social cursor: last_created_at is required when last_uid is set", plan.errorLabel)
		}
		lastCreatedAt = cursor.LastCreatedAt
	}

	key := fmt.Sprintf("%s%d", plan.keyPrefix, targetUserID)
	// 尝试从Redis ZSet拿：按 (score, member) 复合游标读取，避免 ZRange 的 offset 分页。
	// key 不存在属于正常 miss；Redis 异常也降级到数据库，避免缓存故障拖垮读接口。
	total, cacheHit, err := s.readSocialCacheCount(ctx, key)
	if err != nil {
		logger.Ctx(ctx).Warn("read social cache failed, fallback to DB",
			logger.String("key", key), logger.Uint("target_user_id", targetUserID), logger.Err(err))
		cacheHit = false
	}
	if cacheHit {
		// 空集合占位说明缓存已确认该用户没有关注/粉丝，直接返回空页，避免穿透数据库
		if total == 0 {
			return []*dto.UserSocialSimpleResponse{}, 0, nil, nil
		}
		userIDStrList, pageErr := s.socialCachePageByCursor(ctx, key, lastCreatedAt, lastUserID, int(pageSize))
		if pageErr != nil && !errors.Is(pageErr, cache.ErrCacheMiss) {
			return nil, 0, nil, fmt.Errorf("%s: read cache page key=%s: %w", plan.errorLabel, key, pageErr)
		}
		if pageErr == nil {
			nextCursor, cursorErr := s.socialCursorFromCache(ctx, key, userIDStrList, int(pageSize))
			if cursorErr != nil && !errors.Is(cursorErr, cache.ErrCacheMiss) {
				return nil, 0, nil, fmt.Errorf("%s: build cursor from cache key=%s: %w", plan.errorLabel, key, cursorErr)
			}
			if cursorErr == nil {
				// 转uint，批量拿用户信息
				userIDList, convErr := strSlice2UintSlice(userIDStrList)
				if convErr != nil {
					logger.Ctx(ctx).Error("parse social cache user ids failed", logger.String("key", key), logger.Err(convErr))
				} else {
					userList, batchErr := s.batchGetUserSimple(ctx, userIDList)
					if batchErr == nil {
						if fillErr := s.fillIsFollow(ctx, dir, userID, targetUserID, userList); fillErr != nil {
							return nil, 0, nil, fillErr
						}
						return userList, total, nextCursor, nil
					}
					logger.Ctx(ctx).Error("batch get user simple info failed", logger.Err(batchErr))
				}
			} else {
				// 分页游标成员在读取过程中过期，按缓存未命中原生回源数据库
				logger.Ctx(ctx).Warn("social cache cursor expired, fallback to DB", logger.String("key", key), logger.Err(cursorErr))
			}
		} else {
			// 游标成员已不在同分集合中（取关/部分重建），不能假设缓存页完整，回源数据库
			logger.Ctx(ctx).Warn("social cache page cursor missing, fallback to DB", logger.String("key", key), logger.Err(pageErr))
		}
	}

	// Cache Miss：直接走数据库 keyset 分页（COUNT + LIMIT）。
	// 不再把全量关注/粉丝加载进内存排序和异步重建 ZSet，避免大 V 列表拉爆内存。
	socials, total, err := plan.listPage(ctx, targetUserID, lastCreatedAt, lastUserID, int(pageSize), true)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%s: list from db: %w", plan.errorLabel, err)
	}

	// 将socials转换为userIDList（SQL 已按 created_at DESC, userID DESC 排好序）
	userIDList := make([]uint, len(socials))
	for i, social := range socials {
		userIDList[i] = plan.relatedID(social)
	}

	// 批量获取用户简单信息
	userList, err := s.batchGetUserSimple(ctx, userIDList)
	if err != nil {
		logger.Ctx(ctx).Error("batch get user simple info failed", logger.Err(err))
		return nil, 0, nil, fmt.Errorf("%s: batch get user info: %w", plan.errorLabel, err)
	}

	// 根据实际情况处理IsFollow字段
	if err := s.fillIsFollow(ctx, dir, userID, targetUserID, userList); err != nil {
		return nil, 0, nil, err
	}

	nextCursor := socialCursorFromDBPage(socials, int(pageSize), plan.relatedID)
	return userList, total, nextCursor, nil
}

// fillIsFollow 按列表方向和访问者身份填充 IsFollow：
// 查看自己的关注列表时全部为已关注；查看自己的粉丝列表时判断粉丝是否回关；
// 查看他人列表时判断当前登录用户是否关注列表中的用户。
func (s *SocialService) fillIsFollow(ctx context.Context, dir socialListDirection, loginUserID, targetUserID uint, userList []*dto.UserSocialSimpleResponse) error {
	if loginUserID == 0 || len(userList) == 0 {
		return nil
	}
	if loginUserID == targetUserID && dir == socialListBloggers {
		// 查看自己的关注列表，所有人都是已关注
		for _, user := range userList {
			user.IsFollow = true
		}
		return nil
	}

	userIDs := make([]uint, 0, len(userList))
	for _, user := range userList {
		userIDs = append(userIDs, user.UserID)
	}

	if loginUserID == targetUserID {
		// 查看自己的粉丝列表：IsFollow = 粉丝有没有关注我，一次 IN 查询
		followedBy, err := s.SocialRepo.BatchIsFollowedBy(ctx, loginUserID, userIDs)
		if err != nil {
			logger.Ctx(ctx).Error("batch check fan follow relation failed", logger.Uint("target_user_id", loginUserID), logger.Err(err))
			return fmt.Errorf("batch check fan follow relation for user %d: %w", loginUserID, err)
		}
		for _, user := range userList {
			user.IsFollow = followedBy[user.UserID]
		}
		return nil
	}

	// 查看他人列表：判断当前登录用户是否关注列表中的用户，一次 IN 查询
	following, err := s.SocialRepo.BatchIsFollowing(ctx, loginUserID, userIDs)
	if err != nil {
		logger.Ctx(ctx).Error("batch check follow relation failed", logger.Uint("user_id", loginUserID), logger.Err(err))
		return fmt.Errorf("batch check follow relation for user %d: %w", loginUserID, err)
	}
	for _, user := range userList {
		user.IsFollow = following[user.UserID]
	}
	return nil
}

// asyncRewriteSocialZSet 异步全量回写关注/粉丝 ZSet。
// 同一个 key 同时只允许一个回写 goroutine，且回写总数不超过 socialCacheWriteConcurrency，
// 避免高并发缓存miss时每个请求都启动 goroutine 刷写同一个 ZSet。
func (s *SocialService) asyncRewriteSocialZSet(ctx context.Context, key string, zList []redis.Z) {
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
				logger.Ctx(ctx).Error("social cache write goroutine panic", logger.Any("panic", r))
			}
			<-socialCacheWriteSem
			socialCacheWriteInflight.Delete(key)
		}()

		// 异步回写与请求生命周期解耦，使用独立超时 ctx 控制最坏耗时；
		// WithoutCancel 保留 ctx 中的 requestId 等值，但不随请求结束被取消。
		cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), socialCacheWriteTimeout)
		defer cancel()

		// 全量重建必须先删旧 key 再写入，否则只 ZAdd 会把历史脏成员（已取关/错误回填）
		// 永远保留在集合里，读路径又只以 key 是否存在判断缓存是否完整。
		if err := s.cache.Delete(cacheCtx, key); err != nil {
			logger.Ctx(ctx).Error("delete old social cache before rebuild failed", logger.String("key", key), logger.Err(err))
			return
		}
		// 空列表也会写入占位成员，缓存“该用户没有关注/粉丝”这一结果，避免 0 值反复穿透数据库
		if err := s.writeSocialZSet(cacheCtx, key, zList); err != nil {
			logger.Ctx(ctx).Error("batch zadd social cache failed", logger.String("key", key), logger.Err(err))
		}
	}()
}

// socialPageByCursor 从全量社交记录中，按 (created_at 秒, userID) 复合游标截取一页。
// 这里把排序键统一到秒 + userID，与 Redis ZSet 的 score(秒) + member 顺序保持一致，
// 避免缓存与数据库两条路径的游标语义不同导致同一秒内的记录重复或丢失。
func socialPageByCursor(socials []*model.Social, lastCreatedAt time.Time, lastUserID uint, pageSize int, userIDOf func(*model.Social) uint) []*model.Social {
	sort.SliceStable(socials, func(i, j int) bool {
		leftSec, rightSec := socials[i].CreatedAt.Unix(), socials[j].CreatedAt.Unix()
		if leftSec != rightSec {
			return leftSec > rightSec
		}
		return userIDOf(socials[i]) > userIDOf(socials[j])
	})

	page := make([]*model.Social, 0, pageSize)
	cursorSec := lastCreatedAt.Unix()
	for _, social := range socials {
		if lastUserID != 0 {
			sec := social.CreatedAt.Unix()
			if sec > cursorSec {
				continue
			}
			if sec == cursorSec && userIDOf(social) >= lastUserID {
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
func socialCursorFromDBPage(pageSocials []*model.Social, pageSize int, userIDOf func(*model.Social) uint) *dto.FeedCursor {
	if pageSize <= 0 || len(pageSocials) < pageSize {
		return nil
	}
	last := pageSocials[len(pageSocials)-1]
	return &dto.FeedCursor{
		LastUserID:    strconv.FormatUint(uint64(userIDOf(last)), 10),
		LastCreatedAt: time.Unix(last.CreatedAt.Unix(), 0),
	}
}

// socialCachePageByCursor 按 (score, member) 复合游标从 ZSet 读取一页 userID。
// score 为关注时间戳（秒），同一秒内按 member 倒序排列；先定位游标 member 在同分桶中的位置，
// 再取更早分数的成员，避免 ZRange offset 分页在数据变动时重复或漏数据。
func (s *SocialService) socialCachePageByCursor(ctx context.Context, key string, lastCreatedAt time.Time, lastUserID uint, pageSize int) ([]string, error) {
	limit := int64(pageSize)
	if lastUserID == 0 {
		members, err := s.cache.ZRange(ctx, key, 0, limit-1, true)
		if err != nil {
			return nil, err
		}
		return filterSocialMembers(members), nil
	}

	scoreStr := strconv.FormatInt(lastCreatedAt.Unix(), 10)
	tieMembers, err := s.cache.ZRangeByScore(ctx, key, scoreStr, scoreStr, 0, -1, true)
	if err != nil {
		return nil, err
	}

	// Redis ZSet 同分成员按字符串字典序返回（"9" > "10"），而数据库路径按 userID 数值倒序。
	// 这里显式解析成 uint 后排序，保证缓存/数据库两条路径的翻页顺序一致。
	sort.Slice(tieMembers, func(i, j int) bool {
		left, leftErr := strconv.ParseUint(tieMembers[i], 10, 64)
		right, rightErr := strconv.ParseUint(tieMembers[j], 10, 64)
		if leftErr != nil || rightErr != nil {
			return tieMembers[i] > tieMembers[j]
		}
		return left > right
	})

	result := make([]string, 0, pageSize)
	cursorMember := strconv.FormatUint(uint64(lastUserID), 10)
	cursorFound := false
	for i, member := range tieMembers {
		if member == cursorMember {
			result = append(result, tieMembers[i+1:]...)
			cursorFound = true
			break
		}
	}
	if !cursorFound {
		// 游标成员已不在 ZSet（取关或部分重建），无法确认同秒成员是否完整，
		// 交给上层回源数据库，避免静默丢掉同一秒里游标之后的记录。
		return nil, cache.ErrCacheMiss
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
	return filterSocialMembers(result), nil
}

// socialCursorFromCache 用缓存页的最后一条 userID + ZSet score 生成下一页游标。
func (s *SocialService) socialCursorFromCache(ctx context.Context, key string, userIDs []string, pageSize int) (*dto.FeedCursor, error) {
	if pageSize <= 0 || len(userIDs) < pageSize {
		return nil, nil
	}
	last := userIDs[len(userIDs)-1]
	score, err := s.cache.ZScore(ctx, key, last)
	if err != nil {
		return nil, err
	}
	return &dto.FeedCursor{
		LastUserID:    last,
		LastCreatedAt: time.Unix(int64(score), 0),
	}, nil
}

// strSlice2UintSlice 将字符串切片转换为 uint 切片，非法成员直接返回错误而不是静默置 0。
func strSlice2UintSlice(strs []string) ([]uint, error) {
	uints := make([]uint, len(strs))
	for i, s := range strs {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse user id %q: %w", s, err)
		}
		uints[i] = uint(v)
	}
	return uints, nil
}

// batchGetUserSimple 批量获取用户的简单信息：一次 IN 查询，避免逐条 FindByID 的 N+1。
// 已注销/不存在的用户不会出现在 BatchGetProfiles 结果中，这里直接跳过，
// 避免一条脏关注记录导致整页列表失败。
func (s *SocialService) batchGetUserSimple(ctx context.Context, userIDList []uint) ([]*dto.UserSocialSimpleResponse, error) {
	if len(userIDList) == 0 {
		return []*dto.UserSocialSimpleResponse{}, nil
	}

	profiles, err := s.UserService.BatchGetProfiles(ctx, userIDList)
	if err != nil {
		return nil, fmt.Errorf("batch get user profiles: %w", err)
	}

	simpleList := make([]*dto.UserSocialSimpleResponse, 0, len(userIDList))
	for _, userID := range userIDList {
		user, ok := profiles[userID]
		if !ok {
			logger.Ctx(ctx).Warn("social list user missing, skipped", logger.Uint("user_id", userID))
			continue
		}
		simpleList = append(simpleList, &dto.UserSocialSimpleResponse{
			UserID:    user.ID,
			UserName:  user.Username,
			AvatarURL: user.AvatarURL,
			IsFollow:  false, // 后面根据实际情况处理
		})
	}

	return simpleList, nil
}

// IsFollow 获取两者双向关注关系
func (s *SocialService) IsFollow(ctx context.Context, userID, targetID uint) (bool, bool, error) {
	if userID == 0 || targetID == 0 {
		return false, false, errors.New("user id and target id must be non-zero")
	}
	if userID == targetID {
		return false, false, errors.New("user cannot follow themselves")
	}

	// userID 是否关注 targetID
	keyUserIDBloggers := fmt.Sprintf("%s%d", socialBloggersKey, userID)
	keyUserIDFollowers := fmt.Sprintf("%s%d", socialFollowersKey, userID)
	keyTIDBloggers := fmt.Sprintf("%s%d", socialBloggersKey, targetID)
	keyTIDFollowers := fmt.Sprintf("%s%d", socialFollowersKey, targetID)

	isFollow, err := s.cache.ZIsMember(ctx, keyUserIDBloggers, targetID)
	if err != nil {
		logger.Ctx(ctx).Error("check follow relation in cache failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
		return false, false, fmt.Errorf("check follow cache for user %d target %d: %w", userID, targetID, err)
	}
	// 查库并回写缓存
	if !isFollow {
		isFollow, err = s.SocialRepo.IsFollowing(ctx, userID, targetID)
		if err != nil {
			logger.Ctx(ctx).Error("check follow relation in DB failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
			return false, false, fmt.Errorf("check follow relation for user %d target %d: %w", userID, targetID, err)
		}
		// 数据库查到结果，回填缓存
		if isFollow {
			social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, userID, targetID)
			if err != nil {
				logger.Ctx(ctx).Error("find follow relation in DB failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
				return false, false, fmt.Errorf("find follow relation for user %d target %d: %w", userID, targetID, err)
			}

			// 关注缓存：仅 key 已存在时增量回填，避免生成残缺集合
			if err := s.zaddIfKeyExists(ctx, keyUserIDBloggers, targetID, social.CreatedAt); err != nil {
				logger.Ctx(ctx).Warn("zadd follow cache failed", logger.String("key", keyUserIDBloggers), logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
			}
			// 粉丝缓存：同上
			if err := s.zaddIfKeyExists(ctx, keyTIDFollowers, userID, social.CreatedAt); err != nil {
				logger.Ctx(ctx).Warn("zadd follower cache failed", logger.String("key", keyTIDFollowers), logger.Uint("target_user_id", targetID), logger.Uint("user_id", userID), logger.Err(err))
			}
		}
	}

	// targetID是否关注userID
	isFan, err := s.cache.ZIsMember(ctx, keyUserIDFollowers, targetID)
	if err != nil {
		logger.Ctx(ctx).Error("check fan relation in cache failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
		return false, false, fmt.Errorf("check fan cache for user %d target %d: %w", userID, targetID, err)
	}
	// 查库并回写缓存
	if !isFan {
		isFan, err = s.SocialRepo.IsFollowing(ctx, targetID, userID)
		if err != nil {
			logger.Ctx(ctx).Error("check fan relation in DB failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
			return false, false, fmt.Errorf("check fan relation for user %d target %d: %w", userID, targetID, err)
		}

		if isFan {
			social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, targetID, userID)
			if err != nil {
				logger.Ctx(ctx).Error("find fan relation in DB failed", logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
				return false, false, fmt.Errorf("find fan relation for user %d target %d: %w", userID, targetID, err)
			}

			// 关注缓存：仅 key 已存在时增量回填，避免生成残缺集合
			if err := s.zaddIfKeyExists(ctx, keyTIDBloggers, userID, social.CreatedAt); err != nil {
				logger.Ctx(ctx).Warn("zadd follow cache failed", logger.String("key", keyTIDBloggers), logger.Uint("target_user_id", targetID), logger.Uint("user_id", userID), logger.Err(err))
			}
			// 粉丝缓存：同上
			if err := s.zaddIfKeyExists(ctx, keyUserIDFollowers, targetID, social.CreatedAt); err != nil {
				logger.Ctx(ctx).Warn("zadd follower cache failed", logger.String("key", keyUserIDFollowers), logger.Uint("user_id", userID), logger.Uint("target_user_id", targetID), logger.Err(err))
			}
		}
	}

	return isFollow, isFan, nil
}

// GetCounts 获取关注和粉丝数量。
// 缓存命中（包括缓存下来的真实 0）直接返回；只有 key 不存在时才回源数据库，
// 回源后按方向写回 ZSet 或空集合占位，避免 0 关注/0 粉丝用户每次请求都穿透数据库。
func (s *SocialService) GetCounts(ctx context.Context, targetID uint) (int64, int64, error) {
	if targetID == 0 {
		return 0, 0, errors.New("target id must be non-zero")
	}

	keyBloggers := fmt.Sprintf("%s%d", socialBloggersKey, targetID)
	keyFollowers := fmt.Sprintf("%s%d", socialFollowersKey, targetID)

	// 缓存读失败不阻断接口：按未知处理，继续用 MySQL 计数降级返回。
	followCount, followKnown, err := s.readSocialCacheCount(ctx, keyBloggers)
	if err != nil {
		logger.Ctx(ctx).Warn("get follow count from cache failed, fallback to DB", logger.Uint("target_user_id", targetID), logger.Err(err))
		followKnown = false
	}
	fanCount, fanKnown, err := s.readSocialCacheCount(ctx, keyFollowers)
	if err != nil {
		logger.Ctx(ctx).Warn("get fan count from cache failed, fallback to DB", logger.Uint("target_user_id", targetID), logger.Err(err))
		fanKnown = false
	}
	if followKnown && fanKnown {
		return followCount, fanCount, nil
	}

	followCountDB, fanCountDB, err := s.SocialRepo.GetCounts(ctx, targetID)
	if err != nil {
		logger.Ctx(ctx).Error("get counts from DB failed", logger.Uint("target_user_id", targetID), logger.Err(err))
		return 0, 0, fmt.Errorf("get counts from db: %w", err)
	}

	// 只回源缓存未知的一侧，已知的一侧继续使用缓存值。
	if !followKnown {
		followCount = followCountDB
		socials, err := s.SocialRepo.ListAllBloggers(ctx, targetID, true)
		if err != nil {
			logger.Ctx(ctx).Error("list all bloggers for cache failed", logger.Uint("target_user_id", targetID), logger.Err(err))
		} else {
			zList := make([]redis.Z, 0, len(socials))
			for _, social := range socials {
				zList = append(zList, redis.Z{
					Score:  float64(social.CreatedAt.Unix()),
					Member: social.BloggerID,
				})
			}
			// 0 关注时写入空集合占位，下次请求直接命中缓存
			if followCountDB > 0 && len(zList) == 0 {
				logger.Ctx(ctx).Warn("follow count mismatch", logger.Uint("target_user_id", targetID), logger.Int64("db_count", followCountDB))
			} else if err := s.writeSocialZSet(ctx, keyBloggers, zList); err != nil {
				logger.Ctx(ctx).Error("write follow list to cache failed", logger.Uint("target_user_id", targetID), logger.Err(err))
			}
		}
	}

	if !fanKnown {
		fanCount = fanCountDB
		socials, err := s.SocialRepo.ListAllFollowers(ctx, targetID, true)
		if err != nil {
			logger.Ctx(ctx).Error("list all followers for cache failed", logger.Uint("target_user_id", targetID), logger.Err(err))
		} else {
			zList := make([]redis.Z, 0, len(socials))
			for _, social := range socials {
				zList = append(zList, redis.Z{
					Score:  float64(social.CreatedAt.Unix()),
					Member: social.FollowerID,
				})
			}
			// 0 粉丝时写入空集合占位，下次请求直接命中缓存
			if fanCountDB > 0 && len(zList) == 0 {
				logger.Ctx(ctx).Warn("fan count mismatch", logger.Uint("target_user_id", targetID), logger.Int64("db_count", fanCountDB))
			} else if err := s.writeSocialZSet(ctx, keyFollowers, zList); err != nil {
				logger.Ctx(ctx).Error("write fan list to cache failed", logger.Uint("target_user_id", targetID), logger.Err(err))
			}
		}
	}

	return followCount, fanCount, nil
}

// BatchIsFollow 批量查询：loginUserID 是否关注 authorUserIDList 里面的作者
func (s *SocialService) BatchIsFollow(ctx context.Context, loginUserID uint, authorUserIDList []uint) (map[uint]bool, error) {
	resMap := make(map[uint]bool)
	if loginUserID == 0 || len(authorUserIDList) == 0 {
		return resMap, nil
	}
	// 缓存禁用时直接按数据库关注关系查询
	if !s.cache.IsEnabled() {
		for _, authorUserID := range authorUserIDList {
			isFollowing, err := s.SocialRepo.IsFollowing(ctx, loginUserID, authorUserID)
			if err != nil {
				return nil, fmt.Errorf("failed to query follow relation from db for user %d target %d: %w", loginUserID, authorUserID, err)
			}
			resMap[authorUserID] = isFollowing
		}
		return resMap, nil
	}
	key := fmt.Sprintf("%s%d", socialBloggersKey, loginUserID)

	// 关注集合整体不存在时，一次性回源全量关注列表并重建 ZSet。
	// 不能按单个作者逐条 ZAdd，否则冷缓存下只写入部分成员，
	// 会让 GetCounts/GetBloggers 把不完整的集合当成全量缓存。
	keyExists, err := s.cache.Exists(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("batch is follow: check cache key for user %d: %w", loginUserID, err)
	}
	if !keyExists {
		socials, err := s.SocialRepo.ListAllBloggers(ctx, loginUserID, true)
		if err != nil {
			return nil, fmt.Errorf("batch is follow: list follow relations for user %d: %w", loginUserID, err)
		}
		followSet := make(map[uint]struct{}, len(socials))
		zList := make([]redis.Z, 0, len(socials))
		for _, social := range socials {
			followSet[social.BloggerID] = struct{}{}
			zList = append(zList, redis.Z{
				Score:  float64(social.CreatedAt.Unix()),
				Member: social.BloggerID,
			})
		}
		// 异步全量重建（内部先删旧 key 再写入），不阻塞本次批量查询结果。
		s.asyncRewriteSocialZSet(ctx, key, zList)
		for _, authorUserID := range authorUserIDList {
			_, ok := followSet[authorUserID]
			resMap[authorUserID] = ok
		}
		return resMap, nil
	}

	for _, authorUserID := range authorUserIDList {
		if authorUserID == 0 {
			resMap[authorUserID] = false
			continue
		}
		isMember, err := s.cache.ZIsMember(ctx, key, strconv.FormatUint(uint64(authorUserID), 10))
		if err != nil {
			return nil, fmt.Errorf("batch is follow: check cache for user %d target %d: %w", loginUserID, authorUserID, err)
		}
		if isMember {
			resMap[authorUserID] = true
			continue
		}

		// 缓存未命中（key 不存在或关注集合尚未同步）时回源数据库，避免把已关注误判为未关注
		isFollowing, err := s.SocialRepo.IsFollowing(ctx, loginUserID, authorUserID)
		if err != nil {
			return nil, fmt.Errorf("batch is follow: query db for user %d target %d: %w", loginUserID, authorUserID, err)
		}
		resMap[authorUserID] = isFollowing
		if !isFollowing {
			continue
		}

		// DB 确认已关注，按原有 ZSet 结构回填：member=博主userID，score=关注时间戳（秒）
		social, err := s.SocialRepo.FindByFollowerAndBlogger(ctx, loginUserID, authorUserID)
		if err != nil {
			logger.Ctx(ctx).Error("find follow relation for cache backfill failed", logger.Uint("user_id", loginUserID), logger.Uint("blogger_id", authorUserID), logger.Err(err))
			continue
		}
		if err := s.cache.ZAdd(ctx, key, []redis.Z{{
			Score:  float64(social.CreatedAt.Unix()),
			Member: authorUserID,
		}}, socialUserIDsTTL); err != nil {
			logger.Ctx(ctx).Warn("backfill follow cache failed", logger.Uint("user_id", loginUserID), logger.Uint("blogger_id", authorUserID), logger.Err(err))
		}
	}
	return resMap, nil
}
