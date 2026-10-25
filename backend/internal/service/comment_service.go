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
	"strings"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
)

// maxCommentContentRunes 单条评论最大字符数，防止超长内容写入数据库。
const maxCommentContentRunes = 500

type CommentService struct {
	CommentRepo  *repo.CommentRepo
	CommentMQ    *mq.CommentMQ
	cache        *cache.RedisCache
	likeService  *LikeService  // 用于查询评论是否被点赞
	videoService *VideoService // 用于查询视频信息
}

func NewCommentService(commentRepo *repo.CommentRepo, commentMQ *mq.CommentMQ, cache *cache.RedisCache, likeService *LikeService, videoService *VideoService) *CommentService {
	return &CommentService{
		CommentRepo:  commentRepo,
		CommentMQ:    commentMQ,
		cache:        cache,
		likeService:  likeService,
		videoService: videoService,
	}
}

// PublishComment 发布评论：先做内容与目标校验，再把合法评论投递到 MQ。
// 非法评论在入口即被拒绝，不会进入 MQ 或数据库。
func (s *CommentService) PublishComment(ctx context.Context, vid, uid, pid uint, username, content string) error {
	if vid == 0 || uid == 0 {
		log.Printf("Invalid parameters: vid=%d, uid=%d", vid, uid)
		return errors.New("video ID and user ID must be non-zero")
	}

	// 去掉首尾空白后再校验和入库，避免纯空白评论
	content = strings.TrimSpace(content)
	if err := validateCommentContent(content); err != nil {
		log.Printf("Invalid comment content: vid=%d uid=%d err=%v", vid, uid, err)
		return err
	}
	// 校验视频存在性以及二级评论的父评论归属
	if err := s.validateCommentTarget(ctx, vid, pid); err != nil {
		log.Printf("Invalid comment target: vid=%d uid=%d pid=%d err=%v", vid, uid, pid, err)
		return err
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
		if createErr := s.CommentRepo.CreateComment(ctx, comment); createErr != nil {
			return fmt.Errorf("create comment fallback for video %d user %d: %w", vid, uid, createErr)
		}
	}

	// MQ投递成功（或降级写库成功），返回nil
	return nil
}

// validateCommentContent 校验评论内容：非空、长度受限，且只允许可打印字符与常见空白字符。
func validateCommentContent(content string) error {
	if content == "" {
		return errors.New("comment content must be non-empty")
	}
	if utf8.RuneCountInString(content) > maxCommentContentRunes {
		return fmt.Errorf("comment content must not exceed %d characters", maxCommentContentRunes)
	}
	for _, r := range content {
		// 允许换行、制表符及其他 Unicode 空白字符
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsSpace(r) {
			continue
		}
		// 控制字符、格式字符、私用区等不可打印字符一律拒绝
		if !unicode.IsPrint(r) {
			return errors.New("comment content contains invalid characters")
		}
	}
	return nil
}

// validateCommentTarget 校验评论目标：视频必须存在；二级评论的父评论必须属于同一个视频。
func (s *CommentService) validateCommentTarget(ctx context.Context, vid, pid uint) error {
	if _, err := s.videoService.VideoRepo.FindByID(ctx, vid); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("comment video %d not found", vid)
		}
		return fmt.Errorf("check comment video %d: %w", vid, err)
	}
	if pid == 0 {
		return nil
	}

	parent, err := s.CommentRepo.FindByID(ctx, pid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("parent comment %d not found", pid)
		}
		return fmt.Errorf("find parent comment %d: %w", pid, err)
	}
	if parent.VideoID != vid {
		return fmt.Errorf("parent comment %d does not belong to video %d", pid, vid)
	}
	return nil
}

func (s *CommentService) DeleteComment(ctx context.Context, uid, commentID uint) error {
	if uid == 0 || commentID == 0 {
		log.Printf("Invalid parameters: uid=%d, commentID=%d", uid, commentID)
		return errors.New("user ID and comment ID must be non-zero")
	}

	// 获取comment实例
	comment, err := s.CommentRepo.FindByID(ctx, commentID)
	if err != nil {
		log.Printf("Failed to find comment with ID %d: %v", commentID, err)
		return errors.New("comment not found")
	}

	// 检查用户是否有权限删除评论
	// 1. 评论发布者
	if comment.UserID != uid {
		// 获取视频信息
		video, err := s.videoService.getDetail(ctx, comment.VideoID)
		if err != nil {
			log.Printf("Failed to get video detail: %v", err)
			return errors.New("failed to get video detail")
		}
		// 2. 视频作者
		if video.AuthorID != uid {
			log.Printf("User %d is not authorized to delete comment %d", uid, commentID)
			return errors.New("user is not authorized to delete this comment")
		}
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
	return s.getCommentCount(ctx, vid, true)
}

// getCommentCount 评论计数内部实现；refreshTTL=false 时批量读取只读不续期，
// 避免 feed 一次请求对每个视频都产生一次 SET。
func (s *CommentService) getCommentCount(ctx context.Context, vid uint, refreshTTL bool) (int64, error) {
	key := cache.CommentVideoCntKey(vid)
	// 尝试读取redis
	val, err := s.cache.Get(ctx, key)
	if err == nil && val != "" {
		count, parseErr := strconv.ParseInt(val, 10, 64)
		if parseErr == nil {
			// 成功读取到redis计数器，按需刷新TTL，避免热视频缓存过期
			if refreshTTL {
				if err := s.cache.Set(ctx, key, val, cache.CommentVideoCntTTL); err != nil {
					log.Printf("Failed to refresh comment count TTL in cache for video %d: %v", vid, err)
				}
			}
			return count, nil
		}
		log.Printf("Invalid comment count cache value for video %d: %v", vid, parseErr)
	}

	// redis异常/key不存在/缓存脏数据，回数据库count兜底
	cnt, err := s.CommentRepo.CountByVideo(ctx, vid)
	if err != nil {
		log.Printf("Failed to count comments for video %d: %v", vid, err)
		return 0, fmt.Errorf("failed to count comments for video %d: %w", vid, err)
	}

	// 回填redis，冷视频自动失效，下次访问重新count
	err = s.cache.Set(ctx, key, strconv.FormatInt(cnt, 10), cache.CommentVideoCntTTL)
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

// BatchGetCommentCount 批量获取视频评论数量 map[vid]count
// 统一复用 getCommentCount：优先读 MQ 消费端回写的 comment:video:cnt:{vid}，
// 缓存未命中或 Redis 异常时回源数据库并回填，保证 feed 的 comments_cnt 不再恒为0。
func (s *CommentService) BatchGetCommentCount(ctx context.Context, vidList []string) (map[string]int64, error) {
	countMap := make(map[string]int64, len(vidList))
	for _, vid := range vidList {
		vidNum, err := strconv.ParseUint(vid, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid video id %q: %w", vid, err)
		}
		cnt, err := s.getCommentCount(ctx, uint(vidNum), false)
		if err != nil {
			return nil, fmt.Errorf("failed to get comment count for video %d: %w", vidNum, err)
		}
		countMap[vid] = cnt
	}
	return countMap, nil
}
