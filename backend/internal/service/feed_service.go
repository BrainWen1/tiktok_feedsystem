package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"feedsystem/internal/utils/logger"
	"fmt"
	"strconv"
	"time"
)

const defaultFeedLimit = 20

type FeedService struct {
	feedRepo       *repo.FeedRepo
	likeService    *LikeService
	socialService  *SocialService
	commentService *CommentService
	userService    *UserService // 新增，用来批量拿作者信息
}

type authorInfo struct {
	name   string
	avatar string
}

func NewFeedService(
	feedRepo *repo.FeedRepo,
	likeService *LikeService,
	socialService *SocialService,
	commentService *CommentService,
	userService *UserService,
) *FeedService {
	return &FeedService{
		feedRepo:       feedRepo,
		likeService:    likeService,
		socialService:  socialService,
		commentService: commentService,
		userService:    userService,
	}
}

func normalizeFeedLimit(limit int) int {
	if limit <= 0 || limit > defaultFeedLimit {
		return defaultFeedLimit
	}
	return limit
}

// parseFeedCursorVid 解析游标中的字符串主键；空游标返回 (0, false, nil)。
func parseFeedCursorVid(lastVid string) (uint, bool, error) {
	if lastVid == "" {
		return 0, false, nil
	}
	vid, err := strconv.ParseUint(lastVid, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid cursor last_vid %q: %w", lastVid, err)
	}
	if vid == 0 {
		return 0, false, fmt.Errorf("invalid cursor last_vid %q: must be greater than 0", lastVid)
	}
	return uint(vid), true, nil
}

// parseTimeFeedCursor 解析 (created_at, id) 复合游标。
// last_vid 与 last_created_at 必须同时存在或同时为空，避免半个游标导致重复/丢数据。
func parseTimeFeedCursor(cursor dto.FeedCursor) (time.Time, uint, bool, error) {
	vid, hasVid, err := parseFeedCursorVid(cursor.LastVid)
	if err != nil {
		return time.Time{}, 0, false, err
	}
	if !hasVid && cursor.LastCreatedAt.IsZero() {
		return time.Time{}, 0, false, nil
	}
	if !hasVid || cursor.LastCreatedAt.IsZero() {
		return time.Time{}, 0, false, fmt.Errorf("incomplete feed cursor: last_vid and last_created_at must be provided together")
	}
	return cursor.LastCreatedAt, vid, true, nil
}

// buildAuthorInfoMap 批量查询作者昵称与头像，返回 map[authorID]authorInfo。
// 一次 SQL 查全整页作者，替代逐作者 FindByID 的 N+1 查询；
// 作者信息属于附属数据，查询失败时整体降级为空作者字段，不阻断 feed 主流程。
func (s *FeedService) buildAuthorInfoMap(ctx context.Context, authorIDs []uint) map[uint]authorInfo {
	authorMap := make(map[uint]authorInfo, len(authorIDs))
	if s.userService == nil {
		logger.Ctx(ctx).Warn("user service unavailable, feed author info will be empty")
		return authorMap
	}
	if len(authorIDs) == 0 {
		return authorMap
	}

	profiles, err := s.userService.BatchGetProfiles(ctx, authorIDs)
	if err != nil {
		logger.Ctx(ctx).Warn("batch get feed author info failed, degrade to empty", logger.Err(err))
		return authorMap
	}

	for authorID, profile := range profiles {
		authorMap[authorID] = authorInfo{
			name:   profile.Username,
			avatar: profile.AvatarURL,
		}
	}

	return authorMap
}

// assembleFeedItems 【公共复用函数】原始model.Video切片 → 组装 []dto.FeedItemResp
// loginUserID = 0 代表游客，直接is_like=false is_follow=false，跳过批量查询
func (s *FeedService) assembleFeedItems(ctx context.Context, loginUserID uint, rawVideos []model.Video) ([]dto.FeedItemResp, error) {
	if len(rawVideos) == 0 {
		return []dto.FeedItemResp{}, nil
	}

	var vidList []string
	authorUserIDSet := make(map[uint]struct{})

	for _, v := range rawVideos {
		vidList = append(vidList, fmt.Sprintf("%d", v.ID))
		authorUserIDSet[v.AuthorID] = struct{}{}
	}

	authorUserIDList := make([]uint, 0, len(authorUserIDSet))
	for authorID := range authorUserIDSet {
		authorUserIDList = append(authorUserIDList, authorID)
	}
	authorInfoMap := s.buildAuthorInfoMap(ctx, authorUserIDList)

	var isLikeMap map[string]bool
	var isFollowMap map[uint]bool
	var likeCntMap map[string]int64
	var commentCntMap map[string]int64

	if loginUserID == 0 {
		// 游客：短路，不走批量查询
		isLikeMap = make(map[string]bool)
		isFollowMap = make(map[uint]bool)
	} else {
		// 登录用户：批量查询状态。
		// 点赞/关注状态属于附属信息，任一查询失败都降级为 false，不能阻断 feed 主流程。
		var err error
		isLikeMap, err = s.likeService.BatchGetIsLike(ctx, loginUserID, vidList)
		if err != nil {
			logger.Ctx(ctx).Warn("batch get like status failed, degrade to false", logger.Uint("user_id", loginUserID), logger.Err(err))
			isLikeMap = make(map[string]bool, len(vidList))
		}
		isFollowMap, err = s.socialService.BatchIsFollow(ctx, loginUserID, authorUserIDList)
		if err != nil {
			logger.Ctx(ctx).Warn("batch get follow status failed, degrade to false", logger.Uint("user_id", loginUserID), logger.Err(err))
			isFollowMap = make(map[uint]bool, len(authorUserIDList))
		}
	}
	// 计数，不管游客还是登录，都要拿计数展示。
	// Redis 计数异常时降级为写路径维护的 videos.likes_count，避免单条计数缓存抖动拖垮整个 feed。
	likeCntMap, likeCountErr := s.likeService.BatchGetLikeCount(ctx, vidList)
	if likeCountErr != nil {
		logger.Ctx(ctx).Warn("batch get like count failed, fallback to videos.likes_count", logger.Err(likeCountErr))
		likeCntMap = make(map[string]int64, len(rawVideos))
		for _, v := range rawVideos {
			likeCntMap[fmt.Sprintf("%d", v.ID)] = v.LikesCount
		}
	}
	commentCntMap, commentCountErr := s.commentService.BatchGetCommentCount(ctx, vidList)
	if commentCountErr != nil {
		// 评论数属于附属信息，查询失败时降级为 0，避免单个依赖异常让整个 feed 返回 500
		logger.Ctx(ctx).Warn("batch get comment count failed, degrade to 0", logger.Err(commentCountErr))
		commentCntMap = make(map[string]int64, len(vidList))
	}

	// 组装VO
	var respList []dto.FeedItemResp
	for _, v := range rawVideos {
		author := authorInfoMap[v.AuthorID]
		vidKey := fmt.Sprintf("%d", v.ID)
		likesCnt := likeCntMap[vidKey]
		// 计数缓存缺失（返回 0）但数据库维护列有值时，用数据库计数兜底
		if likesCnt == 0 && v.LikesCount > 0 {
			likesCnt = v.LikesCount
		}
		respList = append(respList, dto.FeedItemResp{
			Vid:            vidKey,
			Title:          v.Title,
			Desc:           v.Description,
			VideoURL:       v.VideoURL,
			CoverURL:       v.CoverURL,
			AuthorUserID:   v.AuthorID,
			AuthorName:     author.name,
			AvatarURL:      author.avatar,
			LikesCnt:       likesCnt,
			CommentsCnt:    commentCntMap[vidKey],
			Popularity:     v.Popularity,
			SortLikesCount: v.LikesCount,
			IsLike:         isLikeMap[vidKey],
			IsFollow:       isFollowMap[v.AuthorID],
			CreatedAt:      v.CreatedAt,
		})
	}
	return respList, nil
}

