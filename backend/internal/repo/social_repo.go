package repo

import (
	"context"
	"feedsystem/internal/model"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SocialRepo 关注关系仓库，只负责 social 表的原始 SQL 读写。
type SocialRepo struct {
	db *gorm.DB
}

// NewSocialRepo 创建关注关系仓库
func NewSocialRepo(db *gorm.DB) *SocialRepo {
	return &SocialRepo{db: db}
}

// CreateFollow 创建关注关系，返回是否真的插入了新记录。
// 依赖 (follower_id, blogger_id) 唯一索引 + ON CONFLICT DO NOTHING 保证重复关注幂等：
// 并发或重复请求不会报唯一键冲突，只有第一个请求 created=true。
func (r *SocialRepo) CreateFollow(ctx context.Context, followerID, bloggerID uint) (created bool, err error) {
	social := &model.Social{
		FollowerID: followerID,
		BloggerID:  bloggerID,
	}
	tx := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(social)
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil
}

// DeleteFollow 删除关注关系，并返回是否真的删除到记录。
func (r *SocialRepo) DeleteFollow(ctx context.Context, followerID, bloggerID uint) (bool, error) {
	tx := r.db.WithContext(ctx).
		Where("follower_id = ? AND blogger_id = ?", followerID, bloggerID).
		Delete(&model.Social{})
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil // 返回是否真的删除到记录
}

// IsFollowing 检查用户是否已经关注了某个博主。
func (r *SocialRepo) IsFollowing(ctx context.Context, followerID, bloggerID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.Social{}).
		Where("follower_id = ? AND blogger_id = ?", followerID, bloggerID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// BatchIsFollowing 批量查询 followerID 是否关注了 bloggerIDs 中的每个用户。
// 一次 IN 查询替代列表场景逐条 IsFollowing，避免 N+1。
func (r *SocialRepo) BatchIsFollowing(ctx context.Context, followerID uint, bloggerIDs []uint) (map[uint]bool, error) {
	result := make(map[uint]bool, len(bloggerIDs))
	if followerID == 0 || len(bloggerIDs) == 0 {
		return result, nil
	}

	var rows []struct{ BloggerID uint }
	if err := r.db.WithContext(ctx).
		Model(&model.Social{}).
		Select("blogger_id").
		Where("follower_id = ? AND blogger_id IN ?", followerID, bloggerIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.BloggerID] = true
	}
	return result, nil
}

// BatchIsFollowedBy 批量查询 followerIDs 中的用户是否关注了 bloggerID。
// 用于“查看自己的粉丝列表时判断回关”的场景，同样一次 IN 查询。
func (r *SocialRepo) BatchIsFollowedBy(ctx context.Context, bloggerID uint, followerIDs []uint) (map[uint]bool, error) {
	result := make(map[uint]bool, len(followerIDs))
	if bloggerID == 0 || len(followerIDs) == 0 {
		return result, nil
	}

	var rows []struct{ FollowerID uint }
	if err := r.db.WithContext(ctx).
		Model(&model.Social{}).
		Select("follower_id").
		Where("blogger_id = ? AND follower_id IN ?", bloggerID, followerIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.FollowerID] = true
	}
	return result, nil
}

