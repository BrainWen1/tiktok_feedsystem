package service

import (
	"context"
	"feedsystem/internal/dto"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"
	"fmt"
)

type FeedService struct {
	feedRepo       *repo.FeedRepo
	likeService    *LikeService
	socialService  *SocialService
	commentService *CommentService
	userService    *UserService // 新增，用来批量拿作者信息
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

// assembleFeedItems 【公共复用函数】原始model.Video切片 → 组装 []dto.FeedItemResp
// loginUid = 0 代表游客，直接is_like=false is_follow=false，跳过批量查询
func (s *FeedService) assembleFeedItems(ctx context.Context, loginUid uint, rawVideos []model.Video) ([]dto.FeedItemResp, error) {
	if len(rawVideos) == 0 {
		return []dto.FeedItemResp{}, nil
	}

	var vidList []string
	var authorUidList []uint
	vidMap := make(map[string]model.Video)

	for _, v := range rawVideos {
		vidList = append(vidList, intToString(v.ID))
		authorUidList = append(authorUidList, v.AuthorID)
		vidMap[intToString(v.ID)] = v
	}

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
		respList = append(respList, dto.FeedItemResp{
			Vid:       intToString(v.ID),
			Title:     v.Title,
			Desc:      v.Description,
			VideoUrl:  v.VideoURL,
			CoverUrl:  v.CoverURL,
			AuthorUid: v.AuthorID,
			// 注意：AuthorName、AvatarUrl 需要你补充：从user信息map拿，这里你可以后续扩展批量查用户信息map
			AuthorName: "",
			AvatarUrl:  "",
			LikeCnt:    likeCntMap[intToString(v.ID)],
			CommentCnt: commentCntMap[intToString(v.ID)],
			IsLike:     isLikeMap[intToString(v.ID)],
			IsFollow:   isFollowMap[v.AuthorID],
		})
	}
	return respList, nil
}

// ---------- 5个业务方法骨架 ----------

func (s *FeedService) ListLatest(ctx context.Context, loginUid uint, lastVid string, limit int) ([]dto.FeedItemResp, error) {
	rawVideos, err := s.feedRepo.ListLatest(ctx, lastVid, limit)
	if err != nil {
		return nil, err
	}
	return s.assembleFeedItems(ctx, loginUid, rawVideos)
}

func (s *FeedService) ListByFollowing(ctx context.Context, loginUid uint, lastVid string, limit int) ([]dto.FeedItemResp, error) {
	followUidList, err := s.socialService.GetMyFollowUidList(ctx, loginUid)
	if err != nil {
		return nil, err
	}
	if len(followUidList) == 0 {
		return []dto.FeedItemResp{}, nil
	}
	rawVideos, err := s.feedRepo.ListByAuthorUids(ctx, followUidList, lastVid, limit)
	if err != nil {
		return nil, err
	}
	return s.assembleFeedItems(ctx, loginUid, rawVideos)
}

func (s *FeedService) ListByPopularity(ctx context.Context, loginUid uint, lastVid string, limit int) ([]dto.FeedItemResp, error) {
	rawVideos, err := s.feedRepo.ListByPopularity(ctx, lastVid, limit)
	if err != nil {
		return nil, err
	}
	return s.assembleFeedItems(ctx, loginUid, rawVideos)
}

func (s *FeedService) ListLikesCount(ctx context.Context, loginUid uint, lastVid string, limit int) ([]dto.FeedItemResp, error) {
	rawVideos, err := s.feedRepo.ListByLikeCount(ctx, lastVid, limit)
	if err != nil {
		return nil, err
	}
	return s.assembleFeedItems(ctx, loginUid, rawVideos)
}

func (s *FeedService) ListByTag(ctx context.Context, loginUid uint, tag string, lastVid string, limit int) ([]dto.FeedItemResp, error) {
	rawVideos, err := s.feedRepo.ListByTag(ctx, tag, lastVid, limit)
	if err != nil {
		return nil, err
	}
	return s.assembleFeedItems(ctx, loginUid, rawVideos)
}