// buildVideoListResp 将原始model.Video切片，组装成dto.FeedListResp，包含next_cursor
func buildVideoListResp(videos []dto.FeedItemResp) dto.FeedListResp {
	resp := dto.FeedListResp{
		List: videos,
	}
	// 有数据才返回next_cursor；列表为空，next_cursor直接不返回（前端判定没有更多）
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		resp.NextCursor = &dto.FeedCursor{
			LastPopularity: last.Popularity,
			LastCreatedAt:  last.CreatedAt,
			// 必须使用数据库排序键 likes_count，而不是可能来自缓存的展示值 LikesCnt，
			// 否则缓存与数据库不一致时，下一页会跳数据或重复。
			LastLikesCount: last.SortLikesCount,
			LastVid:        last.Vid,
		}
	}
	return resp
}

// ---------- 5个业务方法骨架 ----------

func (s *FeedService) ListLatest(ctx context.Context, loginUserID uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	lastCreatedAt, lastVid, _, err := parseTimeFeedCursor(cursor)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	rawVideos, err := s.feedRepo.ListLatest(ctx, lastCreatedAt, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("list latest feeds: %w", err)
	}
	list, err := s.assembleFeedItems(ctx, loginUserID, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("assemble latest feed items: %w", err)
	}
	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByFollowing(ctx context.Context, loginUserID uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	lastCreatedAt, lastVid, _, err := parseTimeFeedCursor(cursor)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	// 直接按 follower_id JOIN social 表过滤关注关系，
	// 避免把全量关注 ID 拉进内存再拼进 IN 子句（关注量大时会撑爆 SQL 占位符与内存）。
	rawVideos, err := s.feedRepo.ListByFollowing(ctx, loginUserID, lastCreatedAt, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("list following feeds: %w", err)
	}

	list, err := s.assembleFeedItems(ctx, loginUserID, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("assemble following feed items: %w", err)
	}

	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByPopularity(ctx context.Context, loginUserID uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	lastVid, _, err := parseFeedCursorVid(cursor.LastVid)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	var lastPopularity int64
	if lastVid != 0 {
		lastPopularity = cursor.LastPopularity
	}

	rawVideos, err := s.feedRepo.ListByPopularity(ctx, lastPopularity, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("list popular feeds: %w", err)
	}

	list, err := s.assembleFeedItems(ctx, loginUserID, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("assemble popular feed items: %w", err)
	}

	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByLikesCount(ctx context.Context, loginUserID uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	lastVid, _, err := parseFeedCursorVid(cursor.LastVid)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	var lastLikesCount int64
	if lastVid != 0 {
		lastLikesCount = cursor.LastLikesCount
	}

	rawVideos, err := s.feedRepo.ListByLikesCount(ctx, lastLikesCount, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("list feeds by likes count: %w", err)
	}

	list, err := s.assembleFeedItems(ctx, loginUserID, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("assemble likes count feed items: %w", err)
	}

	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByTag(ctx context.Context, loginUserID uint, tag string, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	lastCreatedAt, lastVid, _, err := parseTimeFeedCursor(cursor)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	if tag == "" {
		return dto.FeedListResp{}, fmt.Errorf("feed by tag: empty tag")
	}

	rawVideos, err := s.feedRepo.ListByTag(ctx, tag, lastCreatedAt, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("list feeds by tag %q: %w", tag, err)
	}

	list, err := s.assembleFeedItems(ctx, loginUserID, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("assemble tag feed items for tag %q: %w", tag, err)
	}

	return buildVideoListResp(list), nil
}
