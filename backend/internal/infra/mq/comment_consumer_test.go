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

func setupCommentConsumer(t *testing.T) (*repo.CommentRepo, *cache.RedisCache, *miniredis.Miniredis, *gorm.DB) {
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

	if err := db.AutoMigrate(&model.Comment{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	return repo.NewCommentRepo(db), redisCache, mr, db
}

func mustCommentDelivery(t *testing.T, event CommentEvent) amqp.Delivery {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal comment event: %v", err)
	}
	return amqp.Delivery{Body: body, Headers: amqp.Table{}}
}

func commentConsumerRowCount(t *testing.T, db *gorm.DB, videoID uint) int64 {
	t.Helper()
	var rows int64
	if err := db.Model(&model.Comment{}).Where("video_id = ?", videoID).Count(&rows).Error; err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return rows
}

func mustSeedConsumerComment(t *testing.T, commentRepo *repo.CommentRepo, videoID, userID uint, content string) *model.Comment {
	t.Helper()
	comment := &model.Comment{
		VideoID:  videoID,
		UserID:   userID,
		UserName: fmt.Sprintf("user%d", userID),
		Content:  content,
	}
	if err := commentRepo.CreateComment(context.Background(), comment); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	return comment
}

// TestHandleCommentMessage_CreateWritesDBAndCache 覆盖创建事件的主链路：
// 评论落库、幂等标记写入、评论计数缓存按数据库真实值回写。
func TestHandleCommentMessage_CreateWritesDBAndCache(t *testing.T) {
	commentRepo, redisCache, mr, db := setupCommentConsumer(t)
	ctx := context.Background()
	const videoID = uint(42)

	event := CommentEvent{
		EventID: "comment-evt-create",
		Action:  "create",
		Comment: &model.Comment{
			VideoID: videoID, UserID: 7, UserName: "user7", Content: "hello",
		},
	}
	handleCommentMessage(ctx, nil, mustCommentDelivery(t, event), commentRepo, redisCache, noopNotificationProcessor{})

	if got := commentConsumerRowCount(t, db, videoID); got != 1 {
		t.Fatalf("comment rows = %d, want 1", got)
	}
	if !mr.Exists(cache.CommentEventIDKey(event.EventID)) {
		t.Fatalf("idempotency key %q should be written", cache.CommentEventIDKey(event.EventID))
	}
	got, err := mr.Get(cache.CommentVideoCntKey(videoID))
	if err != nil || got != "1" {
		t.Fatalf("comment count cache = %q, err = %v, want \"1\"", got, err)
	}
}

// TestHandleCommentMessage_DuplicateDeliveryIsIdempotent 覆盖 RabbitMQ 至少一次投递：
// 同一个 EventID 消费两次只能落一条评论，计数保持 1。
func TestHandleCommentMessage_DuplicateDeliveryIsIdempotent(t *testing.T) {
	commentRepo, redisCache, _, db := setupCommentConsumer(t)
	ctx := context.Background()
	const videoID = uint(43)

	event := CommentEvent{
		EventID: "comment-evt-dup",
		Action:  "create",
		Comment: &model.Comment{
			VideoID: videoID, UserID: 8, UserName: "user8", Content: "dup",
		},
	}
	msg := mustCommentDelivery(t, event)
	handleCommentMessage(ctx, nil, msg, commentRepo, redisCache, noopNotificationProcessor{})
	handleCommentMessage(ctx, nil, msg, commentRepo, redisCache, noopNotificationProcessor{})

	if got := commentConsumerRowCount(t, db, videoID); got != 1 {
		t.Fatalf("comment rows after duplicate delivery = %d, want 1", got)
	}
	if got, _ := redisCache.Get(ctx, cache.CommentVideoCntKey(videoID)); got != "1" {
		t.Fatalf("comment count cache = %q, want \"1\"", got)
	}
}

// TestHandleCommentMessage_DeleteRemovesRowAndRefreshesCache 覆盖删除事件：
// 物理删除评论后，计数缓存必须回写为数据库真实值。
func TestHandleCommentMessage_DeleteRemovesRowAndRefreshesCache(t *testing.T) {
	commentRepo, redisCache, mr, db := setupCommentConsumer(t)
	ctx := context.Background()
	const videoID = uint(44)

	first := mustSeedConsumerComment(t, commentRepo, videoID, 9, "first")
	mustSeedConsumerComment(t, commentRepo, videoID, 10, "second")

	event := CommentEvent{
		EventID: "comment-evt-delete",
		Action:  "delete",
		Comment: &model.Comment{ID: first.ID, VideoID: videoID, UserID: first.UserID},
	}
	handleCommentMessage(ctx, nil, mustCommentDelivery(t, event), commentRepo, redisCache, noopNotificationProcessor{})

	if got := commentConsumerRowCount(t, db, videoID); got != 1 {
		t.Fatalf("comment rows after delete = %d, want 1", got)
	}
	if _, err := commentRepo.FindByID(ctx, first.ID); err == nil {
		t.Fatal("deleted comment should not be found")
	}
	if got, err := mr.Get(cache.CommentVideoCntKey(videoID)); err != nil || got != "1" {
		t.Fatalf("comment count cache = %q, err = %v, want \"1\"", got, err)
	}
}

// TestHandleCommentMessage_MalformedBodyIsDropped 验证坏 JSON 不会 panic，也不会落库。
func TestHandleCommentMessage_MalformedBodyIsDropped(t *testing.T) {
	commentRepo, redisCache, _, db := setupCommentConsumer(t)

	msg := amqp.Delivery{Body: []byte("{not-json"), Headers: amqp.Table{}}
	handleCommentMessage(context.Background(), nil, msg, commentRepo, redisCache, noopNotificationProcessor{})

	var rows int64
	if err := db.Model(&model.Comment{}).Count(&rows).Error; err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if rows != 0 {
		t.Fatalf("comment rows = %d, want 0", rows)
	}
}

// TestHandleCommentMessage_UnknownActionIsDropped 验证未知 action 被 ACK 丢弃，不产生副作用。
func TestHandleCommentMessage_UnknownActionIsDropped(t *testing.T) {
	commentRepo, redisCache, _, db := setupCommentConsumer(t)

	event := CommentEvent{
		EventID: "comment-evt-unknown",
		Action:  "update",
		Comment: &model.Comment{VideoID: 45, UserID: 1, Content: "unknown"},
	}
	handleCommentMessage(context.Background(), nil, mustCommentDelivery(t, event), commentRepo, redisCache, noopNotificationProcessor{})

	if got := commentConsumerRowCount(t, db, 45); got != 0 {
		t.Fatalf("comment rows = %d, want 0", got)
	}
}

// TestHandleCommentMessage_NilCommentDoesNotCrashWorker 是给 P0 缺陷预留的回归用例：
// 消息体缺少 comment 字段时，create 会调用 GORM Create(nil) panic，delete 会直接空指针，
// 未经 recover 的消费协程 panic 会带崩整个 worker 进程。
// 修复方式见审查报告 P0-1；修复前跳过。
func TestHandleCommentMessage_NilCommentDoesNotCrashWorker(t *testing.T) {
	t.Skip("known P0: nil event.Comment panics the worker; see review P0-1")

	commentRepo, redisCache, _, db := setupCommentConsumer(t)

	for _, action := range []string{"create", "delete"} {
		event := CommentEvent{EventID: "comment-evt-nil-" + action, Action: action}
		// 当前实现会在这里 panic；修复后应安全 ACK/Nack 并返回。
		handleCommentMessage(context.Background(), nil, mustCommentDelivery(t, event), commentRepo, redisCache, noopNotificationProcessor{})
	}

	var rows int64
	if err := db.Model(&model.Comment{}).Count(&rows).Error; err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if rows != 0 {
		t.Fatalf("comment rows = %d, want 0", rows)
	}
}

// TestHandleCommentMessage_ConcurrentSameEvent 是给 P0 缺陷预留的回归用例：
// 幂等判断是 GET 后 SET 的 check-then-act，且 comments 表没有事件唯一键；
// 同一 EventID 被多个消费者并发处理时会重复落库。
// 修复方式见审查报告 P0-2；修复前跳过。
func TestHandleCommentMessage_ConcurrentSameEvent(t *testing.T) {
	t.Skip("known P0: check-then-set idempotency without a DB unique key allows duplicates; see review P0-2")

	commentRepo, redisCache, _, db := setupCommentConsumer(t)
	ctx := context.Background()
	const videoID = uint(46)

	event := CommentEvent{
		EventID: "comment-evt-concurrent",
		Action:  "create",
		Comment: &model.Comment{VideoID: videoID, UserID: 11, UserName: "user11", Content: "concurrent"},
	}
	msg := mustCommentDelivery(t, event)

	const goroutines = 8
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleCommentMessage(ctx, nil, msg, commentRepo, redisCache, noopNotificationProcessor{})
		}()
	}
	wg.Wait()

	if got := commentConsumerRowCount(t, db, videoID); got != 1 {
		t.Fatalf("comment rows after concurrent consumption = %d, want 1", got)
	}
}

