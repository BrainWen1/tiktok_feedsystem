package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"fmt"
	"log"
	"strconv"
	"time"
)

type CommentService struct {
	CommentRepo *repo.CommentRepo
	CommentMQ   *mq.CommentMQ
	cache       *cache.RedisCache
	userService *UserService // 用于查询作者信息
	likeService *LikeService // 用于查询评论是否被点赞
}

func NewCommentService(commentRepo *repo.CommentRepo, commentMQ *mq.CommentMQ, cache *cache.RedisCache) *CommentService {
	return &CommentService{
		CommentRepo: commentRepo,
		CommentMQ:   commentMQ,
		cache:       cache,
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

func (s *CommentService) DeleteComment(ctx context.Context, uid, commentID uint) error {
	if uid == 0 || commentID == 0 {
		log.Printf("Invalid parameters: uid=%d, commentID=%d", uid, commentID)
		return errors.New("user ID and comment ID must be non-zero")
	}

	// 检查评论是否属于该用户
	comment, err := s.CommentRepo.FindByID(ctx, commentID)
	if err != nil {
		log.Printf("Failed to find comment with ID %d: %v", commentID, err)
		return errors.New("comment not found")
	}
	if comment.UserID != uid {
		log.Printf("User %d is not authorized to delete comment %d", uid, commentID)
		return errors.New("user is not authorized to delete this comment")
	}

	//组装消息
	err = s.CommentMQ.DeleteComment(ctx, comment)
	if err != nil {
		log.Printf("Failed to publish publish-comment event to MQ: %v", err)
		// MQ投递失败，降级方案：同步直接写数据库
		err = s.CommentRepo.DeleteComment(ctx, commentID)
		return err
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// GetCommentCount 获取视频评论总数（Redis计数器，DB兜底）
func (s *CommentService) GetCommentCount(ctx context.Context, vid uint) (int64, error) {
	key := fmt.Sprintf("video:comment_count:%d", vid)
	// 尝试读取redis
	val, err := s.cache.Get(ctx, key)
	if err == nil && val != "" {
		// 成功读取到redis计数器
		count, _ := strconv.ParseInt(val, 10, 64)
		// 刷新TTL，避免热视频缓存过期
		err = s.cache.Set(ctx, key, val, 24*time.Hour)
		if err != nil {
			log.Printf("Failed to refresh comment count TTL in cache for video %d: %v", vid, err)
		}
		return count, nil
	}

	// redis异常/key不存在，回数据库count兜底
	cnt, err := s.CommentRepo.CountByVideo(ctx, vid)
	if err != nil {
		log.Printf("Failed to count comments for video %d: %v", vid, err)
		return 0, err
	}

	// 回填redis，可以设置TTL，冷视频自动失效，下次访问重新count
	err = s.cache.Set(ctx, key, strconv.FormatInt(cnt, 10), 24*time.Hour)
	if err != nil {
		log.Printf("Failed to set comment count in cache for video %d: %v", vid, err)
	}
	return cnt, nil
}

// ListComment 游标分页查询评论列表
func (s *CommentService) ListComments(ctx context.Context, uid uint, req dto.ListCommentsRequest) (*dto.ListCommentsResponse, error) {
	// 游标分页查询评论
	comments, err := s.CommentRepo.ListByVideoCursor(ctx, req.VideoID, req.LastTime, req.LastID, req.PageSize)
	if err != nil {
		return nil, err
	}

	// 获取评论总数
	total, err := s.GetCommentCount(ctx, req.VideoID)
	if err != nil {
		return nil, err
	}

	// 组装返回DTO，填充作者信息、是否点赞（复用你之前的点赞缓存逻辑）
	respList := make([]model.Comment, 0, len(comments))
	var nextLastTime *int64
	var nextLastID *uint
	if len(comments) > 0 {
		// 取当前页最后一条，作为下一轮游标
		lastItem := comments[len(comments)-1]
		ts := lastItem.CreatedAt.Unix()
		nextLastTime = &ts
		nextLastID = &lastItem.ID
	}

	for _, comment := range comments {
		// 鸽：使用传入的uid查询是否点赞该评论

		respList = append(respList, *comment)
	}

	// hasMore：返回条数 < pageSize 代表没有更多数据
	hasMore := len(comments) >= req.PageSize

	return &dto.ListCommentsResponse{
		Comments: respList,
		HasMore:  hasMore,
		LastTime: nextLastTime,
		LastID:   nextLastID,
		Total:    total,
	}, nil
}
