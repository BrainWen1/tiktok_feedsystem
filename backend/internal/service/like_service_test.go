package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/infra/mq"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// fakeVideoDetailer 只实现 LikeService 需要的视频详情能力，
// 避免测试里启动完整的 VideoService/作者查询链路。
type fakeVideoDetailer struct {
	mu     sync.RWMutex
	videos map[uint]dto.VideoDetailResponse
	err    error
}

func (f *fakeVideoDetailer) VideoDetail(_ context.Context, videoID uint, _ uint) (*dto.VideoDetailResponse, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.videos[videoID]
	if !ok {
		return nil, fmt.Errorf("video %d not found", videoID)
	}
	return &v, nil
}

func (f *fakeVideoDetailer) put(v dto.VideoDetailResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.videos[v.ID] = v
}

// setupLikeService 组装点赞测试环境：
//   - SQLite 内存库替代 MySQL；
//   - miniredis 替代 Redis；
//   - LikeMQ 以禁用实例注入，所有用例走“MQ 不可用→直写数据库”的降级分支；
//   - NotificationService 使用真实实现，但用例统一构造“自己点赞自己的视频”来跳过通知落库。
func setupLikeService(t *testing.T) (*LikeService, *gorm.DB, *cache.RedisCache, *miniredis.Miniredis, *fakeVideoDetailer) {
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
	// 单连接保证 SQLite 写事务串行化，避免 "database is locked" 干扰并发用例。
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.User{}, &model.Video{}, &model.Like{}, &model.Notification{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	// NewLikeMQ(nil) 返回 ErrMQDisabled 与一个已禁用的合法实例。
	likeMQ, _ := mq.NewLikeMQ(nil)
	notificationService := NewNotificationService(
		redisCache,
		repo.NewNotificationRepo(db),
		repo.NewVideoRepo(db),
		repo.NewCommentRepo(db),
		repo.NewLikeRepo(db),
		repo.NewSocialRepo(db),
	)
	userService := NewUserService(repo.NewUserRepo(db), redisCache)
	svc := NewLikeService(repo.NewLikeRepo(db), likeMQ, userService, notificationService, redisCache)

	detailer := &fakeVideoDetailer{videos: make(map[uint]dto.VideoDetailResponse)}
	svc.SetVideoDetailer(detailer)
	return svc, db, redisCache, mr, detailer
}

func mustCreateLikeTestVideo(t *testing.T, db *gorm.DB, authorID uint) *model.Video {
	t.Helper()
	video := &model.Video{
		AuthorID:    authorID,
		Title:       "like test video",
		Description: "created by like_service_test.go",
		VideoURL:    fmt.Sprintf("/upload/videos/%d/test.mp4", authorID),
		CoverURL:    fmt.Sprintf("/upload/covers/%d/test.jpg", authorID),
	}
	if err := db.Create(video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	return video
}

func countLikes(t *testing.T, db *gorm.DB, userID, videoID uint) int64 {
	t.Helper()
	var rows int64
	if err := db.Model(&model.Like{}).Where("user_id = ? AND video_id = ?", userID, videoID).Count(&rows).Error; err != nil {
		t.Fatalf("count likes: %v", err)
	}
	return rows
}

func videoLikesCount(t *testing.T, db *gorm.DB, videoID uint) int64 {
	t.Helper()
	var video model.Video
	if err := db.Select("id", "likes_count").First(&video, videoID).Error; err != nil {
		t.Fatalf("load video %d: %v", videoID, err)
	}
	return video.LikesCount
}

// TestLikeVideo_FallbackWritesDBAndInvalidatesDetailCache 验证 MQ 不可用时的降级链路：
// 点赞落库、计数只加一次、详情缓存被失效，重复点赞被前置检查拦截。
func TestLikeVideo_FallbackWritesDBAndInvalidatesDetailCache(t *testing.T) {
	svc, db, _, mr, detailer := setupLikeService(t)
	ctx := context.Background()

	// 自己点赞自己的视频：通知逻辑会直接跳过，避免异步写入干扰断言。
	userID := uint(11)
	video := mustCreateLikeTestVideo(t, db, userID)
	detailer.put(dto.VideoDetailResponse{ID: video.ID})

	detailKey := cache.VideoDetailKey(video.ID)
	if err := mr.Set(detailKey, "stale"); err != nil {
		t.Fatalf("seed detail cache: %v", err)
	}

	if err := svc.LikeVideo(ctx, userID, video.ID, "user11"); err != nil {
		t.Fatalf("first like should succeed: %v", err)
	}
	if mr.Exists(detailKey) {
		t.Fatalf("video detail cache should be invalidated after fallback write")
	}
	if got := countLikes(t, db, userID, video.ID); got != 1 {
		t.Fatalf("like rows = %d, want 1", got)
	}
	if got := videoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("videos.likes_count = %d, want 1", got)
	}

	// 第二次点赞应被 IsLiked 前置检查拦截，且不产生第二条记录。
	err := svc.LikeVideo(ctx, userID, video.ID, "user11")
	if err == nil || !strings.Contains(err.Error(), "already liked") {
		t.Fatalf("second like error = %v, want already liked", err)
	}
	if got := countLikes(t, db, userID, video.ID); got != 1 {
		t.Fatalf("like rows after duplicate = %d, want 1", got)
	}
	if got := videoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("videos.likes_count after duplicate = %d, want 1", got)
	}

	// 等待降级分支里的通知 goroutine 结束，避免测试清理时数据库已关闭。
	time.Sleep(100 * time.Millisecond)
}

