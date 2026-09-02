package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/repo"
	"log"
)

type LikeService struct {
	likeRepo *repo.LikeRepo
	likeMQ   *mq.LikeMQ
}

func NewLikeService(likeRepo *repo.LikeRepo, likeMQ *mq.LikeMQ) *LikeService {
	return &LikeService{likeRepo: likeRepo, likeMQ: likeMQ}
}

// LikeVideo 点赞视频
func (s *LikeService) LikeVideo(ctx context.Context, uid, vid uint) error {
	if uid == 0 || vid == 0 {
		log.Printf("Invalid uid or vid: uid=%d, vid=%d", uid, vid)
		return errors.New("uid and vid must be non-zero")
	}
	// 前置查询：拦截重复点击，减少无效MQ消息
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	if err != nil {
		return err
	}
	if isLiked {
		return errors.New("video already liked by user")
	}

	//组装消息
	err = s.likeMQ.Like(ctx, uid, vid)
	if err != nil {
		log.Printf("Failed to publish like event to MQ: %v", err)
		// MQ投递失败，降级方案：同步直接写数据库
		err = s.likeRepo.CreateLike(ctx, uid, vid)
		return err
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// UnlikeVideo 取消点赞视频
func (s *LikeService) UnlikeVideo(ctx context.Context, uid, vid uint) error {
	if uid == 0 || vid == 0 {
		log.Printf("Invalid uid or vid: uid=%d, vid=%d", uid, vid)
		return errors.New("uid and vid must be non-zero")
	}
	// 前置查询：拦截重复点击，减少无效MQ消息
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	if err != nil {
		return err
	}
	if !isLiked {
		return errors.New("video not liked by user")
	}

	//组装消息
	err = s.likeMQ.Unlike(ctx, uid, vid)
	if err != nil {
		log.Printf("Failed to publish unlike event to MQ: %v", err)
		// MQ投递失败，降级方案：同步直接写数据库
		err = s.likeRepo.DeleteLike(ctx, uid, vid)
		return err
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// IsLiked 检查用户是否点赞了视频
func (s *LikeService) IsLiked(ctx context.Context, uid, vid uint) (dto.IsLikedResponse, error) {
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	return dto.IsLikedResponse{IsLiked: isLiked}, err
}
