package service

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
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

// setupSocialService 组装关注模块测试环境：
//   - SQLite 内存库替代 MySQL；
//   - miniredis 替代 Redis；
//   - NotificationService 用真实实现，Follow 产生的通知会异步落库，用例结束前会等待其完成。
func setupSocialService(t *testing.T) (*SocialService, *gorm.DB, *cache.RedisCache, *miniredis.Miniredis) {
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
	// 单连接让 SQLite 写操作串行化，同时保证并发用例可稳定复现唯一键冲突。
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.User{}, &model.Social{}, &model.Notification{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redisCache := cache.NewRedisCache(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = redisCache.Close() })

	userService := NewUserService(repo.NewUserRepo(db), redisCache)
	notificationService := NewNotificationService(
		redisCache,
		repo.NewNotificationRepo(db),
		repo.NewVideoRepo(db),
		repo.NewCommentRepo(db),
	)
	svc := NewSocialService(repo.NewSocialRepo(db), redisCache, userService, notificationService)
	return svc, db, redisCache, mr
}

func mustCreateSocialUser(t *testing.T, db *gorm.DB, username string) *model.User {
	t.Helper()
	user := &model.User{UserName: username, Password: "not-used-in-social-tests"}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return user
}

func mustInsertSocial(t *testing.T, db *gorm.DB, followerID, bloggerID uint, createdAt time.Time) {
	t.Helper()
	social := &model.Social{FollowerID: followerID, BloggerID: bloggerID, CreatedAt: createdAt}
	if err := db.Create(social).Error; err != nil {
		t.Fatalf("insert social %d->%d: %v", followerID, bloggerID, err)
	}
}

func countSocialRows(t *testing.T, db *gorm.DB, followerID, bloggerID uint) int64 {
	t.Helper()
	var rows int64
	if err := db.Model(&model.Social{}).
		Where("follower_id = ? AND blogger_id = ?", followerID, bloggerID).
		Count(&rows).Error; err != nil {
		t.Fatalf("count social rows: %v", err)
	}
	return rows
}

// waitForSocialCacheKey 等待异步回写完成，避免用例在 goroutine 落缓存之前就断言。
func waitForSocialCacheKey(t *testing.T, redisCache *cache.RedisCache, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok, err := redisCache.Exists(context.Background(), key); err == nil && ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cache key %q not written in time", key)
}

// waitForFollowNotification 等待 Follow 的异步通知落库，用例结束时不再有后台 goroutine 访问连接。
func waitForFollowNotification(t *testing.T, db *gorm.DB, receiverID uint) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Model(&model.Notification{}).Where("receiver_id = ?", receiverID).Count(&count).Error; err == nil && count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("follow notification for receiver %d not created in time", receiverID)
}

func socialCacheKey(prefix string, userID uint) string {
	return fmt.Sprintf("%s%d", prefix, userID)
}

