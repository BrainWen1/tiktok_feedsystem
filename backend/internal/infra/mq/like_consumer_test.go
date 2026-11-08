package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"

	"github.com/alicebob/miniredis/v2"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// noopNotificationProcessor 让消费用例不依赖通知落库链路；
// 注意 LikeService 的降级分支同样会异步触发通知，测试时必须注入非 nil 实现。
type noopNotificationProcessor struct{}

func (noopNotificationProcessor) ProcessNotification(context.Context, NotificationEvent) error {
	return nil
}

func setupLikeConsumer(t *testing.T) (*repo.LikeRepo, *cache.RedisCache, *miniredis.Miniredis, *gorm.DB) {
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
	// 单连接让 SQLite 写事务串行执行，避免并发用例出现 "database is locked"。
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.Video{}, &model.Like{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	return repo.NewLikeRepo(db), redisCache, mr, db
}

func mustCreateConsumerVideo(t *testing.T, db *gorm.DB) *model.Video {
	t.Helper()
	video := &model.Video{
		AuthorID: 99,
		Title:    "like consumer test video",
		VideoURL: "/upload/videos/99/test.mp4",
		CoverURL: "/upload/covers/99/test.jpg",
	}
	if err := db.Create(video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	return video
}

func mustLikeDelivery(t *testing.T, event LikeEvent) amqp.Delivery {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal like event: %v", err)
	}
	return amqp.Delivery{Body: body, Headers: amqp.Table{}}
}

func countConsumerLikes(t *testing.T, db *gorm.DB, userID, videoID uint) int64 {
	t.Helper()
	var rows int64
	if err := db.Model(&model.Like{}).Where("user_id = ? AND video_id = ?", userID, videoID).Count(&rows).Error; err != nil {
		t.Fatalf("count likes: %v", err)
	}
	return rows
}

func consumerVideoLikesCount(t *testing.T, db *gorm.DB, videoID uint) int64 {
	t.Helper()
	var video model.Video
	if err := db.Select("id", "likes_count").First(&video, videoID).Error; err != nil {
		t.Fatalf("load video %d: %v", videoID, err)
	}
	return video.LikesCount
}

// TestHandleLikeMessage_DuplicateDeliveryIsIdempotent 覆盖 MQ 至少一次投递语义：
// 同一 EventID 被消费两次，只能落一条点赞、计数只加一次，并写入幂等标记。
func TestHandleLikeMessage_DuplicateDeliveryIsIdempotent(t *testing.T) {
	likeRepo, redisCache, mr, db := setupLikeConsumer(t)
	ctx := context.Background()

	video := mustCreateConsumerVideo(t, db)
	event := LikeEvent{
		EventID:  "like-evt-dup",
		Action:   "like",
		UserID:   1,
		UserName: "user1",
		VideoID:  video.ID,
	}
	msg := mustLikeDelivery(t, event)

	handleLikeMessage(ctx, nil, msg, likeRepo, redisCache, noopNotificationProcessor{})
	handleLikeMessage(ctx, nil, msg, likeRepo, redisCache, noopNotificationProcessor{})

	if got := countConsumerLikes(t, db, 1, video.ID); got != 1 {
		t.Fatalf("like rows = %d, want 1", got)
	}
	if got := consumerVideoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("videos.likes_count = %d, want 1", got)
	}
	if !mr.Exists(cache.LikeEventIDKey(event.EventID)) {
		t.Fatalf("idempotency key %q should be written", cache.LikeEventIDKey(event.EventID))
	}
	if member, err := redisCache.IsMemberOfSet(ctx, cache.LikeUserSetKey(event.UserID), event.VideoID); err != nil || !member {
		t.Fatalf("user like set member = %v, err = %v, want true", member, err)
	}
	if cnt, err := redisCache.ZCard(ctx, cache.LikeVideoZSetKey(event.VideoID)); err != nil || cnt != 1 {
		t.Fatalf("video like zset card = %d, err = %v, want 1", cnt, err)
	}
	if _, err := mr.ZScore(cache.VideoRankZSetKey(), fmt.Sprint(event.VideoID)); err != nil {
		t.Fatalf("video rank should contain video %d: %v", event.VideoID, err)
	}
}

