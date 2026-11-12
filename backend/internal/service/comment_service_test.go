package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"

	"github.com/alicebob/miniredis/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupCommentService 组装评论测试环境：
//   - SQLite 内存库替代 MySQL（单连接，避免并发写锁冲突）；
//   - miniredis 替代 Redis；
//   - CommentMQ / LikeMQ 均注入禁用实例，所有写路径走“MQ 不可用 → 同步写库”的降级分支，
//     这样测试无需启动 RabbitMQ，也能覆盖 service 的降级逻辑。
func setupCommentService(t *testing.T) (*CommentService, *gorm.DB, *cache.RedisCache, *miniredis.Miniredis) {
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
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(
		&model.User{},
		&model.Video{},
		&model.Comment{},
		&model.Like{},
		&model.Notification{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	commentRepo := repo.NewCommentRepo(db)
	videoRepo := repo.NewVideoRepo(db)
	userService := NewUserService(repo.NewUserRepo(db), redisCache)
	notificationService := NewNotificationService(redisCache, repo.NewNotificationRepo(db), videoRepo, commentRepo, repo.NewLikeRepo(db), repo.NewSocialRepo(db))

	// NewLikeMQ(nil) / NewCommentMQ(nil) 返回 ErrMQDisabled 与已禁用的合法实例。
	likeMQ, _ := mq.NewLikeMQ(nil)
	likeService := NewLikeService(repo.NewLikeRepo(db), likeMQ, userService, notificationService, redisCache)
	videoService := NewVideoService(videoRepo, userService, redisCache, likeService)
	likeService.SetVideoDetailer(videoService)

	commentMQ, _ := mq.NewCommentMQ(nil)
	svc := NewCommentService(commentRepo, commentMQ, redisCache, likeService, videoService, notificationService)
	return svc, db, redisCache, mr
}

func mustCreateCommentVideo(t *testing.T, db *gorm.DB, authorID uint) *model.Video {
	t.Helper()
	video := &model.Video{
		AuthorID:    authorID,
		Title:       "comment test video",
		Description: "created by comment_service_test.go",
		VideoURL:    fmt.Sprintf("/upload/videos/%d/test.mp4", authorID),
		CoverURL:    fmt.Sprintf("/upload/covers/%d/test.jpg", authorID),
	}
	if err := db.Create(video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	return video
}

func mustCreateCommentRow(t *testing.T, db *gorm.DB, comment *model.Comment) *model.Comment {
	t.Helper()
	if err := db.Create(comment).Error; err != nil {
		t.Fatalf("create comment: %v", err)
	}
	return comment
}

func commentRowCount(t *testing.T, db *gorm.DB, videoID uint) int64 {
	t.Helper()
	var rows int64
	if err := db.Model(&model.Comment{}).Where("video_id = ?", videoID).Count(&rows).Error; err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return rows
}

// TestCommentService_PublishRejectsInvalidContent 覆盖空串、纯空白、超长与不可打印字符。
// 这些请求必须在进入 MQ/数据库之前被拒绝。
func TestCommentService_PublishRejectsInvalidContent(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 1)

	cases := []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "whitespace only", content: "  \n\t\r  "},
		{name: "too long", content: strings.Repeat("评", maxCommentContentRunes+1)},
		{name: "control char", content: "hello\x00world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.PublishComment(ctx, video.ID, 10, 0, "user10", tc.content)
			if err == nil {
				t.Fatalf("PublishComment(%q) = nil, want error", tc.content)
			}
		})
	}

	if got := commentRowCount(t, db, video.ID); got != 0 {
		t.Fatalf("comment rows = %d, want 0", got)
	}
}

// TestCommentService_PublishAcceptsEmoji 验证 IsGraphic 放行 Emoji 等图形字符。
func TestCommentService_PublishAcceptsEmoji(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 1)

	const content = "太好笑了 😀👍🎉"
	if err := svc.PublishComment(ctx, video.ID, 10, 0, "user10", content); err != nil {
		t.Fatalf("PublishComment(emoji) = %v, want nil", err)
	}

	var saved model.Comment
	if err := db.Where("video_id = ?", video.ID).First(&saved).Error; err != nil {
		t.Fatalf("load emoji comment: %v", err)
	}
	if saved.Content != content {
		t.Fatalf("content = %q, want %q", saved.Content, content)
	}
}

