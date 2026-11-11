package model

import (
	"fmt"
	"time"
)

// 通知状态：active 表示互动仍然有效，cancelled 表示对应的点赞/关注已被取消。
// 取消只更新状态、不删除记录，通知中心才能展示完整历史。
const (
	NotificationStatusActive    = "active"
	NotificationStatusCancelled = "cancelled"
)

type Notification struct {
	ID uint `gorm:"primaryKey;autoIncrement"`

	// 业务唯一键：ActorID + ReceiverID + Type + TargetID。
	// 这里只建普通索引：允许同一组合保留多条历史记录（cancelled），
	// 唯一性由下面的 ActiveKey 保证。
	ActorID    uint   `gorm:"not null;index:idx_notification_tuple,priority:1"`
	ReceiverID uint   `gorm:"not null;index:idx_notification_tuple,priority:2;index:idx_receiver_read_time,priority:1"`
	Type       string `gorm:"type:varchar(20);not null;index:idx_notification_tuple,priority:3"`
	TargetID   uint   `gorm:"not null;index:idx_notification_tuple,priority:4"`

	// ActiveKey 只在该通知有效（status=active）时写入 "actor:receiver:type:target"，
	// 被取消时置为 NULL。MySQL 唯一索引允许多个 NULL，
	// 因此同一业务键最多只能有一条 active 记录，而 cancelled 历史记录可以无限保留。
	// 这样即使 Redis 不可用、分布锁过期，数据库仍能拦住重复通知。
	// 该字段只用于内部去重，不对外暴露。
	ActiveKey *string `gorm:"type:varchar(128);uniqueIndex:uk_active" json:"-"`

	// Status 见 NotificationStatusActive / NotificationStatusCancelled
	Status string `gorm:"type:varchar(20);not null;default:active;index:idx_notification_status"`

	Content string `gorm:"type:text;not null"`

	// 复合索引：ReceiverID + IsRead + CreatedAt，用于快速查询某个用户的未读通知，并按时间排序
	IsRead    bool      `gorm:"not null;default:false;index:idx_receiver_read_time,priority:2"`
	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_receiver_read_time,priority:3"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// ActiveKeyFor 生成 active 状态下的业务唯一键。
func ActiveKeyFor(actorID, receiverID uint, typ string, targetID uint) string {
	return fmt.Sprintf("%d:%d:%s:%d", actorID, receiverID, typ, targetID)
}
