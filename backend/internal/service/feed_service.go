package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"fmt"
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

// intToString
func intToString(num uint) string {
	return fmt.Sprintf("%d", num)
}

func normalizeFeedLimit(limit int) int {
	if limit <= 0 || limit > defaultFeedLimit {
		return defaultFeedLimit
	}
	return limit
}

func (s *FeedService) buildAuthorInfoMap(ctx context.Context, authorIDs []uint) map[uint]authorInfo {
	authorMap := make(map[uint]authorInfo)
	if s.userService == nil {
		return authorMap
	}

	for _, authorID := range authorIDs {
		if _, exists := authorMap[authorID]; exists {
			continue
		}

		author, err := s.userService.FindByID(ctx, authorID)
		if err != nil {
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
// loginUid = 0 代表游客，直接is_like=false is_follow=false，跳过批量查询
func (s *FeedService) assembleFeedItems(ctx context.Context, loginUid uint, rawVideos []model.Video) ([]dto.FeedItemResp, error) {
	if len(rawVideos) == 0 {
		return []dto.FeedItemResp{}, nil
	}

	var vidList []string
	authorUidSet := make(map[uint]struct{})

	for _, v := range rawVideos {
		vidList = append(vidList, intToString(v.ID))
		authorUidSet[v.AuthorID] = struct{}{}
	}

	authorUidList := make([]uint, 0, len(authorUidSet))
	for authorID := range authorUidSet {
		authorUidList = append(authorUidList, authorID)
	}
	authorInfoMap := s.buildAuthorInfoMap(ctx, authorUidList)

	var isLikeMap map[string]bool
	var isFollowMap map[uint]bool
	var likeCntMap map[string]int64
	var commentCntMap map[string]int64

	if loginUid == 0 {
		// 游客：短路，不走批量查询
		isLikeMap = make(map[string]bool)
		isFollowMap = make(map[uint]bool)
	} else {
		// 登录用户：批量查询状态
		var err error
		isLikeMap, err = s.likeService.BatchGetIsLike(ctx, loginUid, vidList)
		if err != nil {
			return nil, err
		}
		isFollowMap, err = s.socialService.BatchIsFollow(ctx, loginUid, authorUidList)
		if err != nil {
			return nil, err
		}
	}
	// 计数，不管游客还是登录，都要拿计数展示
	likeCntMap, err := s.likeService.BatchGetLikeCount(ctx, vidList)
	if err != nil {
		return nil, err
	}
	commentCntMap, err = s.commentService.BatchGetCommentCount(ctx, vidList)
	if err != nil {
		return nil, err
	}

	// 组装VO
	var respList []dto.FeedItemResp
	for _, v := range rawVideos {
		author := authorInfoMap[v.AuthorID]
		respList = append(respList, dto.FeedItemResp{
			Vid:         intToString(v.ID),
			Title:       v.Title,
			Desc:        v.Description,
			VideoUrl:    v.VideoURL,
			CoverUrl:    v.CoverURL,
			AuthorUid:   v.AuthorID,
			AuthorName:  author.name,
			AvatarUrl:   author.avatar,
			LikesCnt:    likeCntMap[intToString(v.ID)],
			CommentsCnt: commentCntMap[intToString(v.ID)],
			Popularity:  v.Popularity,
			IsLike:      isLikeMap[intToString(v.ID)],
			IsFollow:    isFollowMap[v.AuthorID],
			CreatedAt:   v.CreatedAt,
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

func (s *FeedService) ListLatest(ctx context.Context, loginUid uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	var lastVid string
	var lastCreatedAt time.Time
	if cursor.LastVid != "" && !cursor.LastCreatedAt.IsZero() {
		lastVid = cursor.LastVid
		lastCreatedAt = cursor.LastCreatedAt
	}
	rawVideos, err := s.feedRepo.ListLatest(ctx, lastCreatedAt.Unix(), lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	list, err := s.assembleFeedItems(ctx, loginUid, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByFollowing(ctx context.Context, loginUid uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	var lastVid string
	var lastCreatedAt time.Time
	if cursor.LastVid != "" && !cursor.LastCreatedAt.IsZero() {
		lastVid = cursor.LastVid
		lastCreatedAt = cursor.LastCreatedAt
	}

	followUidList, err := s.socialService.GetMyFollowUidList(ctx, loginUid)
	if err != nil {
		return dto.FeedListResp{}, err
	}
	if len(followUidList) == 0 {
		return dto.FeedListResp{}, nil
	}

	rawVideos, err := s.feedRepo.ListByFollowing(ctx, followUidList, lastCreatedAt.Unix(), lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	list, err := s.assembleFeedItems(ctx, loginUid, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByPopularity(ctx context.Context, loginUid uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	var lastPopularity int64
	var lastVid string
	if cursor.LastVid != "" {
		lastPopularity = cursor.LastPopularity
		lastVid = cursor.LastVid
	}

	rawVideos, err := s.feedRepo.ListByPopularity(ctx, lastPopularity, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	list, err := s.assembleFeedItems(ctx, loginUid, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByLikesCount(ctx context.Context, loginUid uint, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	var lastVid string
	var lastLikesCount int64
	if cursor.LastVid != "" {
		lastVid = cursor.LastVid
		lastLikesCount = cursor.LastLikesCount
	}

	rawVideos, err := s.feedRepo.ListByLikesCount(ctx, lastLikesCount, lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	list, err := s.assembleFeedItems(ctx, loginUid, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	return buildVideoListResp(list), nil
}

func (s *FeedService) ListByTag(ctx context.Context, loginUid uint, tag string, cursor dto.FeedCursor, limit int) (dto.FeedListResp, error) {
	limit = normalizeFeedLimit(limit)
	var lastVid string
	var lastCreatedAt time.Time
	if cursor.LastVid != "" && !cursor.LastCreatedAt.IsZero() {
		lastVid = cursor.LastVid
		lastCreatedAt = cursor.LastCreatedAt
	}

	rawVideos, err := s.feedRepo.ListByTag(ctx, tag, lastCreatedAt.Unix(), lastVid, limit)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	list, err := s.assembleFeedItems(ctx, loginUid, rawVideos)
	if err != nil {
		return dto.FeedListResp{}, err
	}

	return buildVideoListResp(list), nil
}
