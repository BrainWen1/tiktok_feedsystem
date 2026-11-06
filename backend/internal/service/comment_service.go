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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
)

// maxCommentContentRunes 单条评论最大字符数，防止超长内容写入数据库。
const maxCommentContentRunes = 500

type CommentService struct {
	CommentRepo         *repo.CommentRepo
	CommentMQ           *mq.CommentMQ
	cache               *cache.RedisCache
	likeService         *LikeService         // 用于查询评论是否被点赞
	videoService        *VideoService        // 用于查询视频信息
	notificationService *NotificationService // 用于处理评论通知
}

func NewCommentService(commentRepo *repo.CommentRepo, commentMQ *mq.CommentMQ, cache *cache.RedisCache, likeService *LikeService, videoService *VideoService, notificationService *NotificationService) *CommentService {
	return &CommentService{
		CommentRepo:         commentRepo,
		CommentMQ:           commentMQ,
		cache:               cache,
		likeService:         likeService,
		videoService:        videoService,
		notificationService: notificationService,
	}
}

// PublishComment 发布评论：先做内容与目标校验，再把合法评论投递到 MQ。
// 非法评论在入口即被拒绝，不会进入 MQ 或数据库。
func (s *CommentService) PublishComment(ctx context.Context, vid, userID, pid uint, username, content string) error {
	if vid == 0 || userID == 0 {
		logger.Ctx(ctx).Warn("invalid parameters", logger.Uint("video_id", vid), logger.Uint("user_id", userID))
		return errors.New("video ID and user ID must be non-zero")
	}

	// 去掉首尾空白后再校验和入库，避免纯空白评论
	content = strings.TrimSpace(content)
	if err := validateCommentContent(content); err != nil {
		logger.Ctx(ctx).Warn("invalid comment content", logger.Uint("video_id", vid), logger.Uint("user_id", userID), logger.Err(err))
		return err
	}
	// 校验视频存在性以及二级评论的父评论归属
	if err := s.validateCommentTarget(ctx, vid, pid); err != nil {
		logger.Ctx(ctx).Warn("invalid comment target", logger.Uint("video_id", vid), logger.Uint("user_id", userID), logger.Uint("parent_id", pid), logger.Err(err))
		return err
	}

	//组装消息
	Comment := &model.Comment{
		VideoID:  vid,
		UserID:   userID,
		ParentID: pid,
		UserName: username,
		Content:  content,
	}
	err := s.CommentMQ.PublishComment(ctx, Comment)
	if err != nil {
		logger.Ctx(ctx).Error("publish comment event to MQ failed", logger.Err(err))
		// MQ投递失败，降级方案：同步直接写数据库
		if createErr := s.CommentRepo.CreateComment(ctx, Comment); createErr != nil {
			return fmt.Errorf("create comment fallback for video %d user %d: %w", vid, userID, createErr)
		}
		// 同步写数据库成功后，异步处理通知逻辑
		go func() {
			nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			// err 使用 := 创建局部变量，避免覆盖外层的 err，防止以后可能发生的竞态
			err := s.notificationService.ProcessNotification(nctx, mq.NotificationEvent{
				// 目前 eventID 先传空串，后续如果在函数内部加入重试机制，则需要使用 eventID 做幂等
				// 这里不使用 uuid.NewString()，因为在函数内部也不会使用，会产生垃圾
				EventID:   "",
				Action:    "comment",
				ActorID:   Comment.UserID,
				UserName:  Comment.UserName,
				VideoID:   Comment.VideoID,
				CommentID: Comment.ID, // GORM 会在插入后自动把主键回填到传入的 value 的 id 字段中
				ParentID:  Comment.ParentID,
				Content:   Comment.Content,
			})
			if err != nil {
				logger.Ctx(nctx).Error("process notification failed", logger.Err(err))
			}
		}()
		return nil
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

func (s *CommentService) DeleteComment(ctx context.Context, userID, commentID uint) error {
	if userID == 0 || commentID == 0 {
		logger.Ctx(ctx).Warn("invalid parameters", logger.Uint("user_id", userID), logger.Uint("comment_id", commentID))
		return errors.New("user ID and comment ID must be non-zero")
	}

	// 获取comment实例
	comment, err := s.CommentRepo.FindByID(ctx, commentID)
	if err != nil {
		logger.Ctx(ctx).Error("find comment failed", logger.Uint("comment_id", commentID), logger.Err(err))
		return fmt.Errorf("find comment %d: %w", commentID, err)
	}

	// 检查用户是否有权限删除评论
	// 1. 评论发布者
	if comment.UserID != userID {
		// 获取视频信息
		video, err := s.videoService.getDetail(ctx, comment.VideoID)
		if err != nil {
			logger.Ctx(ctx).Error("get video detail failed", logger.Uint("video_id", comment.VideoID), logger.Err(err))
			return fmt.Errorf("get video %d detail for comment %d: %w", comment.VideoID, commentID, err)
		}
		// 2. 视频作者
		if video.AuthorID != userID {
			logger.Ctx(ctx).Warn("user is not authorized to delete comment", logger.Uint("user_id", userID), logger.Uint("comment_id", commentID))
			return errors.New("user is not authorized to delete this comment")
		}
	}

	//组装消息
	err = s.CommentMQ.DeleteComment(ctx, comment)
	if err != nil {
		logger.Ctx(ctx).Error("publish comment event to MQ failed", logger.Err(err))
		// MQ投递失败，降级方案：同步直接写数据库
		if deleteErr := s.CommentRepo.DeleteComment(ctx, commentID); deleteErr != nil {
			return fmt.Errorf("delete comment %d fallback: %w", commentID, deleteErr)
		}
		return nil
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
	// 尝试读取redis；未命中属于正常路径，其余异常记录日志后回源数据库
	val, err := s.cache.Get(ctx, key)
	if err != nil && !errors.Is(err, cache.ErrCacheMiss) {
		logger.Ctx(ctx).Warn("get comment count cache failed", logger.Uint("video_id", vid), logger.Err(err))
	}
	if err == nil && val != "" {
		count, parseErr := strconv.ParseInt(val, 10, 64)
		if parseErr == nil {
			// 成功读取到redis计数器，按需刷新TTL，避免热视频缓存过期
			if refreshTTL {
				if err := s.cache.Set(ctx, key, val, cache.CommentVideoCntTTL); err != nil {
					logger.Ctx(ctx).Error("refresh comment count TTL failed", logger.Uint("video_id", vid), logger.Err(err))
				}
			}
			return count, nil
		}
		logger.Ctx(ctx).Warn("invalid comment count cache value", logger.Uint("video_id", vid), logger.Err(parseErr))
	}

	// redis异常/key不存在/缓存脏数据，回数据库count兜底
	cnt, err := s.CommentRepo.CountByVideo(ctx, vid)
	if err != nil {
		logger.Ctx(ctx).Error("count comments failed", logger.Uint("video_id", vid), logger.Err(err))
		return 0, fmt.Errorf("failed to count comments for video %d: %w", vid, err)
	}

	// 回填redis，冷视频自动失效，下次访问重新count
	err = s.cache.Set(ctx, key, strconv.FormatInt(cnt, 10), cache.CommentVideoCntTTL)
	if err != nil {
		logger.Ctx(ctx).Error("set comment count cache failed", logger.Uint("video_id", vid), logger.Err(err))
	}
	return cnt, nil
}

// ListComment 游标分页查询评论列表
func (s *CommentService) ListComments(ctx context.Context, userID uint, req dto.ListCommentsRequest) (*dto.ListCommentsResponse, error) {
	// 游标分页查询评论
	comments, err := s.CommentRepo.ListByVideoCursor(ctx, req.VideoID, req.LastTime, req.LastID, req.PageSize)
	if err != nil {
		return nil, fmt.Errorf("list comments for video %d: %w", req.VideoID, err)
	}

	// 获取评论总数
	total, err := s.GetCommentCount(ctx, req.VideoID)
	if err != nil {
		return nil, fmt.Errorf("get comment count for video %d: %w", req.VideoID, err)
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
		// 鸽：使用传入的userID查询是否点赞该评论

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
