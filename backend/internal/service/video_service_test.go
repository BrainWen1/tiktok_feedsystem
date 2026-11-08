package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"

	"github.com/alicebob/miniredis/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupVideoService 组装本地测试环境：
//   - SQLite 内存库替代 MySQL；
//   - miniredis 替代真实 Redis，覆盖详情缓存与点赞状态缓存；
//   - LikeService 只用到 IsLiked 的数据库分支，MQ/通知依赖传 nil。
func setupVideoService(t *testing.T) (*VideoService, *gorm.DB, *miniredis.Miniredis) {
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

	if err := db.AutoMigrate(&model.User{}, &model.Video{}, &model.VideoTag{}, &model.Like{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	userService := NewUserService(repo.NewUserRepo(db), redisCache)
	likeService := NewLikeService(repo.NewLikeRepo(db), nil, userService, nil, redisCache)
	videoService := NewVideoService(repo.NewVideoRepo(db), userService, redisCache, likeService)
	return videoService, db, mr
}

func mustCreateUser(t *testing.T, db *gorm.DB, username string) *model.User {
	t.Helper()
	user := &model.User{UserName: username, Password: "not-used-in-video-tests"}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return user
}

func mustCreateVideo(t *testing.T, db *gorm.DB, authorID uint, title string, createdAt time.Time) *model.Video {
	t.Helper()
	video := &model.Video{
		AuthorID:    authorID,
		Title:       title,
		VideoURL:    "/upload/videos/1/sample.mp4",
		CoverURL:    "/upload/covers/1/sample.png",
		Description: "test video",
		CreatedAt:   createdAt,
	}
	if err := db.Create(video).Error; err != nil {
		t.Fatalf("create video %q: %v", title, err)
	}
	return video
}

func TestNormalizeVideoTags(t *testing.T) {
	longTag := strings.Repeat("中", maxTagLength+5)
	tags := []string{
		"  go  ", "", "go", "golang", longTag,
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k",
	}

	got := normalizeVideoTags(tags)

	if len(got) != maxVideoTags {
		t.Fatalf("normalizeVideoTags() returned %d tags, want %d: %v", len(got), maxVideoTags, got)
	}
	if got[0] != "go" || got[1] != "golang" {
		t.Fatalf("tags not trimmed/deduplicated: %v", got)
	}
	if runes := []rune(got[2]); len(runes) != maxTagLength {
		t.Fatalf("long tag should be truncated to %d runes, got %d", maxTagLength, len(runes))
	}
	// 去重后最多 10 个，第 10 个应停在 "g"
	if got[maxVideoTags-1] != "g" {
		t.Fatalf("tag list should be capped at %d items, got %v", maxVideoTags, got)
	}

	if empty := normalizeVideoTags([]string{"", "   "}); len(empty) != 0 {
		t.Fatalf("blank tags should be dropped, got %v", empty)
	}
}

func TestPublishVideoCreatesVideoAndTags(t *testing.T) {
	svc, db, _ := setupVideoService(t)
	author := mustCreateUser(t, db, "publish_author")

	resp, err := svc.PublishVideo(context.Background(), author.ID, "hello", "desc",
		"/upload/videos/1/sample.mp4", "/upload/covers/1/sample.png",
		[]string{" go ", "go", "golang"})
	if err != nil {
		t.Fatalf("PublishVideo() error = %v", err)
	}
	if resp == nil || resp.VideoID == 0 {
		t.Fatalf("PublishVideo() returned empty video id: %+v", resp)
	}

	var video model.Video
	if err := db.First(&video, resp.VideoID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if video.AuthorID != author.ID || video.Title != "hello" {
		t.Fatalf("unexpected video row: %+v", video)
	}

	var tags []model.VideoTag
	if err := db.Where("video_id = ?", resp.VideoID).Order("tag").Find(&tags).Error; err != nil {
		t.Fatalf("load video tags: %v", err)
	}
	if len(tags) != 2 || tags[0].Tag != "go" || tags[1].Tag != "golang" {
		t.Fatalf("tags should be normalized and deduplicated, got %+v", tags)
	}
}

func TestPublishVideoRejectsInvalidInput(t *testing.T) {
	svc, db, _ := setupVideoService(t)
	author := mustCreateUser(t, db, "publish_invalid_author")
	ctx := context.Background()

	cases := []struct {
		name     string
		title    string
		videoURL string
		coverURL string
	}{
		{name: "empty title", title: "", videoURL: "/upload/videos/1/a.mp4", coverURL: "/upload/covers/1/a.png"},
		{name: "empty video url", title: "t", videoURL: "", coverURL: "/upload/covers/1/a.png"},
		{name: "empty cover url", title: "t", videoURL: "/upload/videos/1/a.mp4", coverURL: ""},
		{name: "invalid video prefix", title: "t", videoURL: "http://evil.example/a.mp4", coverURL: "/upload/covers/1/a.png"},
		{name: "invalid cover prefix", title: "t", videoURL: "/upload/videos/1/a.mp4", coverURL: "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.PublishVideo(ctx, author.ID, tc.title, "", tc.videoURL, tc.coverURL, nil); err == nil {
				t.Fatal("PublishVideo() should reject invalid input")
			}
		})
	}
}

func TestVideoDetailCachesAndResolvesLikeStatus(t *testing.T) {
	svc, db, mr := setupVideoService(t)
	author := mustCreateUser(t, db, "detail_author")
	viewer := mustCreateUser(t, db, "detail_viewer")
	video := mustCreateVideo(t, db, author.ID, "detail video", time.Now())
	ctx := context.Background()

	// 游客访问：IsLiked 恒为 false，并把视频公共元数据写入缓存
	guestDetail, err := svc.VideoDetail(ctx, video.ID, 0)
	if err != nil {
		t.Fatalf("guest VideoDetail() error = %v", err)
	}
	if guestDetail.IsLiked {
		t.Fatal("guest IsLiked should be false")
	}
	cacheKey := fmt.Sprintf("video_detail:%d", video.ID)
	if !mr.Exists(cacheKey) {
		t.Fatalf("video detail cache %q should be written", cacheKey)
	}

	// 登录用户点赞后应命中数据库并回写点赞集合
	if err := db.Create(&model.Like{UserID: viewer.ID, VideoID: video.ID}).Error; err != nil {
		t.Fatalf("create like: %v", err)
	}
	detail, err := svc.VideoDetail(ctx, video.ID, viewer.ID)
	if err != nil {
		t.Fatalf("viewer VideoDetail() error = %v", err)
	}
	if !detail.IsLiked {
		t.Fatal("viewer IsLiked should be true after liking")
	}
	likeSetKey := cache.LikeUserSetKey(viewer.ID)
	if !mr.Exists(likeSetKey) {
		t.Fatalf("like set cache %q should be written", likeSetKey)
	}
	members, err := mr.Members(likeSetKey)
	if err != nil {
		t.Fatalf("read like set: %v", err)
	}
	if len(members) != 1 || members[0] != fmt.Sprint(video.ID) {
		t.Fatalf("like set should contain video %d, got %v", video.ID, members)
	}
}

func TestVideoDetailNotFound(t *testing.T) {
	svc, _, _ := setupVideoService(t)
	if _, err := svc.VideoDetail(context.Background(), 999999, 0); err == nil {
		t.Fatal("VideoDetail() should return error for missing video")
	}
}

func TestVideoListCursorPagination(t *testing.T) {
	svc, db, _ := setupVideoService(t)
	author := mustCreateUser(t, db, "list_author")

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	const total = 5
	wantOrder := make([]uint, 0, total)
	for i := range total {
		v := mustCreateVideo(t, db, author.ID, fmt.Sprintf("video-%d", i), base.Add(time.Duration(i)*time.Second))
		wantOrder = append(wantOrder, v.ID)
	}
	// wantOrder 目前是升序，期望按创建时间倒序返回
	for i, j := 0, len(wantOrder)-1; i < j; i, j = i+1, j-1 {
		wantOrder[i], wantOrder[j] = wantOrder[j], wantOrder[i]
	}

	ctx := context.Background()
	cursor := dto.FeedCursor{}
	var got []uint
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate, possible cursor loop")
		}
		resp, err := svc.VideoList(ctx, author.ID, 0, cursor, 2)
		if err != nil {
			t.Fatalf("VideoList() page %d error = %v", page, err)
		}
		if resp.Total != total {
			t.Fatalf("VideoList() total = %d, want %d", resp.Total, total)
		}
		for _, item := range resp.Videos {
			got = append(got, item.ID)
		}
		if resp.NextCursor == nil {
			break
		}
		cursor = *resp.NextCursor
	}

	if len(got) != total {
		t.Fatalf("pagination returned %d videos, want %d: %v", len(got), total, got)
	}
	for i := range wantOrder {
		if got[i] != wantOrder[i] {
			t.Fatalf("pagination order = %v, want %v", got, wantOrder)
		}
	}
}

