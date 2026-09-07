package service

import (
	"context"
	"errors"
	"feedsystem/internal/dto"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/repo"
	"log"
)

// VideoDetailer 只暴露 LikeService 需要的最小视频详情能力。
// 这里不要直接依赖完整的 VideoService，避免和 VideoService 形成初始化循环。
type VideoDetailer interface {
	VideoDetail(ctx context.Context, videoID uint, uid uint) (*dto.VideoDetailResponse, error)
}

type LikeService struct {
	likeRepo      *repo.LikeRepo
	likeMQ        *mq.LikeMQ
	UserService   *UserService // 引入UserService以便查询作者信息
	videoDetailer VideoDetailer
}

func NewLikeService(likeRepo *repo.LikeRepo, likeMQ *mq.LikeMQ, userService *UserService) *LikeService {
	return &LikeService{likeRepo: likeRepo, likeMQ: likeMQ, UserService: userService}
}

// SetVideoDetailer 在 VideoService 创建完成后注入。
// 这一步把双向依赖拆成“先创建 LikeService，再把 VideoService 作为接口塞回去”。
func (s *LikeService) SetVideoDetailer(videoDetailer VideoDetailer) {
	s.videoDetailer = videoDetailer
}

// LikeVideo 点赞视频
func (s *LikeService) LikeVideo(ctx context.Context, uid, vid uint) error {
	if uid == 0 || vid == 0 {
		log.Printf("Invalid uid or vid: uid=%d, vid=%d", uid, vid)
		return errors.New("uid and vid must be non-zero")
	}
	// 前置查询：拦截重复点击，减少无效MQ消息
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	if err != nil {
		return err
	}
	if isLiked {
		return errors.New("video already liked by user")
	}

	//组装消息
	err = s.likeMQ.Like(ctx, uid, vid)
	if err != nil {
		log.Printf("Failed to publish like event to MQ: %v", err)
		// MQ投递失败，降级方案：同步直接写数据库
		err = s.likeRepo.CreateLike(ctx, uid, vid)
		return err
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// UnlikeVideo 取消点赞视频
func (s *LikeService) UnlikeVideo(ctx context.Context, uid, vid uint) error {
	if uid == 0 || vid == 0 {
		log.Printf("Invalid uid or vid: uid=%d, vid=%d", uid, vid)
		return errors.New("uid and vid must be non-zero")
	}
	// 前置查询：拦截重复点击，减少无效MQ消息
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	if err != nil {
		return err
	}
	if !isLiked {
		return errors.New("video not liked by user")
	}

	//组装消息
	err = s.likeMQ.Unlike(ctx, uid, vid)
	if err != nil {
		log.Printf("Failed to publish unlike event to MQ: %v", err)
		// MQ投递失败，降级方案：同步直接写数据库
		err = s.likeRepo.DeleteLike(ctx, uid, vid)
		return err
	}

	// MQ投递成功，返回nil，后台慢慢消费入库
	return nil
}

// IsLiked 检查用户是否点赞了视频
func (s *LikeService) IsLiked(ctx context.Context, uid, vid uint) (dto.IsLikedResponse, error) {
	isLiked, err := s.likeRepo.IsLiked(ctx, uid, vid)
	return dto.IsLikedResponse{IsLiked: isLiked}, err
}

// ListLikedVideos 列出用户点赞过的视频
func (s *LikeService) ListLikedVideos(ctx context.Context, targetUid uint, visitorUid, pageNum, pageSize uint) (*dto.VideoListResponse, error) {
	if s.videoDetailer == nil {
		return nil, errors.New("video detail service is not initialized")
	}

	// 根据targetUid查询用户点赞过的vid列表
	vids, total, err := s.likeRepo.FindVidsByUserID(ctx, targetUid, pageNum, pageSize)
	if err != nil {
		return nil, err
	}

	// 根据vids查询视频详情，并组装返回结果
	resList := make([]dto.VideoDetailResponse, 0, len(vids)) // 预分配容量，避免多次扩容
	for _, vid := range vids {
		videoDetail, err := s.videoDetailer.VideoDetail(ctx, vid, visitorUid)
		if err != nil {
			log.Printf("get video detail fail vid=%d", vid)
			continue
		}
		// dto.VideoListResponse 里保存的是值类型，所以这里要解引用后再放入切片。
		resList = append(resList, *videoDetail)
	}

	return &dto.VideoListResponse{
		Videos: resList,
		Total:  total,
	}, nil
}