// TestCommentService_PublishValidatesTarget 覆盖视频不存在、父评论不存在、
// 父评论不属于当前视频三种非法目标。
func TestCommentService_PublishValidatesTarget(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()

	video1 := mustCreateCommentVideo(t, db, 100)
	video2 := mustCreateCommentVideo(t, db, 100)
	parentOnVideo2 := mustCreateCommentRow(t, db, &model.Comment{
		VideoID: video2.ID, UserID: 20, UserName: "parent", Content: "parent comment",
	})

	cases := []struct {
		name string
		vid  uint
		pid  uint
	}{
		{name: "video not found", vid: 999999, pid: 0},
		{name: "parent not found", vid: video1.ID, pid: 999999},
		{name: "parent belongs to another video", vid: video1.ID, pid: parentOnVideo2.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.PublishComment(ctx, tc.vid, 10, tc.pid, "user10", "valid content"); err == nil {
				t.Fatalf("PublishComment(vid=%d, pid=%d) = nil, want error", tc.vid, tc.pid)
			}
		})
	}

	// 只允许上面直接插入的父评论存在，失败的发布不应产生新行。
	if got := commentRowCount(t, db, video1.ID); got != 0 {
		t.Fatalf("video1 comment rows = %d, want 0", got)
	}
	if got := commentRowCount(t, db, video2.ID); got != 1 {
		t.Fatalf("video2 comment rows = %d, want 1", got)
	}
}

// TestCommentService_PublishFallbackWritesDB 验证 MQ 禁用时的降级写库：
// 内容去首尾空白后落库，回复评论保留 ParentID。
func TestCommentService_PublishFallbackWritesDB(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 100)

	if err := svc.PublishComment(ctx, video.ID, 20, 0, "user20", "  top level  "); err != nil {
		t.Fatalf("publish top-level comment: %v", err)
	}

	var top model.Comment
	if err := db.Where("video_id = ? AND parent_id = 0", video.ID).First(&top).Error; err != nil {
		t.Fatalf("load top-level comment: %v", err)
	}
	if top.Content != "top level" {
		t.Fatalf("content = %q, want trimmed %q", top.Content, "top level")
	}
	if top.UserID != 20 || top.UserName != "user20" {
		t.Fatalf("unexpected author: userID=%d userName=%q", top.UserID, top.UserName)
	}

	if err := svc.PublishComment(ctx, video.ID, 21, top.ID, "user21", "reply"); err != nil {
		t.Fatalf("publish reply: %v", err)
	}
	var reply model.Comment
	if err := db.Where("parent_id = ?", top.ID).First(&reply).Error; err != nil {
		t.Fatalf("load reply: %v", err)
	}
	if reply.VideoID != video.ID || reply.UserID != 21 {
		t.Fatalf("unexpected reply: videoID=%d userID=%d", reply.VideoID, reply.UserID)
	}
}

// TestCommentService_DeletePermission 覆盖越权删除与两类合法删除：
// 评论作者可删自己的评论，视频作者可删自己视频下的他人评论，其他人一律拒绝。
func TestCommentService_DeletePermission(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()

	const (
		videoAuthor = uint(100)
		commenter   = uint(200)
		attacker    = uint(999)
	)
	video := mustCreateCommentVideo(t, db, videoAuthor)

	own := mustCreateCommentRow(t, db, &model.Comment{
		VideoID: video.ID, UserID: commenter, UserName: "commenter", Content: "my comment",
	})

	// 1. 既不是评论作者，也不是视频作者：必须拒绝，且评论不能被删除。
	if err := svc.DeleteComment(ctx, attacker, own.ID); err == nil {
		t.Fatal("DeleteComment by unrelated user = nil, want permission error")
	}
	if _, err := svc.CommentRepo.FindByID(ctx, own.ID); err != nil {
		t.Fatalf("comment should still exist after unauthorized delete: %v", err)
	}

	// 2. 评论作者删除自己的评论：允许。
	if err := svc.DeleteComment(ctx, commenter, own.ID); err != nil {
		t.Fatalf("DeleteComment by comment author: %v", err)
	}
	if _, err := svc.CommentRepo.FindByID(ctx, own.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("comment should be deleted, err = %v", err)
	}

	// 3. 视频作者删除他人评论：允许。
	reply := mustCreateCommentRow(t, db, &model.Comment{
		VideoID: video.ID, UserID: commenter, ParentID: 1, UserName: "commenter", Content: "reply",
	})
	if err := svc.DeleteComment(ctx, videoAuthor, reply.ID); err != nil {
		t.Fatalf("DeleteComment by video author: %v", err)
	}
	if _, err := svc.CommentRepo.FindByID(ctx, reply.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("reply should be deleted, err = %v", err)
	}
}