func TestVideoListEmptyAndPageSizeClamp(t *testing.T) {
	svc, db, _ := setupVideoService(t)
	author := mustCreateUser(t, db, "list_empty_author")
	ctx := context.Background()

	resp, err := svc.VideoList(ctx, author.ID, 0, dto.FeedCursor{}, 0)
	if err != nil {
		t.Fatalf("VideoList() error = %v", err)
	}
	if resp.Total != 0 || len(resp.Videos) != 0 || resp.NextCursor != nil {
		t.Fatalf("empty author list should be empty, got %+v", resp)
	}

	for i := range defaultVideoPageSize + 5 {
		mustCreateVideo(t, db, author.ID, fmt.Sprintf("clamp-%d", i), time.Now().Add(time.Duration(i)*time.Second))
	}
	// pageSize 超出上限时应被钳制为 defaultVideoPageSize，而不是全量返回
	resp, err = svc.VideoList(ctx, author.ID, 0, dto.FeedCursor{}, 10000)
	if err != nil {
		t.Fatalf("VideoList() with huge page size error = %v", err)
	}
	if len(resp.Videos) != defaultVideoPageSize {
		t.Fatalf("page size should be clamped to %d, got %d", defaultVideoPageSize, len(resp.Videos))
	}
}

func TestVideoListRejectsIncompleteCursor(t *testing.T) {
	svc, db, _ := setupVideoService(t)
	author := mustCreateUser(t, db, "list_cursor_author")
	mustCreateVideo(t, db, author.ID, "cursor-video", time.Now())

	// 只有 last_vid、没有 last_created_at，属于半个游标，必须拒绝
	_, err := svc.VideoList(context.Background(), author.ID, 0, dto.FeedCursor{LastVid: "1"}, 10)
	if err == nil {
		t.Fatal("VideoList() should reject cursor with only last_vid")
	}
}

// 并发读取同一个视频详情时，缓存锁应保证结果一致且不 panic（配合 -race 运行）。
func TestVideoDetailConcurrentReaders(t *testing.T) {
	svc, db, _ := setupVideoService(t)
	author := mustCreateUser(t, db, "concurrent_author")
	video := mustCreateVideo(t, db, author.ID, "concurrent video", time.Now())

	const readers = 8
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for range readers {
		wg.Go(func() {
			detail, err := svc.VideoDetail(context.Background(), video.ID, 0)
			if err != nil {
				errs <- err
				return
			}
			if detail.ID != video.ID {
				errs <- fmt.Errorf("detail id = %d, want %d", detail.ID, video.ID)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent VideoDetail() error = %v", err)
	}
}
