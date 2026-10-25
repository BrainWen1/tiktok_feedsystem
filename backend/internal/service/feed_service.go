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

func (s *FeedService) buildAuthorInfoMap(ctx context.Context, authorIDs []uint) map[uint]authorInfo {
	authorMap := make(map[uint]authorInfo)
	if s.userService == nil {
		logger.Warn("user service unavailable, feed author info will be empty")
		return authorMap
	}

	for _, authorID := range authorIDs {
		if _, exists := authorMap[authorID]; exists {
			continue
		}

		author, err := s.userService.FindByID(ctx, authorID)
		if err != nil {
			// 单个作者信息缺失不影响整条 feed 读取，降级为空作者字段
			logger.Warn("get feed author info failed", logger.Uint("author_id", authorID), logger.Err(err))
			continue
		}
		authorMap[authorID] = authorInfo{
			name:   author.Username,
			avatar: author.AvatarURL,
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
		// 登录用户：批量查询状态
		var err error
		isLikeMap, err = s.likeService.BatchGetIsLike(ctx, loginUserID, vidList)
		if err != nil {
			return nil, fmt.Errorf("batch get like status for user %d: %w", loginUserID, err)
		}
		isFollowMap, err = s.socialService.BatchIsFollow(ctx, loginUserID, authorUserIDList)
		if err != nil {
			return nil, fmt.Errorf("batch get follow status for user %d: %w", loginUserID, err)
		}
	}
	// 计数，不管游客还是登录，都要拿计数展示
	likeCntMap, err := s.likeService.BatchGetLikeCount(ctx, vidList)
	if err != nil {
		return nil, fmt.Errorf("batch get like count: %w", err)
	}
	commentCntMap, err = s.commentService.BatchGetCommentCount(ctx, vidList)
	if err != nil {
		return nil, fmt.Errorf("batch get comment count: %w", err)
	}

	// 组装VO
	var respList []dto.FeedItemResp
	for _, v := range rawVideos {
		author := authorInfoMap[v.AuthorID]
		respList = append(respList, dto.FeedItemResp{
			Vid:          fmt.Sprintf("%d", v.ID),
			Title:        v.Title,
			Desc:         v.Description,
			VideoURL:     v.VideoURL,
			CoverURL:     v.CoverURL,
			AuthorUserID: v.AuthorID,
			AuthorName:   author.name,
			AvatarURL:    author.avatar,
			LikesCnt:     likeCntMap[fmt.Sprintf("%d", v.ID)],
			CommentsCnt:  commentCntMap[fmt.Sprintf("%d", v.ID)],
			Popularity:   v.Popularity,
			IsLike:       isLikeMap[fmt.Sprintf("%d", v.ID)],
			IsFollow:     isFollowMap[v.AuthorID],
			CreatedAt:    v.CreatedAt,
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
			LastLikesCount: last.LikesCnt,
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

	followUserIDList, err := s.socialService.GetMyFollowUserIDList(ctx, loginUserID)
	if err != nil {
		return dto.FeedListResp{}, fmt.Errorf("get following user list for user %d: %w", loginUserID, err)
	}
	if len(followUserIDList) == 0 {
		return dto.FeedListResp{}, nil
	}

	rawVideos, err := s.feedRepo.ListByFollowing(ctx, followUserIDList, lastCreatedAt, lastVid, limit)
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