// TestHandleCommentMessage_EmptyEventIDDoesNotDropDistinctEvents 是给 P0/P1 缺陷预留的回归用例：
// 空 EventID 会被拼成同一个幂等 key，第一条之后的事件都会被误判为重复并 ACK 丢弃。
// 修复方式见审查报告 P0-2；修复前跳过。
func TestHandleCommentMessage_EmptyEventIDDoesNotDropDistinctEvents(t *testing.T) {
	t.Skip("known P0: empty EventID shares one idempotency key; see review P0-2")

	commentRepo, redisCache, _, db := setupCommentConsumer(t)
	ctx := context.Background()
	const videoID = uint(47)

	events := []CommentEvent{
		{Action: "create", Comment: &model.Comment{VideoID: videoID, UserID: 12, UserName: "u12", Content: "a"}},
		{Action: "create", Comment: &model.Comment{VideoID: videoID, UserID: 13, UserName: "u13", Content: "b"}},
	}
	for _, event := range events {
		handleCommentMessage(ctx, nil, mustCommentDelivery(t, event), commentRepo, redisCache, noopNotificationProcessor{})
	}

	if got := commentConsumerRowCount(t, db, videoID); got != 2 {
		t.Fatalf("comment rows = %d, want 2 (event with empty id was dropped)", got)
	}
}