// TestCommentService_DeleteNotFound 删除不存在的评论必须返回错误，避免 handler 误报成功。
func TestCommentService_DeleteNotFound(t *testing.T) {
	svc, _, _, _ := setupCommentService(t)

	if err := svc.DeleteComment(context.Background(), 1, 999999); err == nil {
		t.Fatal("DeleteComment(nonexistent) = nil, want error")
	}
}

// TestCommentService_ListCommentsCursorPagination 覆盖正常（跨秒）游标分页：
// 第一页 5 条 + 第二页 2 条，不重复、不遗漏，排序为 created_at DESC。
func TestCommentService_ListCommentsCursorPagination(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 100)

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 7; i++ {
		mustCreateCommentRow(t, db, &model.Comment{
			VideoID:   video.ID,
			UserID:    uint(10 + i),
			UserName:  fmt.Sprintf("user%d", 10+i),
			Content:   fmt.Sprintf("comment-%d", i),
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	page1, err := svc.ListComments(ctx, 0, dto.ListCommentsRequest{VideoID: video.ID, PageSize: 5})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1.Comments) != 5 {
		t.Fatalf("page1 len = %d, want 5", len(page1.Comments))
	}
	if !page1.HasMore {
		t.Fatal("page1 has_more = false, want true")
	}
	if page1.LastTime == nil || page1.LastID == nil {
		t.Fatal("page1 cursor should not be nil")
	}
	if page1.Comments[0].Content != "comment-6" {
		t.Fatalf("page1 first content = %q, want comment-6", page1.Comments[0].Content)
	}
	if page1.Total != 7 {
		t.Fatalf("page1 total = %d, want 7", page1.Total)
	}

	page2, err := svc.ListComments(ctx, 0, dto.ListCommentsRequest{
		VideoID:  video.ID,
		LastTime: page1.LastTime,
		LastID:   page1.LastID,
		PageSize: 5,
	})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2.Comments) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2.Comments))
	}
	if page2.HasMore {
		t.Fatal("page2 has_more = true, want false")
	}

	seen := make(map[uint]struct{}, 7)
	for _, c := range page1.Comments {
		seen[c.ID] = struct{}{}
	}
	for _, c := range page2.Comments {
		if _, ok := seen[c.ID]; ok {
			t.Fatalf("comment %d appears on both pages", c.ID)
		}
		seen[c.ID] = struct{}{}
	}
	if len(seen) != 7 {
		t.Fatalf("unique comments = %d, want 7", len(seen))
	}
}

// TestCommentService_ListCommentsSameSecondDoesNotDropRows 覆盖 P1 缺陷修复：
// 游标改用 Unix 毫秒，与 MySQL datetime(3) 精度对齐，同一秒内的评论不会被整段跳过。
func TestCommentService_ListCommentsSameSecondDoesNotDropRows(t *testing.T) {
	svc, db, _, _ := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 100)

	// 全部落在同一秒内，但毫秒非零；当前实现用 time.Unix(秒,0) 回查，剩余行会被漏掉。
	base := time.Now().Add(-time.Hour).Truncate(time.Second).Add(100 * time.Millisecond)
	for i := 0; i < 6; i++ {
		mustCreateCommentRow(t, db, &model.Comment{
			VideoID:   video.ID,
			UserID:    uint(10 + i),
			UserName:  fmt.Sprintf("user%d", 10+i),
			Content:   fmt.Sprintf("same-second-%d", i),
			CreatedAt: base.Add(time.Duration(i) * 10 * time.Millisecond),
		})
	}

	page1, err := svc.ListComments(ctx, 0, dto.ListCommentsRequest{VideoID: video.ID, PageSize: 5})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	page2, err := svc.ListComments(ctx, 0, dto.ListCommentsRequest{
		VideoID: video.ID, LastTime: page1.LastTime, LastID: page1.LastID, PageSize: 5,
	})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if got := len(page1.Comments) + len(page2.Comments); got != 6 {
		t.Fatalf("paged comments = %d, want 6 (rows in the same second were dropped)", got)
	}
}