// ListBloggers 获取用户的关注列表，按复合游标分页。
// orderByFollowTime=true 时按 (created_at DESC, blogger_id DESC) 排序，游标为上一页最后一条关注记录；
// false 时按 blogger_id ASC 排序，游标为上一页最后一个博主ID。
func (r *SocialRepo) ListBloggers(ctx context.Context, followerID uint, lastCreatedAt time.Time, lastUserID uint, pageSize int, orderByFollowTime bool) ([]*model.Social, int64, error) {
	if followerID == 0 {
		return nil, 0, fmt.Errorf("list bloggers: empty follower id: %w", ErrInvalidArgument)
	}

	var socials []*model.Social
	var total int64

	// 获取总数
	if err := r.db.WithContext(ctx).Model(&model.Social{}).
		Where("follower_id = ?", followerID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := r.db.WithContext(ctx).Model(&model.Social{}).Where("follower_id = ?", followerID)
	if orderByFollowTime {
		if lastUserID != 0 {
			// 与缓存 ZSet 对齐：score 精确到秒。先取更早的秒，
			// 同一秒内再按 blogger_id 倒序续页。
			cursorStart := lastCreatedAt.Truncate(time.Second)
			cursorEnd := cursorStart.Add(time.Second)
			query = query.Where(
				"(created_at < ? OR (created_at >= ? AND created_at < ? AND blogger_id < ?))",
				cursorStart, cursorStart, cursorEnd, lastUserID,
			)
		}
		query = query.Order("created_at DESC, blogger_id DESC")
	} else {
		if lastUserID != 0 {
			// blogger_id 在同一 follower 下唯一，单独作为游标即可保证不重不漏
			query = query.Where("blogger_id > ?", lastUserID)
		}
		query = query.Order("blogger_id ASC")
	}

	if err := query.Limit(pageSize).Find(&socials).Error; err != nil {
		return nil, 0, err
	}

	return socials, total, nil
}

// ListAllBloggers 获取用户的全部关注列表，按关注时间排序。
func (r *SocialRepo) ListAllBloggers(ctx context.Context, followerID uint, orderByFollowTime bool) ([]*model.Social, error) {
	if followerID == 0 {
		return nil, fmt.Errorf("list all bloggers: empty follower id: %w", ErrInvalidArgument)
	}

	var socials []*model.Social
	query := r.db.WithContext(ctx).Model(&model.Social{}).Where("follower_id = ?", followerID)

	if orderByFollowTime {
		query = query.Order("created_at DESC, blogger_id DESC")
	} else {
		query = query.Order("blogger_id ASC")
	}

	if err := query.Find(&socials).Error; err != nil {
		return nil, err
	}

	return socials, nil
}

// FindByFollowerAndBlogger 查找特定关注关系。
// 使用显式 WHERE 条件而不是结构体条件，避免 ID 为 0 时 GORM 忽略零值生成空 WHERE 查回第一条记录。
func (r *SocialRepo) FindByFollowerAndBlogger(ctx context.Context, followerID, bloggerID uint) (*model.Social, error) {
	if followerID == 0 || bloggerID == 0 {
		return nil, fmt.Errorf("find follow relation: follower_id=%d blogger_id=%d: %w", followerID, bloggerID, ErrInvalidArgument)
	}

	var social model.Social
	err := r.db.WithContext(ctx).
		Where("follower_id = ? AND blogger_id = ?", followerID, bloggerID).
		First(&social).Error
	if err != nil {
		return nil, err
	}
	return &social, nil
}

// ListFollowers 获取用户粉丝列表，按复合游标分页。
func (r *SocialRepo) ListFollowers(ctx context.Context, bloggerID uint, lastCreatedAt time.Time, lastUserID uint, pageSize int, orderByFollowTime bool) ([]*model.Social, int64, error) {
	if bloggerID == 0 {
		return nil, 0, fmt.Errorf("list followers: empty blogger id: %w", ErrInvalidArgument)
	}

	var socials []*model.Social
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.Social{}).
		Where("blogger_id = ?", bloggerID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := r.db.WithContext(ctx).Model(&model.Social{}).Where("blogger_id = ?", bloggerID)
	if orderByFollowTime {
		if lastUserID != 0 {
			// 与缓存 ZSet 对齐：score 精确到秒。先取更早的秒，
			// 同一秒内再按 follower_id 倒序续页。
			cursorStart := lastCreatedAt.Truncate(time.Second)
			cursorEnd := cursorStart.Add(time.Second)
			query = query.Where(
				"(created_at < ? OR (created_at >= ? AND created_at < ? AND follower_id < ?))",
				cursorStart, cursorStart, cursorEnd, lastUserID,
			)
		}
		query = query.Order("created_at DESC, follower_id DESC")
	} else {
		if lastUserID != 0 {
			// follower_id 在同一 blogger 下唯一，单独作为游标即可保证不重不漏
			query = query.Where("follower_id > ?", lastUserID)
		}
		query = query.Order("follower_id ASC")
	}

	if err := query.Limit(pageSize).Find(&socials).Error; err != nil {
		return nil, 0, err
	}
	return socials, total, nil
}

// ListAllFollowers 获取用户的全部粉丝列表，按关注时间排序。
func (r *SocialRepo) ListAllFollowers(ctx context.Context, bloggerID uint, orderByFollowTime bool) ([]*model.Social, error) {
	if bloggerID == 0 {
		return nil, fmt.Errorf("list all followers: empty blogger id: %w", ErrInvalidArgument)
	}

	var socials []*model.Social
	query := r.db.WithContext(ctx).Model(&model.Social{}).Where("blogger_id = ?", bloggerID)

	if orderByFollowTime {
		query = query.Order("created_at DESC, follower_id DESC")
	} else {
		query = query.Order("follower_id ASC")
	}

	if err := query.Find(&socials).Error; err != nil {
		return nil, err
	}

	return socials, nil
}

// GetCounts 获取用户的关注和粉丝数量
func (r *SocialRepo) GetCounts(ctx context.Context, userID uint) (followCount int64, fanCount int64, err error) {
	if userID == 0 {
		return 0, 0, fmt.Errorf("get social counts: empty user id: %w", ErrInvalidArgument)
	}

	// 获取关注数量
	if err = r.db.WithContext(ctx).Model(&model.Social{}).Where("follower_id = ?", userID).Count(&followCount).Error; err != nil {
		return
	}
	// 获取粉丝数量
	if err = r.db.WithContext(ctx).Model(&model.Social{}).Where("blogger_id = ?", userID).Count(&fanCount).Error; err != nil {
		return
	}
	return followCount, fanCount, nil
}

// QueryMyFollowList 【纯DB】查询 userID 的所有关注记录，返回完整social记录（含CreatedAt）
func (r *SocialRepo) QueryMyFollowList(ctx context.Context, userID uint) ([]model.Social, error) {
	if userID == 0 {
		return nil, fmt.Errorf("query my follow list: empty user id: %w", ErrInvalidArgument)
	}

	var list []model.Social
	err := r.db.WithContext(ctx).
		Where("follower_id = ?", userID).
		Order("created_at DESC").
		Find(&list).Error
	return list, err
}
