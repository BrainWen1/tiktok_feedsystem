package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/repo"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	socialUidsTTL      = 24 * 60 * 60 * time.Second // 关注列表缓存的过期时间，单位秒
	socialBloggersKey  = "social:bloggers:uid:"     // 关注列表缓存的key模板
	socialFollowersKey = "social:followers:uid:"    // 粉丝列表缓存的key模板
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

// GetBloggers 获取用户的关注列表
func (s *SocialService) GetBloggers(ctx context.Context, targetUid, pageNum, pageSize, uid uint) ([]*dto.UserSocialSimpleResponse, int64, error) {
	key := fmt.Sprintf("%s%d", socialBloggersKey, targetUid)
	// 尝试从Redis ZSet拿
	exists, err := s.cache.Exists(ctx, key)
	if err == nil && exists {
		// 总数直接ZCARD
		total, _ := s.cache.ZCard(ctx, key)
		// 分页uid列表，按关注时间倒序
		uidStrList, _ := s.cache.ZRange(ctx, key, int64((pageNum-1)*pageSize), int64(pageNum*pageSize-1), true)
		// 转uint，批量拿用户信息
		uidList := strSlice2UintSlice(uidStrList)
		userList, err := s.batchGetUserSimple(ctx, uidList)
		if err != nil {
			log.Printf("Failed to batch get user simple info: %v", err)
			return nil, 0, err
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
						return nil, 0, err
					}
					user.IsFollow = isFollowing
				}
			}
		}

		return userList, total, nil
	}

	// Cache Miss，查MySQL
	socials, err := s.SocialRepo.ListAllBloggers(ctx, targetUid, true) // 按关注时间倒序
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(socials))
	start := int((pageNum - 1) * pageSize)
	if start >= len(socials) {
		return []*dto.UserSocialSimpleResponse{}, total, nil
	}
	end := start + int(pageSize)
	if end > len(socials) {
		end = len(socials)
	}
	pageSocials := socials[start:end]

	// 将socials转换为uidList
	uidlist := make([]uint, len(pageSocials))
	for i, social := range pageSocials { // 保持倒序关系不变
		uidlist[i] = social.BloggerID
	}

	// 批量获取用户简单信息
	userList, err := s.batchGetUserSimple(ctx, uidlist)
	if err != nil {
		log.Printf("Failed to batch get user simple info: %v", err)
		return nil, 0, err
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
					return nil, 0, err
				}
				user.IsFollow = isFollowing
			}
		}
	}

	// 异步回写ZSet缓存：每条关注记录，member=博主uid，score=关注创建时间戳
	go func() {
		// 注册recover，防止goroutine panic导致程序崩溃
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ZAdd goroutine panic: %v", r)
			}
		}()

		cacheCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// 组装ZSet数据
		var zList []redis.Z
		for _, social := range socials { // 全量写入
			zList = append(zList, redis.Z{
				Score:  float64(social.CreatedAt.Unix()),
				Member: social.BloggerID,
			})
		}
		// 批量写入
		err := s.cache.ZAdd(cacheCtx, key, zList, socialUidsTTL)
		if err != nil {
			log.Printf("batch zadd follow cache failed, key=%s, err:%v", key, err)
		}
	}()

	return userList, total, nil
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

// GetFollowers 获取用户的粉丝列表（几乎与GetBloggers一模一样）
func (s *SocialService) GetFollowers(ctx context.Context, targetUid, pageNum, pageSize, uid uint) ([]*dto.UserSocialSimpleResponse, int64, error) {
	key := fmt.Sprintf("%s%d", socialFollowersKey, targetUid)
	// 尝试从Redis ZSet拿
	exists, err := s.cache.Exists(ctx, key)
	if err == nil && exists {
		// 总数直接ZCARD
		total, _ := s.cache.ZCard(ctx, key)
		// 分页uid列表，按关注时间倒序（粉丝新增关注的时间）
		uidStrList, _ := s.cache.ZRange(ctx, key, int64((pageNum-1)*pageSize), int64(pageNum*pageSize-1), true)
		// 转uint，批量拿用户信息
		uidList := strSlice2UintSlice(uidStrList)
		userList, err := s.batchGetUserSimple(ctx, uidList)
		if err != nil {
			log.Printf("Failed to batch get user simple info: %v", err)
			return nil, 0, err
		}
		// 根据实际情况处理IsFollow字段
		if uid != 0 {
			if uid == targetUid {
				// 查看自己的粉丝列表：IsFollow = 这个粉丝有没有关注我
				for _, user := range userList {
					isFollowing, err := s.SocialRepo.IsFollowing(ctx, user.Uid, uid)
					if err != nil {
						log.Printf("Failed to check follow relation for fan=%d target=%d: %v", user.Uid, uid, err)
						return nil, 0, err
					}
					user.IsFollow = isFollowing
				}
			} else {
				// 看别人的粉丝列表：当前登录用户uid，是否关注这个粉丝user.Uid
				for _, user := range userList {
					isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, user.Uid)
					if err != nil {
						log.Printf("Failed to check follow relation for uid=%d and fanID=%d: %v", uid, user.Uid, err)
						return nil, 0, err
					}
					user.IsFollow = isFollowing
				}
			}
		}
		return userList, total, nil
	}
	// Cache Miss，查MySQL
	socials, err := s.SocialRepo.ListAllFollowers(ctx, targetUid, true) // 按关注时间倒序
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(socials))
	start := int((pageNum - 1) * pageSize)
	if start >= len(socials) {
		return []*dto.UserSocialSimpleResponse{}, total, nil
	}
	end := start + int(pageSize)
	if end > len(socials) {
		end = len(socials)
	}
	pageSocials := socials[start:end]
	// 将socials转换为uidList：粉丝ID是FollowerID
	uidlist := make([]uint, len(pageSocials))
	for i, social := range pageSocials {
		uidlist[i] = social.FollowerID
	}
	// 批量获取用户简单信息
	userList, err := s.batchGetUserSimple(ctx, uidlist)
	if err != nil {
		log.Printf("Failed to batch get user simple info: %v", err)
		return nil, 0, err
	}
	// 根据实际情况处理IsFollow字段
	if uid != 0 {
		if uid == targetUid {
			// 查看自己的粉丝列表：IsFollow = 粉丝有没有关注我
			for _, user := range userList {
				isFollowing, err := s.SocialRepo.IsFollowing(ctx, user.Uid, uid)
				if err != nil {
					log.Printf("Failed to check follow relation for fan=%d target=%d: %v", user.Uid, uid, err)
					return nil, 0, err
				}
				user.IsFollow = isFollowing
			}
		} else {
			// 看别人的粉丝列表：登录用户是否关注这个粉丝
			for _, user := range userList {
				isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, user.Uid)
				if err != nil {
					log.Printf("Failed to check follow relation for uid=%d and fanID=%d: %v", uid, user.Uid, err)
					return nil, 0, err
				}
				user.IsFollow = isFollowing
			}
		}
	}
	// 异步回写ZSet缓存：member=粉丝uid，score=关注创建时间戳
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ZAdd goroutine panic: %v", r)
			}
		}()

		cacheCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var zList []redis.Z
		for _, social := range socials {
			zList = append(zList, redis.Z{
				Score:  float64(social.CreatedAt.Unix()),
				Member: social.FollowerID,
			})
		}
		err := s.cache.ZAdd(cacheCtx, key, zList, socialUidsTTL)
		if err != nil {
			log.Printf("batch zadd follower cache failed, key=%s, err:%v", key, err)
		}
	}()
	return userList, total, nil
}
