package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/repo"
)

type LikeService struct {
	likeRepo *repo.LikeRepo
}

func NewLikeService(likeRepo *repo.LikeRepo) *LikeService {
	return &LikeService{likeRepo: likeRepo}
}

// LikeVideo 点赞视频
func (s *LikeService) LikeVideo(ctx context.Context, uid, vid uint) error {
	return s.likeRepo.CreateLike(ctx, uid, vid)
}

// UnlikeVideo 取消点赞视频
func (s *LikeService) UnlikeVideo(ctx context.Context, uid, vid uint) error {
	return s.likeRepo.DeleteLike(ctx, uid, vid)
}

// IsLiked 检查用户是否点赞了视频
func (s *LikeService) IsLiked(ctx context.Context, uid, vid uint) (dto.IsLikedResponse, error) {
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	return dto.IsLikedResponse{IsLiked: isLiked}, err
}