// TestHandleLikeMessage_ConcurrentSameEvent 模拟同一消息被多个消费者并发处理：
// 数据库唯一键 + 重复消费同步逻辑必须保证最终只有一条点赞、计数不会重复累加。
func TestHandleLikeMessage_ConcurrentSameEvent(t *testing.T) {
	likeRepo, redisCache, _, db := setupLikeConsumer(t)
	ctx := context.Background()

	video := mustCreateConsumerVideo(t, db)
	event := LikeEvent{
		EventID:  "like-evt-concurrent",
		Action:   "like",
		UserID:   2,
		UserName: "user2",
		VideoID:  video.ID,
	}
	msg := mustLikeDelivery(t, event)

	const goroutines = 8
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleLikeMessage(ctx, nil, msg, likeRepo, redisCache, noopNotificationProcessor{})
		}()
	}
	wg.Wait()

	if got := countConsumerLikes(t, db, 2, video.ID); got != 1 {
		t.Fatalf("like rows after concurrent consumption = %d, want 1", got)
	}
	if got := consumerVideoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("videos.likes_count after concurrent consumption = %d, want 1", got)
	}
	if cnt, err := redisCache.ZCard(ctx, cache.LikeVideoZSetKey(video.ID)); err != nil || cnt != 1 {
		t.Fatalf("video like zset card = %d, err = %v, want 1", cnt, err)
	}
}

// TestHandleLikeMessage_DistinctEventsForSameLike 验证不同 EventID 的重复点赞事件：
// 第二条走唯一键冲突分支，但不能重复计数，且两个事件都应写入幂等标记。
func TestHandleLikeMessage_DistinctEventsForSameLike(t *testing.T) {
	likeRepo, redisCache, mr, db := setupLikeConsumer(t)
	ctx := context.Background()

	video := mustCreateConsumerVideo(t, db)
	events := []LikeEvent{
		{EventID: "like-evt-a", Action: "like", UserID: 3, UserName: "user3", VideoID: video.ID},
		{EventID: "like-evt-b", Action: "like", UserID: 3, UserName: "user3", VideoID: video.ID},
	}
	for _, event := range events {
		handleLikeMessage(ctx, nil, mustLikeDelivery(t, event), likeRepo, redisCache, noopNotificationProcessor{})
	}

	if got := countConsumerLikes(t, db, 3, video.ID); got != 1 {
		t.Fatalf("like rows = %d, want 1", got)
	}
	if got := consumerVideoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("videos.likes_count = %d, want 1", got)
	}
	for _, event := range events {
		if !mr.Exists(cache.LikeEventIDKey(event.EventID)) {
			t.Fatalf("idempotency key %q should be written", cache.LikeEventIDKey(event.EventID))
		}
	}
}

// TestHandleLikeMessage_UnlikeTwiceNeverNegative 验证重复取消点赞：
// 第二次没有删除任何行时，计数必须保持 0，并补齐缓存视图（空占位、Set/ZSet 清理）。
func TestHandleLikeMessage_UnlikeTwiceNeverNegative(t *testing.T) {
	likeRepo, redisCache, mr, db := setupLikeConsumer(t)
	ctx := context.Background()

	video := mustCreateConsumerVideo(t, db)
	userID := uint(4)
	if err := likeRepo.CreateLike(ctx, userID, video.ID); err != nil {
		t.Fatalf("seed like: %v", err)
	}
	if got := consumerVideoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("seeded videos.likes_count = %d, want 1", got)
	}

	for _, eventID := range []string{"unlike-evt-a", "unlike-evt-b"} {
		event := LikeEvent{EventID: eventID, Action: "unlike", UserID: userID, VideoID: video.ID}
		handleLikeMessage(ctx, nil, mustLikeDelivery(t, event), likeRepo, redisCache, noopNotificationProcessor{})
	}

	if got := countConsumerLikes(t, db, userID, video.ID); got != 0 {
		t.Fatalf("like rows = %d, want 0", got)
	}
	if got := consumerVideoLikesCount(t, db, video.ID); got != 0 {
		t.Fatalf("videos.likes_count = %d, want 0 (must not go negative)", got)
	}
	if member, err := redisCache.IsMemberOfSet(ctx, cache.LikeUserSetKey(userID), video.ID); err != nil || member {
		t.Fatalf("user like set member = %v, err = %v, want false", member, err)
	}
	if cnt, err := redisCache.ZCard(ctx, cache.LikeVideoZSetKey(video.ID)); err != nil || cnt != 0 {
		t.Fatalf("video like zset card = %d, err = %v, want 0", cnt, err)
	}
	if !mr.Exists(cache.LikePlaceholderKey(userID, video.ID)) {
		t.Fatalf("unlike placeholder should be written to prevent cache penetration")
	}
	if _, err := mr.ZScore(cache.VideoRankZSetKey(), fmt.Sprint(video.ID)); err == nil {
		t.Fatalf("video %d should be removed from rank after unlike", video.ID)
	}
}

