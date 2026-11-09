package repo

import (
	"context"
	"feedsystem/internal/model"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// LikeRepo 点赞仓库，只负责点赞表的原始 SQL 读写。
type LikeRepo struct {
	db *gorm.DB
}

// NewLikeRepo 创建点赞仓库
func NewLikeRepo(db *gorm.DB) *LikeRepo {
	return &LikeRepo{db: db}
}

// CreateLike 创建点赞记录，并在同一事务内递增 videos.likes_count。
// 点赞行与计数同事务提交，保证视频表上的计数与点赞表一致；
// 唯一键冲突时整个事务回滚，不会重复累加计数。
func (r *LikeRepo) CreateLike(ctx context.Context, userID, vid uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		like := &model.Like{
			UserID:  userID,
			VideoID: vid,
		}
		if err := tx.Create(like).Error; err != nil {
			return err
		}
		// 使用 UpdateColumn 跳过 UpdatedAt，避免点赞动作频繁刷新视频元数据的更新时间
		return tx.Model(&model.Video{}).Where("id = ?", vid).
			UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error
	})
}

// DeleteLike 删除点赞记录并递减 videos.likes_count，返回实际删除的行数。
// 只有确实删除了点赞行才递减，且计数不会减为负数；
// 返回 0 表示该点赞已不存在（重复消息），调用方可据此按点赞表重算计数。
func (r *LikeRepo) DeleteLike(ctx context.Context, userID, vid uint) (int64, error) {
	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("user_id = ? AND video_id = ?", userID, vid).Delete(&model.Like{})
		if res.Error != nil {
			return res.Error
		}
		affected = res.RowsAffected // 记录实际删除的行数
		// 如果没有删除任何行，说明该点赞已不存在（重复消息）
		if affected == 0 {
			return nil
		}
		// 删除奏效了，递减计数，且计数不会减为负数
		return tx.Model(&model.Video{}).Where("id = ?", vid).
			UpdateColumn("likes_count", gorm.Expr("CASE WHEN likes_count > 0 THEN likes_count - 1 ELSE 0 END")).Error
	})
	return affected, err
}

// SyncLikesCount 以点赞表为准重算 videos.likes_count。
// 用于重复消费、首次落库后进程退出等导致计数漏记的场景做自愈。
// COUNT 与 UPDATE 合并为一条带子查询的原生 SQL，避免两步之间被并发点赞覆盖造成丢失更新（P0-4）。
func (r *LikeRepo) SyncLikesCount(ctx context.Context, vid uint) error {
	if vid == 0 {
		return fmt.Errorf("sync likes count: empty video id: %w", ErrInvalidArgument)
	}
	return r.db.WithContext(ctx).Exec(
		"UPDATE videos SET likes_count = (SELECT COUNT(*) FROM likes WHERE likes.video_id = ?) WHERE id = ?",
		vid, vid,
	).Error
}

// SyncPopularity 以点赞数为准刷新 videos.popularity，供热度榜按 (popularity, id) 排序。
// 点赞/取消点赞的计数事务提交后调用；用单条 UPDATE 读取最新 likes_count，
// 避免应用层先读后写被并发点赞覆盖。失败由调用方决定是否重试，不影响主流程。
func (r *LikeRepo) SyncPopularity(ctx context.Context, vid uint) error {
	if vid == 0 {
		return fmt.Errorf("sync popularity: empty video id: %w", ErrInvalidArgument)
	}
	return r.db.WithContext(ctx).Exec(
		"UPDATE videos SET popularity = likes_count WHERE id = ?",
		vid,
	).Error
}

// IsLiked 检查用户是否点赞了视频
func (r *LikeRepo) IsLiked(ctx context.Context, userID, vid uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Like{}).Where("user_id = ? AND video_id = ?", userID, vid).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// IsLikedBatch 批量查询用户对一组视频的点赞状态，避免逐条 IsLiked 造成 N+1 查询。
// 返回 map[vid]bool，未点赞的视频不会出现在结果中（调用方按 false 处理）。
func (r *LikeRepo) IsLikedBatch(ctx context.Context, userID uint, vids []uint) (map[uint]bool, error) {
	liked := make(map[uint]bool, len(vids))
	if userID == 0 || len(vids) == 0 {
		return liked, nil
	}
	var likedVids []uint
	if err := r.db.WithContext(ctx).Model(&model.Like{}).
		Where("user_id = ? AND video_id IN ?", userID, vids).
		Pluck("video_id", &likedVids).Error; err != nil {
		return nil, err
	}
	for _, vid := range likedVids {
		liked[vid] = true
	}
	return liked, nil
}

// CountByVideo 统计视频的点赞总数，供缓存禁用时降级查询
func (r *LikeRepo) CountByVideo(ctx context.Context, vid uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Like{}).Where("video_id = ?", vid).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// FindUserIDsByVideo 查询点赞该视频的全部用户ID，用于点赞明细 ZSet 过期后的缓存重建
func (r *LikeRepo) FindUserIDsByVideo(ctx context.Context, vid uint) ([]uint, error) {
	var userIDs []uint
	err := r.db.WithContext(ctx).Model(&model.Like{}).
		Where("video_id = ?", vid).
		Pluck("user_id", &userIDs).Error
	if err != nil {
		return nil, err
	}
	return userIDs, nil
}

// FindLikedVideoPage 按点赞时间 + 视频ID复合游标分页查询用户点赞记录。
// 返回完整 Like 记录，service 层据此同时拿到 video_id 和 created_at 生成下一页游标。
func (r *LikeRepo) FindLikedVideoPage(ctx context.Context, userID uint, lastCreatedAt time.Time, lastVid uint, pageSize int) ([]model.Like, int64, error) {
	var likes []model.Like
	var total int64

	if userID == 0 {
		return nil, 0, fmt.Errorf("find liked videos: empty user id: %w", ErrInvalidArgument)
	}

	if err := r.db.WithContext(ctx).Model(&model.Like{}).
		Where("user_id = ?", userID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q := r.db.WithContext(ctx).Model(&model.Like{}).Where("user_id = ?", userID)
	if lastVid != 0 {
		q = q.Where("(created_at, video_id) < (?, ?)", lastCreatedAt, lastVid)
	}
	if err := q.Order("created_at DESC, video_id DESC"). // 按点赞时间倒序，video_id 兜底保证顺序稳定
								Limit(pageSize).
								Find(&likes).Error; err != nil {
		return nil, 0, err
	}

	return likes, total, nil
}