// TestFollow_WritesDBAndBothCaches 验证关注成功后的落库、双向 ZSet 与计数。
func TestFollow_WritesDBAndBothCaches(t *testing.T) {
	svc, db, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")

	// Follow 只对已存在的缓存 key 做单成员增量写入，先通过 GetCounts 建立空集合占位。
	if _, _, err := svc.GetCounts(ctx, alice.ID); err != nil {
		t.Fatalf("init alice cache: %v", err)
	}
	if _, _, err := svc.GetCounts(ctx, bob.ID); err != nil {
		t.Fatalf("init bob cache: %v", err)
	}

	if err := svc.Follow(ctx, alice.ID, bob.ID, alice.UserName); err != nil {
		t.Fatalf("follow: %v", err)
	}
	waitForFollowNotification(t, db, bob.ID)

	if got := countSocialRows(t, db, alice.ID, bob.ID); got != 1 {
		t.Fatalf("social rows = %d, want 1", got)
	}

	ok, err := redisCache.ZIsMember(ctx, socialCacheKey(socialBloggersKey, alice.ID), bob.ID)
	if err != nil || !ok {
		t.Fatalf("bloggers zset member = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = redisCache.ZIsMember(ctx, socialCacheKey(socialFollowersKey, bob.ID), alice.ID)
	if err != nil || !ok {
		t.Fatalf("followers zset member = (%v, %v), want (true, nil)", ok, err)
	}

	followCount, fanCount, err := svc.GetCounts(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get alice counts: %v", err)
	}
	if followCount != 1 || fanCount != 0 {
		t.Fatalf("alice counts = (%d,%d), want (1,0)", followCount, fanCount)
	}

	followCount, fanCount, err = svc.GetCounts(ctx, bob.ID)
	if err != nil {
		t.Fatalf("get bob counts: %v", err)
	}
	if followCount != 0 || fanCount != 1 {
		t.Fatalf("bob counts = (%d,%d), want (0,1)", followCount, fanCount)
	}
}

// TestFollow_ValidationAndDuplicate 验证自关注、零 ID 与重复关注（缓存命中/缓存丢失两条路径）。
func TestFollow_ValidationAndDuplicate(t *testing.T) {
	svc, db, _, mr := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")

	if err := svc.Follow(ctx, 0, bob.ID, ""); err == nil {
		t.Fatal("follow with zero user id should fail")
	}
	if err := svc.Follow(ctx, alice.ID, 0, alice.UserName); err == nil {
		t.Fatal("follow with zero blogger id should fail")
	}
	if err := svc.Follow(ctx, alice.ID, alice.ID, alice.UserName); err == nil {
		t.Fatal("self follow should fail")
	}

	if err := svc.Follow(ctx, alice.ID, bob.ID, alice.UserName); err != nil {
		t.Fatalf("first follow: %v", err)
	}
	waitForFollowNotification(t, db, bob.ID)

	// 重复关注是幂等成功，不应报错也不应重复落库。
	if err := svc.Follow(ctx, alice.ID, bob.ID, alice.UserName); err != nil {
		t.Fatalf("duplicate follow via cache should be idempotent: %v", err)
	}
	if got := countSocialRows(t, db, alice.ID, bob.ID); got != 1 {
		t.Fatalf("social rows after duplicate follow = %d, want 1", got)
	}

	// 清空 Redis，模拟缓存过期后重复关注：依赖唯一索引兜底，仍然幂等。
	mr.FlushAll()
	if err := svc.Follow(ctx, alice.ID, bob.ID, alice.UserName); err != nil {
		t.Fatalf("duplicate follow via db should be idempotent: %v", err)
	}
	if got := countSocialRows(t, db, alice.ID, bob.ID); got != 1 {
		t.Fatalf("social rows after cache-loss duplicate follow = %d, want 1", got)
	}
}

// TestFollow_TargetUserNotFound 验证关注前会校验目标用户存在，不写入脏关注关系。
func TestFollow_TargetUserNotFound(t *testing.T) {
	svc, db, _, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")

	if err := svc.Follow(ctx, alice.ID, 999999, alice.UserName); err == nil {
		t.Fatal("follow nonexistent target should fail")
	}
	if got := countSocialRows(t, db, alice.ID, 999999); got != 0 {
		t.Fatalf("social rows for nonexistent target = %d, want 0", got)
	}
}

// TestGetLists_FillIsFollowBatch 验证列表 IsFollow 字段使用批量 IN 查询后结果仍然正确：
// 自己的关注列表全部已关注、自己的粉丝列表判断回关、他人粉丝列表判断当前登录用户是否关注。
func TestGetLists_FillIsFollowBatch(t *testing.T) {
	svc, db, _, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")
	carol := mustCreateSocialUser(t, db, "carol")

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	mustInsertSocial(t, db, alice.ID, bob.ID, base)
	mustInsertSocial(t, db, alice.ID, carol.ID, base.Add(time.Minute))
	mustInsertSocial(t, db, bob.ID, alice.ID, base.Add(2*time.Minute)) // bob 是 alice 的粉丝
	mustInsertSocial(t, db, carol.ID, bob.ID, base.Add(3*time.Minute)) // carol 关注 bob

	// 自己的关注列表：全部为已关注
	bloggers, _, _, err := svc.GetBloggers(ctx, alice.ID, alice.ID, dto.FeedCursor{}, 10)
	if err != nil {
		t.Fatalf("get own bloggers: %v", err)
	}
	if len(bloggers) != 2 {
		t.Fatalf("own bloggers len = %d, want 2", len(bloggers))
	}
	for _, user := range bloggers {
		if !user.IsFollow {
			t.Fatalf("own bloggers user %d IsFollow = false, want true", user.UserID)
		}
	}

	// 自己的粉丝列表：bob 已关注 alice（回关）
	fans, _, _, err := svc.GetFollowers(ctx, alice.ID, alice.ID, dto.FeedCursor{}, 10)
	if err != nil {
		t.Fatalf("get own followers: %v", err)
	}
	if len(fans) != 1 || fans[0].UserID != bob.ID || !fans[0].IsFollow {
		t.Fatalf("own followers = %+v, want [bob IsFollow=true]", fans)
	}

	// 他人粉丝列表：carol 关注 bob，查看 alice 的粉丝（bob）时 IsFollow 应为 true
	fans, _, _, err = svc.GetFollowers(ctx, alice.ID, carol.ID, dto.FeedCursor{}, 10)
	if err != nil {
		t.Fatalf("get others followers: %v", err)
	}
	if len(fans) != 1 || fans[0].UserID != bob.ID || !fans[0].IsFollow {
		t.Fatalf("others followers = %+v, want [bob IsFollow=true]", fans)
	}
}

// TestUnfollow_RemovesDBAndBothCaches 验证取关的 DB 删除、双向缓存删除与空集合占位。
func TestUnfollow_RemovesDBAndBothCaches(t *testing.T) {
	svc, db, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")

	if err := svc.Follow(ctx, alice.ID, bob.ID, alice.UserName); err != nil {
		t.Fatalf("follow: %v", err)
	}
	waitForFollowNotification(t, db, bob.ID)

	if err := svc.Unfollow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	if got := countSocialRows(t, db, alice.ID, bob.ID); got != 0 {
		t.Fatalf("social rows after unfollow = %d, want 0", got)
	}

	ok, err := redisCache.ZIsMember(ctx, socialCacheKey(socialBloggersKey, alice.ID), bob.ID)
	if err != nil || ok {
		t.Fatalf("bloggers zset member after unfollow = (%v, %v), want (false, nil)", ok, err)
	}
	ok, err = redisCache.ZIsMember(ctx, socialCacheKey(socialFollowersKey, bob.ID), alice.ID)
	if err != nil || ok {
		t.Fatalf("followers zset member after unfollow = (%v, %v), want (false, nil)", ok, err)
	}

	followCount, fanCount, err := svc.GetCounts(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get counts after unfollow: %v", err)
	}
	if followCount != 0 || fanCount != 0 {
		t.Fatalf("counts after unfollow = (%d,%d), want (0,0)", followCount, fanCount)
	}
	// 0 关注会写入占位成员，后续请求不再穿透数据库。
	exists, err := redisCache.Exists(ctx, socialCacheKey(socialBloggersKey, alice.ID))
	if err != nil || !exists {
		t.Fatalf("empty followers cache placeholder exists = (%v, %v), want (true, nil)", exists, err)
	}

	if err := svc.Unfollow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatalf("repeated unfollow should be idempotent: %v", err)
	}
}

// TestGetCounts_RebuildsFullCacheOnMiss 验证 Redis 丢失后计数从 MySQL 回源并重建完整 ZSet。
func TestGetCounts_RebuildsFullCacheOnMiss(t *testing.T) {
	svc, db, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")
	carol := mustCreateSocialUser(t, db, "carol")

	base := time.Now().Add(-2 * time.Hour)
	mustInsertSocial(t, db, alice.ID, bob.ID, base)
	mustInsertSocial(t, db, alice.ID, carol.ID, base.Add(time.Minute))
	mustInsertSocial(t, db, bob.ID, alice.ID, base.Add(2*time.Minute))

	followCount, fanCount, err := svc.GetCounts(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get counts: %v", err)
	}
	if followCount != 2 || fanCount != 1 {
		t.Fatalf("counts = (%d,%d), want (2,1)", followCount, fanCount)
	}

	zcard, err := redisCache.ZCard(ctx, socialCacheKey(socialBloggersKey, alice.ID))
	if err != nil || zcard != 2 {
		t.Fatalf("bloggers zcard = (%d, %v), want (2, nil)", zcard, err)
	}
	zcard, err = redisCache.ZCard(ctx, socialCacheKey(socialFollowersKey, alice.ID))
	if err != nil || zcard != 1 {
		t.Fatalf("followers zcard = (%d, %v), want (1, nil)", zcard, err)
	}

	// 第二次读取应命中缓存，结果保持一致。
	followCount, fanCount, err = svc.GetCounts(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get counts second time: %v", err)
	}
	if followCount != 2 || fanCount != 1 {
		t.Fatalf("cached counts = (%d,%d), want (2,1)", followCount, fanCount)
	}
}

// TestGetBloggers_PaginatesAcrossDBAndCache 验证第一页走 DB、第二页走 Redis ZSet 时游标同源、不重不漏。
func TestGetBloggers_PaginatesAcrossDBAndCache(t *testing.T) {
	svc, db, _, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")
	carol := mustCreateSocialUser(t, db, "carol")
	dave := mustCreateSocialUser(t, db, "dave")

	base := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	mustInsertSocial(t, db, alice.ID, bob.ID, base)
	mustInsertSocial(t, db, alice.ID, carol.ID, base.Add(time.Hour))
	mustInsertSocial(t, db, alice.ID, dave.ID, base.Add(2*time.Hour))

	page1, total, cursor, err := svc.GetBloggers(ctx, alice.ID, 0, dto.FeedCursor{}, 2)
	if err != nil {
		t.Fatalf("get bloggers page1: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(page1) != 2 || page1[0].UserID != dave.ID || page1[1].UserID != carol.ID {
		t.Fatalf("page1 = %+v, want [dave, carol]", page1)
	}
	if cursor == nil {
		t.Fatal("page1 cursor is nil, want non-nil")
	}
	if cursor.LastUserID != strconv.FormatUint(uint64(carol.ID), 10) {
		t.Fatalf("cursor last uid = %q, want %d", cursor.LastUserID, carol.ID)
	}

	// 列表未命中不再全量回写缓存；用 GetCounts 的全量回源重建 ZSet，确保第二页走缓存路径。
	if _, _, err := svc.GetCounts(ctx, alice.ID); err != nil {
		t.Fatalf("build bloggers cache: %v", err)
	}
	waitForSocialCacheKey(t, svc.cache, socialCacheKey(socialBloggersKey, alice.ID))

	page2, total, cursor, err := svc.GetBloggers(ctx, alice.ID, 0, *cursor, 2)
	if err != nil {
		t.Fatalf("get bloggers page2: %v", err)
	}
	if total != 3 {
		t.Fatalf("page2 total = %d, want 3", total)
	}
	if len(page2) != 1 || page2[0].UserID != bob.ID {
		t.Fatalf("page2 = %+v, want [bob]", page2)
	}
	if cursor != nil {
		t.Fatalf("page2 cursor = %+v, want nil", cursor)
	}
}

// TestSocialCachePageByCursor 验证 ZSet 复合游标分页：同分成员按 member 倒序，跨分数续页。
func TestSocialCachePageByCursor(t *testing.T) {
	svc, _, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	key := "social:test:cache:page"

	err := redisCache.ZAdd(ctx, key, []redis.Z{
		{Score: 100, Member: "4"},
		{Score: 100, Member: "3"},
		{Score: 90, Member: "2"},
		{Score: 90, Member: "1"},
	}, time.Hour)
	if err != nil {
		t.Fatalf("seed zset: %v", err)
	}

	page1, err := svc.socialCachePageByCursor(ctx, key, time.Time{}, 0, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if !reflect.DeepEqual(page1, []string{"4", "3"}) {
		t.Fatalf("page1 = %v, want [4 3]", page1)
	}

	page2, err := svc.socialCachePageByCursor(ctx, key, time.Unix(100, 0), 3, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if !reflect.DeepEqual(page2, []string{"2", "1"}) {
		t.Fatalf("page2 = %v, want [2 1]", page2)
	}

	// 游标正好是最后一页最后一条时，下一页为空。
	page3, err := svc.socialCachePageByCursor(ctx, key, time.Unix(90, 0), 1, 2)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 0 {
		t.Fatalf("page3 = %v, want empty", page3)
	}
}

// TestSocialPageByCursor_SameSecondOrdering 验证 DB 侧同秒记录按 userID 数值倒序，并与游标排除逻辑一致。
func TestSocialPageByCursor_SameSecondOrdering(t *testing.T) {
	at := time.Unix(1700000000, 500*int64(time.Millisecond))
	socials := []*model.Social{
		{BloggerID: 1, CreatedAt: at},
		{BloggerID: 3, CreatedAt: at},
		{BloggerID: 2, CreatedAt: at},
	}
	userIDOf := func(s *model.Social) uint { return s.BloggerID }

	page := socialPageByCursor(socials, at, 3, 2, userIDOf)
	if len(page) != 2 || page[0].BloggerID != 2 || page[1].BloggerID != 1 {
		t.Fatalf("page = %v, want [2 1]", []uint{page[0].BloggerID, page[1].BloggerID})
	}

	cursor := socialCursorFromDBPage(page, 2, userIDOf)
	if cursor == nil {
		t.Fatal("cursor is nil, want non-nil")
	}
	if cursor.LastUserID != "1" {
		t.Fatalf("cursor last uid = %q, want 1", cursor.LastUserID)
	}
	if cursor.LastCreatedAt.Unix() != at.Unix() {
		t.Fatalf("cursor last created at = %d, want %d", cursor.LastCreatedAt.Unix(), at.Unix())
	}
	if socialCursorFromDBPage(page[:1], 2, userIDOf) != nil {
		t.Fatal("partial page should not produce a cursor")
	}
}

// TestReadSocialCacheCount_Placeholder 验证空集合占位成员不会被计入数量。
func TestReadSocialCacheCount_Placeholder(t *testing.T) {
	svc, _, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	key := "social:test:count"

	if _, known, err := svc.readSocialCacheCount(ctx, key); err != nil || known {
		t.Fatalf("missing key = (known=%v, err=%v), want (false, nil)", known, err)
	}

	if err := redisCache.ZAdd(ctx, key, []redis.Z{
		{Score: 1, Member: "5"},
		{Score: 2, Member: "6"},
	}, time.Hour); err != nil {
		t.Fatalf("seed zset: %v", err)
	}
	count, known, err := svc.readSocialCacheCount(ctx, key)
	if err != nil || !known || count != 2 {
		t.Fatalf("real members = (count=%d, known=%v, err=%v), want (2,true,nil)", count, known, err)
	}

	if err := redisCache.Delete(ctx, key); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if err := redisCache.ZAdd(ctx, key, []redis.Z{{Score: 0, Member: socialEmptyZSetMember}}, time.Hour); err != nil {
		t.Fatalf("seed placeholder: %v", err)
	}
	count, known, err = svc.readSocialCacheCount(ctx, key)
	if err != nil || !known || count != 0 {
		t.Fatalf("placeholder = (count=%d, known=%v, err=%v), want (0,true,nil)", count, known, err)
	}
}

// TestFilterSocialMembers 验证占位成员不会出现在响应中。
func TestFilterSocialMembers(t *testing.T) {
	got := filterSocialMembers([]string{"0", "11", "0", "12"})
	if !reflect.DeepEqual(got, []string{"11", "12"}) {
		t.Fatalf("filterSocialMembers = %v, want [11 12]", got)
	}
	if got := filterSocialMembers(nil); got != nil {
		t.Fatalf("filterSocialMembers(nil) = %v, want nil", got)
	}
}

// TestStrSlice2UintSlice 验证缓存成员解析：非法成员显式报错而不是静默变成 0。
func TestStrSlice2UintSlice(t *testing.T) {
	got, err := strSlice2UintSlice([]string{"1", "2"})
	if err != nil || !reflect.DeepEqual(got, []uint{1, 2}) {
		t.Fatalf("valid parse = (%v, %v), want ([1 2], nil)", got, err)
	}
	if _, err := strSlice2UintSlice([]string{"x"}); err == nil {
		t.Fatal("invalid member should return error")
	}
	empty, err := strSlice2UintSlice(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty parse = (%v, %v), want ([], nil)", empty, err)
	}
}

// TestBatchIsFollow_RebuildsCacheOnMiss 验证关注集合整体丢失时全量回源重建，而不是只写命中项。
func TestBatchIsFollow_RebuildsCacheOnMiss(t *testing.T) {
	svc, db, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")
	carol := mustCreateSocialUser(t, db, "carol")
	dave := mustCreateSocialUser(t, db, "dave")

	mustInsertSocial(t, db, alice.ID, bob.ID, time.Now().Add(-time.Hour))
	mustInsertSocial(t, db, alice.ID, carol.ID, time.Now().Add(-time.Minute))

	got, err := svc.BatchIsFollow(ctx, alice.ID, []uint{bob.ID, carol.ID, dave.ID})
	if err != nil {
		t.Fatalf("batch is follow: %v", err)
	}
	want := map[uint]bool{bob.ID: true, carol.ID: true, dave.ID: false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batch result = %v, want %v", got, want)
	}

	waitForSocialCacheKey(t, redisCache, socialCacheKey(socialBloggersKey, alice.ID))
	zcard, err := redisCache.ZCard(ctx, socialCacheKey(socialBloggersKey, alice.ID))
	if err != nil || zcard != 2 {
		t.Fatalf("rebuilt zcard = (%d, %v), want (2, nil)", zcard, err)
	}

	// 缓存已存在时走 ZIsMember 分支。
	got, err = svc.BatchIsFollow(ctx, alice.ID, []uint{bob.ID})
	if err != nil || !got[bob.ID] {
		t.Fatalf("cached batch result = (%v, %v), want true", got, err)
	}

	empty, err := svc.BatchIsFollow(ctx, 0, []uint{bob.ID})
	if err != nil || len(empty) != 0 {
		t.Fatalf("zero login = (%v, %v), want empty map", empty, err)
	}
}

// TestIsFollow_Bidirectional 验证双向关注关系查询与零值校验。
func TestIsFollow_Bidirectional(t *testing.T) {
	svc, db, _, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")

	mustInsertSocial(t, db, alice.ID, bob.ID, time.Now())

	isFollow, isFan, err := svc.IsFollow(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("is follow: %v", err)
	}
	if !isFollow || isFan {
		t.Fatalf("relation = (follow=%v, fan=%v), want (true,false)", isFollow, isFan)
	}

	// 反向关注后再次查询：两个方向都应为 true。
	mustInsertSocial(t, db, bob.ID, alice.ID, time.Now())
	isFollow, isFan, err = svc.IsFollow(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("is follow after mutual: %v", err)
	}
	if !isFollow || !isFan {
		t.Fatalf("mutual relation = (follow=%v, fan=%v), want (true,true)", isFollow, isFan)
	}

	if _, _, err := svc.IsFollow(ctx, alice.ID, alice.ID); err == nil {
		t.Fatal("self relation should fail")
	}
	if _, _, err := svc.IsFollow(ctx, 0, bob.ID); err == nil {
		t.Fatal("zero user relation should fail")
	}
}

// TestFollowConcurrent_SamePair 验证并发关注同一人时：
// 唯一索引保证 DB 只有一行，重复请求幂等成功，Redis 计数不会重复累加。
func TestFollowConcurrent_SamePair(t *testing.T) {
	svc, db, redisCache, _ := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.Follow(ctx, alice.ID, bob.ID, alice.UserName)
		}(i)
	}
	wg.Wait()
	waitForFollowNotification(t, db, bob.ID)

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 2 {
		t.Fatalf("idempotent follows = %d, want 2 (errs=%v)", success, errs)
	}
	if got := countSocialRows(t, db, alice.ID, bob.ID); got != 1 {
		t.Fatalf("social rows = %d, want 1", got)
	}

	followCount, fanCount, err := svc.GetCounts(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get alice counts: %v", err)
	}
	if followCount != 1 || fanCount != 0 {
		t.Fatalf("alice counts = (%d,%d), want (1,0)", followCount, fanCount)
	}

	// 主动触发一次全量重建，确认 DB 只有一行时粉丝集合也只有 1 个成员（没有重复累加）。
	if _, _, err := svc.GetCounts(ctx, bob.ID); err != nil {
		t.Fatalf("get bob counts: %v", err)
	}
	zcard, err := redisCache.ZCard(ctx, socialCacheKey(socialFollowersKey, bob.ID))
	if err != nil || zcard != 1 {
		t.Fatalf("bob followers zcard = (%d, %v), want (1, nil)", zcard, err)
	}
}

// TestFollow_AfterCacheLoss_Regression 是 P0 缺陷的回归用例：
// Redis 中关注集合过期后，Follow 不得只 ZAdd 单个成员，否则读路径会把不完整的 ZSet 当成全量。
// 修复后 Follow 会跳过缺失 key 的单成员写入，由 GetCounts 全量回源重建。
func TestFollow_AfterCacheLoss_Regression(t *testing.T) {

	svc, db, _, mr := setupSocialService(t)
	ctx := context.Background()
	alice := mustCreateSocialUser(t, db, "alice")
	bob := mustCreateSocialUser(t, db, "bob")
	carol := mustCreateSocialUser(t, db, "carol")
	dave := mustCreateSocialUser(t, db, "dave")

	mustInsertSocial(t, db, alice.ID, bob.ID, time.Now().Add(-2*time.Hour))
	mustInsertSocial(t, db, alice.ID, carol.ID, time.Now().Add(-time.Hour))
	mr.FlushAll()

	if err := svc.Follow(ctx, alice.ID, dave.ID, alice.UserName); err != nil {
		t.Fatalf("follow after cache loss: %v", err)
	}
	waitForFollowNotification(t, db, dave.ID)

	followCount, _, err := svc.GetCounts(ctx, alice.ID)
	if err != nil {
		t.Fatalf("get counts: %v", err)
	}
	if followCount != 3 {
		t.Fatalf("follow count after cache loss = %d, want 3", followCount)
	}
}