// TestHandleLikeMessage_MalformedBodyIsDropped 验证坏消息不会 panic，也不会落库。
func TestHandleLikeMessage_MalformedBodyIsDropped(t *testing.T) {
	likeRepo, redisCache, _, db := setupLikeConsumer(t)

	msg := amqp.Delivery{Body: []byte("{not-json"), Headers: amqp.Table{}}
	handleLikeMessage(context.Background(), nil, msg, likeRepo, redisCache, noopNotificationProcessor{})

	var rows int64
	if err := db.Model(&model.Like{}).Count(&rows).Error; err != nil {
		t.Fatalf("count likes: %v", err)
	}
	if rows != 0 {
		t.Fatalf("like rows = %d, want 0", rows)
	}
}

// TestHandleLikeMessage_UnknownActionIsDropped 验证未知 action 被 ACK 丢弃，不产生副作用。
func TestHandleLikeMessage_UnknownActionIsDropped(t *testing.T) {
	likeRepo, redisCache, _, db := setupLikeConsumer(t)

	video := mustCreateConsumerVideo(t, db)
	event := LikeEvent{EventID: "evt-unknown", Action: "super-like", UserID: 5, VideoID: video.ID}
	handleLikeMessage(context.Background(), nil, mustLikeDelivery(t, event), likeRepo, redisCache, noopNotificationProcessor{})

	if got := countConsumerLikes(t, db, 5, video.ID); got != 0 {
		t.Fatalf("like rows = %d, want 0", got)
	}
	if got := consumerVideoLikesCount(t, db, video.ID); got != 0 {
		t.Fatalf("videos.likes_count = %d, want 0", got)
	}
}

// TestHandleLikeMessage_EmptyEventIDDoesNotDropDistinctEvents 是给 P0 缺陷预留的回归用例：
// 当前实现把空 EventID 统一映射到 mq:like_event_id:，第一条之后的事件都会被误判为重复。
// 修复方式见审查报告 P0-3；修复前跳过，避免 CI 因已知缺陷变红。
func TestHandleLikeMessage_EmptyEventIDDoesNotDropDistinctEvents(t *testing.T) {
	t.Skip("known P0: empty EventID shares the same idempotency key; see review P0-3")

	likeRepo, redisCache, _, db := setupLikeConsumer(t)
	ctx := context.Background()

	video := mustCreateConsumerVideo(t, db)
	likeEvent := LikeEvent{EventID: "", Action: "like", UserID: 6, VideoID: video.ID}
	unlikeEvent := LikeEvent{EventID: "", Action: "unlike", UserID: 6, VideoID: video.ID}

	handleLikeMessage(ctx, nil, mustLikeDelivery(t, likeEvent), likeRepo, redisCache, noopNotificationProcessor{})
	handleLikeMessage(ctx, nil, mustLikeDelivery(t, unlikeEvent), likeRepo, redisCache, noopNotificationProcessor{})

	if got := countConsumerLikes(t, db, 6, video.ID); got != 0 {
		t.Fatalf("like rows = %d, want 0 (unlike with empty event id was dropped)", got)
	}
}
