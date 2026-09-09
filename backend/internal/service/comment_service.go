package service

import (
	"context"
	"errors"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"log"
)

type CommentService struct {
	CommentRepo *repo.CommentRepo
	CommentMQ   *mq.CommentMQ
}

func NewCommentService(commentRepo *repo.CommentRepo, commentMQ *mq.CommentMQ) *CommentService {
	return &CommentService{
		CommentRepo: commentRepo,
		CommentMQ:   commentMQ,
	}
}

func (s *CommentService) PublishComment(ctx context.Context, vid, uid, pid uint, username, content string) error {
	if vid == 0 || uid == 0 || content == "" {
		log.Printf("Invalid parameters: vid=%d, uid=%d, content=%s", vid, uid, content)
		return errors.New("video ID, user ID, and content must be non-zero and non-empty")
	}

	//组装消息
	comment := &model.Comment{
		VideoID:  vid,
		UserID:   uid,
		ParentID: pid,
		UserName: username,
		Content:  content,
	}
	err := s.CommentMQ.PublishComment(ctx, comment)
	if err != nil {
		log.Printf("Failed to publish publish-comment event to MQ: %v", err)
		// MQ投递失败，降级方案：同步直接写数据库
		err = s.CommentRepo.CreateComment(ctx, comment)
		return err
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}
