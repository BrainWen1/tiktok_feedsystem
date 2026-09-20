package service

import (
	"context"
	"errors"
	"feedsystem/internal/repo"
	"log"
)

type SocialService struct {
	SocialRepo *repo.SocialRepo
}

func NewSocialService(socialRepo *repo.SocialRepo) *SocialService {
	return &SocialService{SocialRepo: socialRepo}
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
	isFollowing, err := s.SocialRepo.IsFollowing(ctx, uid, bloggerID)
	if err != nil {
		log.Printf("Failed to check follow relation: uid=%d bloggerID=%d err=%v", uid, bloggerID, err)
		return err
	}
	if isFollowing {
		return errors.New("user already follows this blogger")
	}

	return s.SocialRepo.CreateFollow(ctx, uid, bloggerID)
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

	return nil
}
