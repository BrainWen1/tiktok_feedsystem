package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"feedsystem/internal/dto"
	"feedsystem/internal/infra/cache"
	"feedsystem/internal/model"
	"feedsystem/internal/repo"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupFeedService 组装 feed 测试环境：
//   - SQLite 内存库替代 MySQL（单连接，避免并发写锁冲突）；
//   - miniredis 替代 Redis，覆盖点赞/关注/评论计数的缓存读写路径；
//   - MQ、通知依赖注入 nil：feed 只读不写，不会触达这些依赖。
func setupFeedService(t *testing.T) (*FeedService, *gorm.DB, *cache.RedisCache, *miniredis.Miniredis) {
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
		&model.VideoTag{},
		&model.Like{},
		&model.Comment{},
		&model.Social{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	userService := NewUserService(repo.NewUserRepo(db), redisCache)
	likeService := NewLikeService(repo.NewLikeRepo(db), nil, userService, nil, redisCache)
	commentService := NewCommentService(repo.NewCommentRepo(db), nil, redisCache, nil, nil, nil)
	socialService := NewSocialService(repo.NewSocialRepo(db), redisCache, userService, nil)
	feedService := NewFeedService(repo.NewFeedRepo(db), likeService, socialService, commentService, userService)

	return feedService, db, redisCache, mr
}

func feedTestUser(t *testing.T, db *gorm.DB, username string) *model.User {
	t.Helper()
	user := &model.User{
		UserName:  username,
		Password:  "not-used-in-feed-tests",
		AvatarURL: "/static/default_avatar.png",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return user
}

func feedTestVideo(t *testing.T, db *gorm.DB, authorID uint, title string, createdAt time.Time, likesCount, popularity int64) *model.Video {
	t.Helper()
	video := &model.Video{
		AuthorID:    authorID,
		Title:       title,
		Description: "created by feed_service_test.go",
		VideoURL:    fmt.Sprintf("/upload/videos/%d/test.mp4", authorID),
		CoverURL:    fmt.Sprintf("/upload/covers/%d/test.jpg", authorID),
		CreatedAt:   createdAt,
		LikesCount:  likesCount,
		Popularity:  popularity,
	}
	if err := db.Create(video).Error; err != nil {
		t.Fatalf("create video %q: %v", title, err)
	}
	return video
}

// feedSeedLikeCount 直接构造 like:video:{vid} ZSet，让 ZCard 与 videos.likes_count 一致。
// feed 读点赞数走 Redis ZCard，命中后不会再回源点赞表。
func feedSeedLikeCount(t *testing.T, redisCache *cache.RedisCache, vid uint, count int) {
	t.Helper()
	if count == 0 {
		return
	}
	zList := make([]redis.Z, 0, count)
	for i := range count {
		zList = append(zList, redis.Z{Score: float64(i), Member: uint(i + 1)})
	}
	if err := redisCache.ZAdd(context.Background(), cache.LikeVideoZSetKey(vid), zList, cache.LikeVideoZSetTTL); err != nil {
		t.Fatalf("seed like count for video %d: %v", vid, err)
	}
}

// walkFeedPages 按 next_cursor 连续翻页，收集全部条目；最多 50 页，避免实现缺陷造成死循环。
func walkFeedPages(t *testing.T, fetch func(dto.FeedCursor) (dto.FeedListResp, error)) []dto.FeedItemResp {
	t.Helper()
	cursor := dto.FeedCursor{}
	var all []dto.FeedItemResp
	for page := range 50 {
		resp, err := fetch(cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		all = append(all, resp.List...)
		if len(resp.List) == 0 || resp.NextCursor == nil {
			return all
		}
		cursor = *resp.NextCursor
	}
	t.Fatal("feed pagination did not terminate within 50 pages")
	return nil
}

func assertUniqueIDs(t *testing.T, list []dto.FeedItemResp, want int) {
	t.Helper()
	seen := make(map[string]struct{}, len(list))
	for _, item := range list {
		if _, ok := seen[item.Vid]; ok {
			t.Fatalf("duplicate vid %s across pages", item.Vid)
		}
		seen[item.Vid] = struct{}{}
	}
	if len(list) != want {
		t.Fatalf("got %d items, want %d", len(list), want)
	}
}

func assertVidOrder(t *testing.T, list []dto.FeedItemResp, want []string) {
	t.Helper()
	if len(list) != len(want) {
		t.Fatalf("got %d items, want %d", len(list), len(want))
	}
	for i := range want {
		if list[i].Vid != want[i] {
			t.Fatalf("item %d vid = %s, want %s (full order: %v)", i, list[i].Vid, want[i], feedVids(list))
		}
	}
}

func feedVids(list []dto.FeedItemResp) []string {
	vids := make([]string, 0, len(list))
	for _, item := range list {
		vids = append(vids, item.Vid)
	}
	return vids
}

func TestParseFeedCursorVid(t *testing.T) {
	tests := []struct {
		name      string
		lastVid   string
		wantVid   uint
		wantHas   bool
		wantError bool
	}{
		{name: "empty means first page", lastVid: "", wantVid: 0, wantHas: false},
		{name: "valid id", lastVid: "42", wantVid: 42, wantHas: true},
		{name: "non numeric", lastVid: "abc", wantError: true},
		{name: "negative", lastVid: "-1", wantError: true},
		{name: "zero is invalid", lastVid: "0", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vid, has, err := parseFeedCursorVid(tt.lastVid)
			if tt.wantError {
				if err == nil {
					t.Fatalf("parseFeedCursorVid(%q) error = nil, want error", tt.lastVid)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFeedCursorVid(%q): %v", tt.lastVid, err)
			}
			if vid != tt.wantVid || has != tt.wantHas {
				t.Fatalf("parseFeedCursorVid(%q) = (%d,%v), want (%d,%v)", tt.lastVid, vid, has, tt.wantVid, tt.wantHas)
			}
		})
	}
}

func TestParseTimeFeedCursor(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	t.Run("empty cursor", func(t *testing.T) {
		createdAt, vid, has, err := parseTimeFeedCursor(dto.FeedCursor{})
		if err != nil || has || vid != 0 || !createdAt.IsZero() {
			t.Fatalf("empty cursor = (%v,%d,%v,%v), want zero/false/nil", createdAt, vid, has, err)
		}
	})

	t.Run("complete cursor", func(t *testing.T) {
		createdAt, vid, has, err := parseTimeFeedCursor(dto.FeedCursor{
			LastVid:       "7",
			LastCreatedAt: now,
		})
		if err != nil || !has || vid != 7 || !createdAt.Equal(now) {
			t.Fatalf("complete cursor = (%v,%d,%v,%v), want (%v,7,true,nil)", createdAt, vid, has, err, now)
		}
	})

	t.Run("only last_vid", func(t *testing.T) {
		if _, _, _, err := parseTimeFeedCursor(dto.FeedCursor{LastVid: "7"}); err == nil {
			t.Fatal("cursor with only last_vid must be rejected")
		}
	})

	t.Run("only last_created_at", func(t *testing.T) {
		if _, _, _, err := parseTimeFeedCursor(dto.FeedCursor{LastCreatedAt: now}); err == nil {
			t.Fatal("cursor with only last_created_at must be rejected")
		}
	})
}

func TestNormalizeFeedLimit(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{in: 0, want: defaultFeedLimit},
		{in: -1, want: defaultFeedLimit},
		{in: 1, want: 1},
		{in: defaultFeedLimit, want: defaultFeedLimit},
		{in: defaultFeedLimit + 1, want: defaultFeedLimit},
	}
	for _, tt := range tests {
		if got := normalizeFeedLimit(tt.in); got != tt.want {
			t.Fatalf("normalizeFeedLimit(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestFeedListLatestPaginationStableWithTies 覆盖 (created_at, id) 复合游标：
// 同一时间戳的多条视频跨页时必须不重复、不遗漏。
func TestFeedListLatestPaginationStableWithTies(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	ctx := context.Background()
	author := feedTestUser(t, db, "latest_author")
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	v1 := feedTestVideo(t, db, author.ID, "v1", base, 0, 0)
	v2 := feedTestVideo(t, db, author.ID, "v2", base, 0, 0)
	v3 := feedTestVideo(t, db, author.ID, "v3", base, 0, 0)
	v4 := feedTestVideo(t, db, author.ID, "v4", base.Add(-time.Second), 0, 0)
	v5 := feedTestVideo(t, db, author.ID, "v5", base.Add(-2*time.Second), 0, 0)

	// 首页同时校验 next_cursor 由本页最后一条记录生成
	first, err := svc.ListLatest(ctx, 0, dto.FeedCursor{}, 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor == nil {
		t.Fatal("first page must return next_cursor")
	}
	// 首页按 (created_at DESC, id DESC) 排序，同一时间戳时 id 大的在前，因此最后一条是 v2
	if first.NextCursor.LastVid != fmt.Sprintf("%d", v2.ID) {
		t.Fatalf("next_cursor.last_vid = %s, want last item %d", first.NextCursor.LastVid, v2.ID)
	}

	list := walkFeedPages(t, func(cursor dto.FeedCursor) (dto.FeedListResp, error) {
		return svc.ListLatest(ctx, 0, cursor, 2)
	})
	assertUniqueIDs(t, list, 5)
	assertVidOrder(t, list, []string{
		fmt.Sprintf("%d", v3.ID),
		fmt.Sprintf("%d", v2.ID),
		fmt.Sprintf("%d", v1.ID),
		fmt.Sprintf("%d", v4.ID),
		fmt.Sprintf("%d", v5.ID),
	})
}

// TestFeedListLatestGuestAndLogin 覆盖游客与登录用户的聚合差异：
// 游客跳过 is_like/is_follow 批量查询；登录用户能看到真实点赞/关注状态。
func TestFeedListLatestGuestAndLogin(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	ctx := context.Background()
	author := feedTestUser(t, db, "feed_author")
	viewer := feedTestUser(t, db, "feed_viewer")
	video := feedTestVideo(t, db, author.ID, "aggregate", time.Now(), 0, 0)

	if err := db.Create(&model.Like{UserID: viewer.ID, VideoID: video.ID}).Error; err != nil {
		t.Fatalf("create like: %v", err)
	}
	if err := db.Create(&model.Social{FollowerID: viewer.ID, BloggerID: author.ID}).Error; err != nil {
		t.Fatalf("create follow relation: %v", err)
	}
	for i := range 2 {
		if err := db.Create(&model.Comment{
			VideoID:  video.ID,
			UserID:   viewer.ID,
			UserName: viewer.UserName,
			Content:  fmt.Sprintf("comment-%d", i),
		}).Error; err != nil {
			t.Fatalf("create comment %d: %v", i, err)
		}
	}

	guestResp, err := svc.ListLatest(ctx, 0, dto.FeedCursor{}, 20)
	if err != nil {
		t.Fatalf("guest feed: %v", err)
	}
	if len(guestResp.List) != 1 {
		t.Fatalf("guest feed returned %d items, want 1", len(guestResp.List))
	}
	guestItem := guestResp.List[0]
	if guestItem.IsLike || guestItem.IsFollow {
		t.Fatalf("guest flags = (is_like=%v,is_follow=%v), want both false", guestItem.IsLike, guestItem.IsFollow)
	}
	if guestItem.AuthorName != author.UserName {
		t.Fatalf("guest author name = %q, want %q", guestItem.AuthorName, author.UserName)
	}
	if guestItem.LikesCnt != 1 || guestItem.CommentsCnt != 2 {
		t.Fatalf("guest counts = (likes=%d,comments=%d), want (1,2)", guestItem.LikesCnt, guestItem.CommentsCnt)
	}

	loginResp, err := svc.ListLatest(ctx, viewer.ID, dto.FeedCursor{}, 20)
	if err != nil {
		t.Fatalf("login feed: %v", err)
	}
	if len(loginResp.List) != 1 {
		t.Fatalf("login feed returned %d items, want 1", len(loginResp.List))
	}
	loginItem := loginResp.List[0]
	if !loginItem.IsLike || !loginItem.IsFollow {
		t.Fatalf("login flags = (is_like=%v,is_follow=%v), want both true", loginItem.IsLike, loginItem.IsFollow)
	}
}

// TestFeedListByFollowingPagination 覆盖关注流：只返回已关注作者的视频，并验证复合游标翻页。
func TestFeedListByFollowingPagination(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	ctx := context.Background()
	viewer := feedTestUser(t, db, "following_viewer")
	followed := feedTestUser(t, db, "followed_author")
	stranger := feedTestUser(t, db, "stranger_author")

	if err := db.Create(&model.Social{FollowerID: viewer.ID, BloggerID: followed.ID}).Error; err != nil {
		t.Fatalf("create follow relation: %v", err)
	}

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	v1 := feedTestVideo(t, db, followed.ID, "followed-1", base.Add(-time.Second), 0, 0)
	v2 := feedTestVideo(t, db, followed.ID, "followed-2", base.Add(-2*time.Second), 0, 0)
	v3 := feedTestVideo(t, db, followed.ID, "followed-3", base.Add(-3*time.Second), 0, 0)
	// 未关注作者的最新视频不能出现在关注流
	feedTestVideo(t, db, stranger.ID, "stranger-newest", base, 0, 0)

	list := walkFeedPages(t, func(cursor dto.FeedCursor) (dto.FeedListResp, error) {
		return svc.ListByFollowing(ctx, viewer.ID, cursor, 2)
	})
	assertUniqueIDs(t, list, 3)
	assertVidOrder(t, list, []string{
		fmt.Sprintf("%d", v1.ID),
		fmt.Sprintf("%d", v2.ID),
		fmt.Sprintf("%d", v3.ID),
	})
}

// TestFeedListByFollowingWithoutFollows 关注列表为空时应返回空页，而不是报错。
func TestFeedListByFollowingWithoutFollows(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	viewer := feedTestUser(t, db, "no_follow_viewer")

	resp, err := svc.ListByFollowing(context.Background(), viewer.ID, dto.FeedCursor{}, 20)
	if err != nil {
		t.Fatalf("following feed without relations: %v", err)
	}
	if len(resp.List) != 0 {
		t.Fatalf("following feed without relations returned %d items, want 0", len(resp.List))
	}
}

// TestFeedListByTagPagination 覆盖标签过滤与翻页，确保其余标签的视频不会混入。
func TestFeedListByTagPagination(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	ctx := context.Background()
	author := feedTestUser(t, db, "tag_author")
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	goVideo1 := feedTestVideo(t, db, author.ID, "go-1", base, 0, 0)
	goVideo2 := feedTestVideo(t, db, author.ID, "go-2", base.Add(-time.Second), 0, 0)
	rustVideo := feedTestVideo(t, db, author.ID, "rust-1", base.Add(-2*time.Second), 0, 0)

	tags := []model.VideoTag{
		{VideoID: goVideo1.ID, Tag: "go"},
		{VideoID: goVideo1.ID, Tag: "backend"},
		{VideoID: goVideo2.ID, Tag: "go"},
		{VideoID: rustVideo.ID, Tag: "rust"},
	}
	if err := db.Create(&tags).Error; err != nil {
		t.Fatalf("create video tags: %v", err)
	}

	list := walkFeedPages(t, func(cursor dto.FeedCursor) (dto.FeedListResp, error) {
		return svc.ListByTag(ctx, 0, "go", cursor, 1)
	})
	assertUniqueIDs(t, list, 2)
	assertVidOrder(t, list, []string{
		fmt.Sprintf("%d", goVideo1.ID),
		fmt.Sprintf("%d", goVideo2.ID),
	})

	if _, err := svc.ListByTag(ctx, 0, "", dto.FeedCursor{}, 20); err == nil {
		t.Fatal("empty tag must be rejected")
	}
}

// TestFeedListByPopularityPagination 覆盖热度并列时的 id 决胜与翻页稳定性。
func TestFeedListByPopularityPagination(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	ctx := context.Background()
	author := feedTestUser(t, db, "popular_author")

	v1 := feedTestVideo(t, db, author.ID, "p1", time.Now(), 0, 10)
	v2 := feedTestVideo(t, db, author.ID, "p2", time.Now(), 0, 10)
	v3 := feedTestVideo(t, db, author.ID, "p3", time.Now(), 0, 10)
	v4 := feedTestVideo(t, db, author.ID, "p4", time.Now(), 0, 5)
	v5 := feedTestVideo(t, db, author.ID, "p5", time.Now(), 0, 0)

	list := walkFeedPages(t, func(cursor dto.FeedCursor) (dto.FeedListResp, error) {
		return svc.ListByPopularity(ctx, 0, cursor, 2)
	})
	assertUniqueIDs(t, list, 5)
	assertVidOrder(t, list, []string{
		fmt.Sprintf("%d", v3.ID),
		fmt.Sprintf("%d", v2.ID),
		fmt.Sprintf("%d", v1.ID),
		fmt.Sprintf("%d", v4.ID),
		fmt.Sprintf("%d", v5.ID),
	})
}

// TestFeedListByLikesCountPagination 覆盖点赞榜排序与翻页。
// 预先构造与 videos.likes_count 一致的 ZSet，避免 Redis 计数与 DB 排序列不一致干扰断言。
func TestFeedListByLikesCountPagination(t *testing.T) {
	svc, db, redisCache, _ := setupFeedService(t)
	ctx := context.Background()
	author := feedTestUser(t, db, "likes_author")

	v1 := feedTestVideo(t, db, author.ID, "l1", time.Now(), 5, 0)
	v2 := feedTestVideo(t, db, author.ID, "l2", time.Now(), 3, 0)
	v3 := feedTestVideo(t, db, author.ID, "l3", time.Now(), 3, 0)
	v4 := feedTestVideo(t, db, author.ID, "l4", time.Now(), 1, 0)
	feedSeedLikeCount(t, redisCache, v1.ID, 5)
	feedSeedLikeCount(t, redisCache, v2.ID, 3)
	feedSeedLikeCount(t, redisCache, v3.ID, 3)
	feedSeedLikeCount(t, redisCache, v4.ID, 1)

	list := walkFeedPages(t, func(cursor dto.FeedCursor) (dto.FeedListResp, error) {
		return svc.ListByLikesCount(ctx, 0, cursor, 2)
	})
	assertUniqueIDs(t, list, 4)
	assertVidOrder(t, list, []string{
		fmt.Sprintf("%d", v1.ID),
		fmt.Sprintf("%d", v3.ID),
		fmt.Sprintf("%d", v2.ID),
		fmt.Sprintf("%d", v4.ID),
	})
}

// TestFeedListLatestIncludesTags 标签属于 feed 附属信息，必须一次性批量补齐并按发布顺序返回。
func TestFeedListLatestIncludesTags(t *testing.T) {
	svc, db, _, _ := setupFeedService(t)
	ctx := context.Background()
	author := feedTestUser(t, db, "tag_author")
	video := feedTestVideo(t, db, author.ID, "with-tags", time.Now(), 0, 0)

	for _, tag := range []string{"美食", "旅行"} {
		if err := db.Create(&model.VideoTag{VideoID: video.ID, Tag: tag}).Error; err != nil {
			t.Fatalf("create video tag %q: %v", tag, err)
		}
	}

	resp, err := svc.ListLatest(ctx, 0, dto.FeedCursor{}, 10)
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	if len(resp.List) != 1 {
		t.Fatalf("want 1 feed item, got %d", len(resp.List))
	}

	got := resp.List[0].Tags
	if len(got) != 2 || got[0] != "美食" || got[1] != "旅行" {
		t.Fatalf("want tags [美食 旅行], got %v", got)
	}
}

// TestFeedListRejectsInvalidCursor 非法游标必须返回错误，不能静默当作首页。
func TestFeedListRejectsInvalidCursor(t *testing.T) {
	svc, _, _, _ := setupFeedService(t)
	ctx := context.Background()

	if _, err := svc.ListLatest(ctx, 0, dto.FeedCursor{LastVid: "abc", LastCreatedAt: time.Now()}, 20); err == nil {
		t.Fatal("non-numeric last_vid must be rejected")
	}
	if _, err := svc.ListLatest(ctx, 0, dto.FeedCursor{LastVid: "3"}, 20); err == nil {
		t.Fatal("cursor missing last_created_at must be rejected")
	}
	if _, err := svc.ListByPopularity(ctx, 0, dto.FeedCursor{LastVid: "abc"}, 20); err == nil {
		t.Fatal("popularity feed with non-numeric last_vid must be rejected")
	}
}
