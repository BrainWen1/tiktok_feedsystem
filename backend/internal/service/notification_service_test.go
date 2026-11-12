package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"

	"github.com/alicebob/miniredis/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupNotificationStateMachine 组装通知状态机测试环境：
//   - SQLite 内存库替代 MySQL，用来验证 uk_active 唯一索引与 status 流转；
//   - miniredis 替代 Redis，用来验证分布式锁和未读数缓存。
func setupNotificationStateMachine(t *testing.T) (*NotificationService, *gorm.DB, *miniredis.Miniredis) {
	t.Helper()

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	// 单连接让 SQLite 写操作串行化，避免 "database is locked"
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.User{}, &model.Video{}, &model.Like{}, &model.Social{}, &model.Notification{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	svc := NewNotificationService(
		redisCache,
		repo.NewNotificationRepo(db),
		repo.NewVideoRepo(db),
		repo.NewCommentRepo(db),
		repo.NewLikeRepo(db),
		repo.NewSocialRepo(db),
	)
	return svc, db, mr
}

func mustCreateTestVideo(t *testing.T, db *gorm.DB, authorID uint) uint {
	t.Helper()
	video := &model.Video{AuthorID: authorID, Title: "t", VideoURL: "u", CoverURL: "c"}
	if err := db.Create(video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	return video.ID
}

// loadNotifications 按 id 升序返回全部通知，便于断言状态流转。
func loadNotifications(t *testing.T, db *gorm.DB) []model.Notification {
	t.Helper()
	var rows []model.Notification
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load notifications: %v", err)
	}
	return rows
}

// TestLikeNotificationStateMachine 验证点赞通知的完整闭环：
// 首次点赞 → 重复点赞被忽略 → 取消点赞置为 cancelled → 取消后再赞产生新通知。
func TestLikeNotificationStateMachine(t *testing.T) {
	ctx := context.Background()
	svc, db, mr := setupNotificationStateMachine(t)

	const actor, author = uint(1), uint(2)
	videoID := mustCreateTestVideo(t, db, author)
	likeRepo := repo.NewLikeRepo(db)

	likeEvent := mq.NotificationEvent{Action: "like", ActorID: actor, UserName: "alice", VideoID: videoID}
	unlikeEvent := mq.NotificationEvent{Action: "unlike", ActorID: actor, VideoID: videoID}

	// 1. 首次点赞：写入一条 active 通知，未读数缓存建立
	if err := likeRepo.CreateLike(ctx, actor, videoID); err != nil {
		t.Fatalf("create like: %v", err)
	}
	if err := svc.ProcessNotification(ctx, likeEvent); err != nil {
		t.Fatalf("process like notification: %v", err)
	}
	rows := loadNotifications(t, db)
	if len(rows) != 1 {
		t.Fatalf("want 1 notification after first like, got %d", len(rows))
	}
	if rows[0].Status != model.NotificationStatusActive || rows[0].ActiveKey == nil {
		t.Fatalf("want active notification with active_key, got status=%q active_key=%v", rows[0].Status, rows[0].ActiveKey)
	}
	if !mr.Exists(cache.UnreadCountKey(author)) {
		t.Fatalf("want unread count key created after notification")
	}

	// 2. 重复点赞（MQ 重投）：不新增记录，也不重复推送
	if err := svc.ProcessNotification(ctx, likeEvent); err != nil {
		t.Fatalf("process duplicate like notification: %v", err)
	}
	if rows = loadNotifications(t, db); len(rows) != 1 {
		t.Fatalf("duplicate like must not create a new notification, got %d rows", len(rows))
	}

	// 3. 取消点赞：同一行置为 cancelled + 已读，active_key 释放，未读缓存清理
	if _, err := likeRepo.DeleteLike(ctx, actor, videoID); err != nil {
		t.Fatalf("delete like: %v", err)
	}
	if err := svc.ProcessNotification(ctx, unlikeEvent); err != nil {
		t.Fatalf("process unlike notification: %v", err)
	}
	rows = loadNotifications(t, db)
	if len(rows) != 1 {
		t.Fatalf("cancel must keep the historical row, got %d rows", len(rows))
	}
	if rows[0].Status != model.NotificationStatusCancelled || rows[0].ActiveKey != nil || !rows[0].IsRead {
		t.Fatalf("want cancelled+read notification without active_key, got status=%q active_key=%v is_read=%v",
			rows[0].Status, rows[0].ActiveKey, rows[0].IsRead)
	}
	if mr.Exists(cache.UnreadCountKey(author)) {
		t.Fatalf("want unread count key removed after cancel")
	}

	// 4. 取消后重新点赞：插入一条全新的 active 记录，历史保留
	if err := likeRepo.CreateLike(ctx, actor, videoID); err != nil {
		t.Fatalf("re-create like: %v", err)
	}
	if err := svc.ProcessNotification(ctx, likeEvent); err != nil {
		t.Fatalf("process re-like notification: %v", err)
	}
	rows = loadNotifications(t, db)
	if len(rows) != 2 {
		t.Fatalf("re-like must create a new notification and keep history, got %d rows", len(rows))
	}
	if rows[0].Status != model.NotificationStatusCancelled || rows[1].Status != model.NotificationStatusActive {
		t.Fatalf("unexpected status order: [%q, %q]", rows[0].Status, rows[1].Status)
	}

	// 5. 重复取消：幂等，第二次取消不会改动已经 cancelled 的记录，也不会新增行
	if _, err := likeRepo.DeleteLike(ctx, actor, videoID); err != nil {
		t.Fatalf("delete like before repeated unlike: %v", err)
	}
	if err := svc.ProcessNotification(ctx, unlikeEvent); err != nil {
		t.Fatalf("process unlike: %v", err)
	}
	if err := svc.ProcessNotification(ctx, unlikeEvent); err != nil {
		t.Fatalf("process repeated unlike: %v", err)
	}
	rows = loadNotifications(t, db)
	if len(rows) != 2 || rows[1].Status != model.NotificationStatusCancelled {
		t.Fatalf("repeated unlike must be idempotent, got %+v", rows)
	}
}

// TestNotificationSkippedWhenInteractionGone 验证乱序场景：
// 取消失败或迟到时，给已经不存在的点赞补发通知会被拦截。
func TestNotificationSkippedWhenInteractionGone(t *testing.T) {
	ctx := context.Background()
	svc, db, _ := setupNotificationStateMachine(t)

	const actor, author = uint(1), uint(2)
	videoID := mustCreateTestVideo(t, db, author)

	// 数据库中没有点赞记录，直接处理点赞通知（等价于取消先到、点赞通知迟到）
	err := svc.ProcessNotification(ctx, mq.NotificationEvent{
		Action:   "like",
		ActorID:  actor,
		UserName: "alice",
		VideoID:  videoID,
	})
	if err != nil {
		t.Fatalf("process like notification: %v", err)
	}
	if rows := loadNotifications(t, db); len(rows) != 0 {
		t.Fatalf("notification must be skipped when like no longer exists, got %d rows", len(rows))
	}
}

// TestFollowNotificationStateMachine 验证关注通知同样支持取消后重新关注。
func TestFollowNotificationStateMachine(t *testing.T) {
	ctx := context.Background()
	svc, db, _ := setupNotificationStateMachine(t)

	const follower, blogger = uint(1), uint(2)
	socialRepo := repo.NewSocialRepo(db)

	followEvent := mq.NotificationEvent{Action: "follow", ActorID: follower, UserName: "alice", BloggerID: blogger}
	unfollowEvent := mq.NotificationEvent{Action: "unfollow", ActorID: follower, BloggerID: blogger}

	if _, err := socialRepo.CreateFollow(ctx, follower, blogger); err != nil {
		t.Fatalf("create follow: %v", err)
	}
	if err := svc.ProcessNotification(ctx, followEvent); err != nil {
		t.Fatalf("process follow notification: %v", err)
	}
	if rows := loadNotifications(t, db); len(rows) != 1 || rows[0].Status != model.NotificationStatusActive {
		t.Fatalf("want 1 active follow notification, got %+v", rows)
	}

	// 重复关注通知被忽略
	if err := svc.ProcessNotification(ctx, followEvent); err != nil {
		t.Fatalf("process duplicate follow notification: %v", err)
	}
	if rows := loadNotifications(t, db); len(rows) != 1 {
		t.Fatalf("duplicate follow must not create a new notification, got %d rows", len(rows))
	}

	if _, err := socialRepo.DeleteFollow(ctx, follower, blogger); err != nil {
		t.Fatalf("delete follow: %v", err)
	}
	if err := svc.ProcessNotification(ctx, unfollowEvent); err != nil {
		t.Fatalf("process unfollow notification: %v", err)
	}
	rows := loadNotifications(t, db)
	if len(rows) != 1 || rows[0].Status != model.NotificationStatusCancelled {
		t.Fatalf("want cancelled follow notification, got %+v", rows)
	}

	// 重新关注 → 新 active 记录
	if _, err := socialRepo.CreateFollow(ctx, follower, blogger); err != nil {
		t.Fatalf("re-create follow: %v", err)
	}
	if err := svc.ProcessNotification(ctx, followEvent); err != nil {
		t.Fatalf("process re-follow notification: %v", err)
	}
	rows = loadNotifications(t, db)
	if len(rows) != 2 || rows[1].Status != model.NotificationStatusActive {
		t.Fatalf("re-follow must create a new active notification, got %+v", rows)
	}
}