// TestLikeVideo_ConcurrentClicksKeepSingleLike 模拟同一用户并发点击点赞：
// 前置检查会拦住大部分重复请求；即使通过前置检查，数据库唯一键保证最终只有一条点赞、计数只加一次，
// 唯一键冲突按幂等成功处理（P1-12），因此失败只可能来自“已点赞”前置检查。
func TestLikeVideo_ConcurrentClicksKeepSingleLike(t *testing.T) {
	svc, db, _, _, detailer := setupLikeService(t)
	ctx := context.Background()

	userID := uint(21)
	video := mustCreateLikeTestVideo(t, db, userID)
	detailer.put(dto.VideoDetailResponse{ID: video.ID})

	const goroutines = 8
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = svc.LikeVideo(ctx, userID, video.ID, "user21")
		}(i)
	}
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
			continue
		}
		if !strings.Contains(err.Error(), "already liked") {
			t.Fatalf("unexpected concurrent like error: %v", err)
		}
	}
	if success < 1 {
		t.Fatalf("successful likes = %d, want at least 1; errors = %v", success, errs)
	}
	if got := countLikes(t, db, userID, video.ID); got != 1 {
		t.Fatalf("like rows = %d, want 1", got)
	}
	if got := videoLikesCount(t, db, video.ID); got != 1 {
		t.Fatalf("videos.likes_count = %d, want 1", got)
	}

	time.Sleep(100 * time.Millisecond)
}

// TestUnlikeVideo_ConcurrentClicksNeverNegative 验证并发取消点赞不会把计数减成负数，
// 并且重复取消不会留下任何点赞行。
func TestUnlikeVideo_ConcurrentClicksNeverNegative(t *testing.T) {
	svc, db, _, _, _ := setupLikeService(t)
	ctx := context.Background()

	userID := uint(31)
	video := mustCreateLikeTestVideo(t, db, 99)
	if err := repo.NewLikeRepo(db).CreateLike(ctx, userID, video.ID); err != nil {
		t.Fatalf("seed like: %v", err)
	}

	const goroutines = 8
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = svc.UnlikeVideo(ctx, userID, video.ID)
		}(i)
	}
	wg.Wait()

	if got := countLikes(t, db, userID, video.ID); got != 0 {
		t.Fatalf("like rows after concurrent unlike = %d, want 0", got)
	}
	if got := videoLikesCount(t, db, video.ID); got != 0 {
		t.Fatalf("videos.likes_count after concurrent unlike = %d, want 0", got)
	}

	// 全部取消完成后再取消一次，应明确返回“未点赞”而不是静默成功或计数下溢。
	err := svc.UnlikeVideo(ctx, userID, video.ID)
	if err == nil || !strings.Contains(err.Error(), "not liked") {
		t.Fatalf("unlike after removal error = %v, want not liked", err)
	}
}