// TestCommentService_MQFallbackKeepsCommentCountCacheInSync 覆盖 P1 缺陷修复：
// MQ 投递失败走同步写库/删库时，必须同步失效 comment:video:cnt:{vid}，避免读取端长期返回旧值。
func TestCommentService_MQFallbackKeepsCommentCountCacheInSync(t *testing.T) {
	svc, db, redisCache, mr := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 100)
	key := cache.CommentVideoCntKey(video.ID)

	// 发布降级：缓存旧值 0，落库后应失效，下一次读取回源数据库得到 1。
	if err := redisCache.Set(ctx, key, "0", cache.CommentVideoCntTTL); err != nil {
		t.Fatalf("seed count cache: %v", err)
	}
	if err := svc.PublishComment(ctx, video.ID, 20, 0, "user20", "fallback publish"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if mr.Exists(key) {
		t.Fatal("count cache should be invalidated after fallback publish")
	}
	if cnt, err := svc.GetCommentCount(ctx, video.ID); err != nil || cnt != 1 {
		t.Fatalf("count after fallback publish = %d, err = %v, want 1", cnt, err)
	}
}

// TestCommentService_GetCommentCountCache 覆盖 Redis 命中、未命中回源并回填、批量查询与非法参数。
func TestCommentService_GetCommentCountCache(t *testing.T) {
	svc, db, redisCache, mr := setupCommentService(t)
	ctx := context.Background()
	video := mustCreateCommentVideo(t, db, 100)

	mustCreateCommentRow(t, db, &model.Comment{VideoID: video.ID, UserID: 1, UserName: "u1", Content: "c1"})
	mustCreateCommentRow(t, db, &model.Comment{VideoID: video.ID, UserID: 2, UserName: "u2", Content: "c2"})

	key := cache.CommentVideoCntKey(video.ID)
	cnt, err := svc.GetCommentCount(ctx, video.ID)
	if err != nil {
		t.Fatalf("GetCommentCount: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("count = %d, want 2", cnt)
	}
	if got, err := mr.Get(key); err != nil || got != "2" {
		t.Fatalf("cached count = %q, err = %v, want \"2\"", got, err)
	}
	if ttl := mr.TTL(key); ttl <= 0 {
		t.Fatalf("cached count ttl = %v, want > 0", ttl)
	}

	// 缓存命中后按需续期。
	mr.Set(key, "9")
	cnt, err = svc.GetCommentCount(ctx, video.ID)
	if err != nil || cnt != 9 {
		t.Fatalf("cached GetCommentCount = %d, err = %v, want 9", cnt, err)
	}
	if ttl := mr.TTL(key); ttl <= 0 {
		t.Fatalf("cached count ttl after refresh = %v, want > 0", ttl)
	}

	counts, err := svc.BatchGetCommentCount(ctx, []string{fmt.Sprint(video.ID), "999999"})
	if err != nil {
		t.Fatalf("BatchGetCommentCount: %v", err)
	}
	if counts[fmt.Sprint(video.ID)] != 9 || counts["999999"] != 0 {
		t.Fatalf("counts = %#v, want video=%d -> 9, 999999 -> 0", counts, video.ID)
	}

	if _, err := svc.BatchGetCommentCount(ctx, []string{"not-a-number"}); err == nil {
		t.Fatal("BatchGetCommentCount(invalid id) = nil, want error")
	}

	// 确保测试没有因为缓存禁用而走空实现。
	if !redisCache.IsEnabled() {
		t.Fatal("redis cache should be enabled in test")
	}
}