// TestListLikedVideos_CursorPagination 覆盖 (created_at, video_id) 复合游标：
// 第一页取最新两条，第二页只能拿到剩余一条，且两页不重复。
func TestListLikedVideos_CursorPagination(t *testing.T) {
	svc, db, _, _, detailer := setupLikeService(t)
	ctx := context.Background()

	targetUserID := uint(41)
	baseTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	likedVids := make([]uint, 0, 3)
	for i := 0; i < 3; i++ {
		video := mustCreateLikeTestVideo(t, db, 99)
		detailer.put(dto.VideoDetailResponse{ID: video.ID})
		like := &model.Like{
			UserID:    targetUserID,
			VideoID:   video.ID,
			CreatedAt: baseTime.Add(time.Duration(i) * time.Minute),
		}
		if err := db.Create(like).Error; err != nil {
			t.Fatalf("seed like %d: %v", i, err)
		}
		likedVids = append(likedVids, video.ID)
	}

	page1, err := svc.ListLikedVideos(ctx, targetUserID, 0, dto.FeedCursor{}, 2)
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if page1.Total != 3 {
		t.Fatalf("page1 total = %d, want 3", page1.Total)
	}
	if len(page1.Videos) != 2 {
		t.Fatalf("page1 videos = %d, want 2", len(page1.Videos))
	}
	if page1.NextCursor == nil {
		t.Fatalf("page1 next cursor should not be nil")
	}
	// 最新点赞的两条：likedVids[2]、likedVids[1]
	if page1.Videos[0].ID != likedVids[2] || page1.Videos[1].ID != likedVids[1] {
		t.Fatalf("page1 order = [%d,%d], want [%d,%d]",
			page1.Videos[0].ID, page1.Videos[1].ID, likedVids[2], likedVids[1])
	}

	page2, err := svc.ListLikedVideos(ctx, targetUserID, 0, *page1.NextCursor, 2)
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if page2.Total != 3 {
		t.Fatalf("page2 total = %d, want 3", page2.Total)
	}
	if len(page2.Videos) != 1 {
		t.Fatalf("page2 videos = %d, want 1", len(page2.Videos))
	}
	if page2.Videos[0].ID != likedVids[0] {
		t.Fatalf("page2 video = %d, want %d", page2.Videos[0].ID, likedVids[0])
	}
	if page2.NextCursor != nil {
		t.Fatalf("page2 next cursor should be nil")
	}
}

// TestBatchGetLikeCount_FallsBackToDBOnCacheMiss 覆盖两种情况：
//  1. ZSet 不存在（冷视频）时回源点赞表，避免点赞数恒为 0；
//  2. ZSet 命中时直接使用 ZCard。
func TestBatchGetLikeCount_FallsBackToDBOnCacheMiss(t *testing.T) {
	svc, db, redisCache, _, _ := setupLikeService(t)
	ctx := context.Background()

	userID := uint(51)
	video := mustCreateLikeTestVideo(t, db, 99)
	if err := repo.NewLikeRepo(db).CreateLike(ctx, userID, video.ID); err != nil {
		t.Fatalf("seed like: %v", err)
	}
	vidKey := strconv.FormatUint(uint64(video.ID), 10)

	counts, err := svc.BatchGetLikeCount(ctx, []string{vidKey})
	if err != nil {
		t.Fatalf("batch count on cache miss: %v", err)
	}
	if counts[vidKey] != 1 {
		t.Fatalf("cache miss count = %d, want 1", counts[vidKey])
	}

	// 命中 ZSet 时以 ZCard 为准。
	if err := redisCache.AddToSet(ctx, cache.LikeUserSetKey(userID), video.ID, cache.LikeUserSetTTL); err != nil {
		t.Fatalf("seed user like set: %v", err)
	}
	if err := redisCache.ZAdd(ctx, cache.LikeVideoZSetKey(video.ID), []redis.Z{{Score: 1, Member: userID}, {Score: 2, Member: userID + 1}}, cache.LikeVideoZSetTTL); err != nil {
		t.Fatalf("seed like zset: %v", err)
	}
	counts, err = svc.BatchGetLikeCount(ctx, []string{vidKey})
	if err != nil {
		t.Fatalf("batch count on cache hit: %v", err)
	}
	if counts[vidKey] != 2 {
		t.Fatalf("cache hit count = %d, want 2", counts[vidKey])
	}
}

// TestBatchGetIsLike_CacheDisabledFallsBackToDB 覆盖 Redis 禁用时的降级分支：
// 必须回源点赞表，而不是把所有视频都判为未点赞。
func TestBatchGetIsLike_CacheDisabledFallsBackToDB(t *testing.T) {
	svc, db, redisCache, _, _ := setupLikeService(t)
	ctx := context.Background()

	userID := uint(61)
	video := mustCreateLikeTestVideo(t, db, 99)
	if err := repo.NewLikeRepo(db).CreateLike(ctx, userID, video.ID); err != nil {
		t.Fatalf("seed like: %v", err)
	}

	redisCache.Disable()
	vidKey := strconv.FormatUint(uint64(video.ID), 10)
	liked, err := svc.BatchGetIsLike(ctx, userID, []string{vidKey})
	if err != nil {
		t.Fatalf("batch is-liked with cache disabled: %v", err)
	}
	if !liked[vidKey] {
		t.Fatalf("cache disabled should fall back to DB and return liked=true")
	}
}

// TestBatchGetIsLike_RebuildsSetOnCacheMiss 覆盖缓存未命中的防击穿逻辑（P0-6）：
// Set 不存在时必须回源数据库，并把已点赞视频重建回 Redis，避免缓存未命中被误判为未点赞。
func TestBatchGetIsLike_RebuildsSetOnCacheMiss(t *testing.T) {
	svc, db, redisCache, mr, _ := setupLikeService(t)
	ctx := context.Background()

	userID := uint(81)
	likedVideo := mustCreateLikeTestVideo(t, db, 99)
	unlikedVideo := mustCreateLikeTestVideo(t, db, 99)
	if err := repo.NewLikeRepo(db).CreateLike(ctx, userID, likedVideo.ID); err != nil {
		t.Fatalf("seed like: %v", err)
	}

	likedKey := strconv.FormatUint(uint64(likedVideo.ID), 10)
	unlikedKey := strconv.FormatUint(uint64(unlikedVideo.ID), 10)
	liked, err := svc.BatchGetIsLike(ctx, userID, []string{likedKey, unlikedKey})
	if err != nil {
		t.Fatalf("batch is-liked on cache miss: %v", err)
	}
	if !liked[likedKey] {
		t.Fatalf("video %s should be liked (cache miss must fall back to DB)", likedKey)
	}
	if liked[unlikedKey] {
		t.Fatalf("video %s should not be liked", unlikedKey)
	}

	// 重建后 Set 应存在且包含已点赞视频，后续查询直接命中缓存
	if !mr.Exists(cache.LikeUserSetKey(userID)) {
		t.Fatalf("user like set should be rebuilt after cache miss")
	}
	member, err := redisCache.IsMemberOfSet(ctx, cache.LikeUserSetKey(userID), likedVideo.ID)
	if err != nil || !member {
		t.Fatalf("rebuilt like set member = %v, err = %v, want true", member, err)
	}
}

// TestLikeVideo_RejectsMissingVideo 验证视频不存在时不会产生孤儿点赞记录。
func TestLikeVideo_RejectsMissingVideo(t *testing.T) {
	svc, db, _, _, _ := setupLikeService(t)
	ctx := context.Background()

	err := svc.LikeVideo(ctx, 71, 999999, "user71")
	if err == nil || !strings.Contains(err.Error(), "video 999999 not found") {
		t.Fatalf("like missing video error = %v, want video not found", err)
	}

	var rows int64
	if err := db.Model(&model.Like{}).Count(&rows).Error; err != nil {
		t.Fatalf("count likes: %v", err)
	}
	if rows != 0 {
		t.Fatalf("like rows = %d, want 0", rows)
	}
}

// TestLikeVideo_RejectsInvalidIDs 验证零值参数在进入任何外部依赖前被拒绝。
func TestLikeVideo_RejectsInvalidIDs(t *testing.T) {
	svc, _, _, _, _ := setupLikeService(t)
	ctx := context.Background()

	if err := svc.LikeVideo(ctx, 0, 1, "u"); err == nil {
		t.Fatalf("like with userID=0 should fail")
	}
	if err := svc.LikeVideo(ctx, 1, 0, "u"); err == nil {
		t.Fatalf("like with videoID=0 should fail")
	}
	if err := svc.UnlikeVideo(ctx, 0, 1); err == nil {
		t.Fatalf("unlike with userID=0 should fail")
	}
	if err := svc.UnlikeVideo(ctx, 1, 0); err == nil {
		t.Fatalf("unlike with videoID=0 should fail")
	}
}
