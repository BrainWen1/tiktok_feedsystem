package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"feedsystem/internal/model"

	"github.com/go-sql-driver/mysql"
	"gorm.io/driver/sqlite" // SQLite 驱动依赖 CGO 和 gcc
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestIsDuplicateKeyError(t *testing.T) {
	if !isDuplicateKeyError(&mysql.MySQLError{Number: 1062}) {
		t.Fatal("MySQL 1062 should be treated as duplicate key")
	}
	if isDuplicateKeyError(&mysql.MySQLError{Number: 1048}) {
		t.Fatal("non-duplicate MySQL error must not be treated as duplicate key")
	}
	if isDuplicateKeyError(errors.New("plain error")) {
		t.Fatal("plain error must not be treated as duplicate key")
	}
}

// newUserTestDB 创建每个测试独立的内存 SQLite 数据库。
// 使用 cache=shared + 唯一文件名，避免不同测试之间复用同一份数据；
// MaxOpenConns(1) 保证 :memory: 模式下所有语句落在同一个连接上。
func newUserTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	// 测试会主动触发唯一键冲突/record not found，关闭 GORM 默认 SQL 日志避免噪音。
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

	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("auto migrate user: %v", err)
	}
	return db
}

func TestUserRepoRegisterAndFind(t *testing.T) {
	r := NewUserRepo(newUserTestDB(t))
	ctx := context.Background()

	user := &model.User{
		UserName:  "alice_01",
		Password:  "hashed-password",
		AvatarURL: "/static/default_avatar.png",
		Bio:       "hello",
	}
	if err := r.Register(ctx, user); err != nil {
		t.Fatalf("register: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("register should fill primary key")
	}

	byID, err := r.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if byID.UserName != user.UserName || byID.Password != user.Password {
		t.Fatalf("find by id = %+v, want user %+v", byID, user)
	}

	byName, err := r.FindByUsername(ctx, user.UserName)
	if err != nil {
		t.Fatalf("find by username: %v", err)
	}
	if byName.ID != user.ID {
		t.Fatalf("find by username id = %d, want %d", byName.ID, user.ID)
	}

	// 用户名唯一索引必须生效，否则并发注册会产生重复账号。
	if err := r.Register(ctx, &model.User{UserName: user.UserName, Password: "other"}); err == nil {
		t.Fatal("duplicate username should violate unique index")
	}
}

func TestUserRepoFindByUsernameRejectsInvalidInput(t *testing.T) {
	r := NewUserRepo(newUserTestDB(t))
	ctx := context.Background()

	// 空用户名会让 GORM 忽略空字符串条件，必须由 repo 层前置拦截。
	for _, username := range []string{"", "   ", "\t\n"} {
		if _, err := r.FindByUsername(ctx, username); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("FindByUsername(%q) error = %v, want ErrInvalidArgument", username, err)
		}
	}

	// 查询不存在的用户应保留 gorm.ErrRecordNotFound，供 service 层区分。
	if _, err := r.FindByUsername(ctx, "missing_user"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing user error = %v, want gorm.ErrRecordNotFound", err)
	}

	// 参数化查询：注入串必须被当作普通用户名，不能改变 WHERE 语义。
	if _, err := r.FindByUsername(ctx, "' OR '1'='1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("injection username error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestUserRepoUpdateProfile(t *testing.T) {
	r := NewUserRepo(newUserTestDB(t))
	ctx := context.Background()

	user := &model.User{UserName: "bob_01", Password: "hash", AvatarURL: "/static/default_avatar.png", Bio: "old"}
	if err := r.Register(ctx, user); err != nil {
		t.Fatalf("register: %v", err)
	}

	// 零值主键或空字段集合必须拒绝，避免 GORM 生成全表 UPDATE。
	if err := r.UpdateProfile(ctx, 0, map[string]interface{}{"bio": "x"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero user id error = %v, want ErrInvalidArgument", err)
	}
	if err := r.UpdateProfile(ctx, user.ID, map[string]interface{}{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty fields error = %v, want ErrInvalidArgument", err)
	}

	err := r.UpdateProfile(ctx, user.ID, map[string]interface{}{
		"bio":        "new bio",
		"avatar_url": "/upload/avatars/1/new.png",
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}

	got, err := r.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find updated user: %v", err)
	}
	if got.Bio != "new bio" || got.AvatarURL != "/upload/avatars/1/new.png" {
		t.Fatalf("updated user = %+v", got)
	}
	// map 更新只应影响传入字段，用户名不能被意外清空。
	if got.UserName != user.UserName {
		t.Fatalf("user name changed unexpectedly: %q", got.UserName)
	}
}

func TestUserRepoUpdatePassword(t *testing.T) {
	r := NewUserRepo(newUserTestDB(t))
	ctx := context.Background()

	user := &model.User{UserName: "carol_01", Password: "old-hash"}
	if err := r.Register(ctx, user); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := r.UpdatePassword(ctx, 0, "x"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero user id error = %v, want ErrInvalidArgument", err)
	}
	if err := r.UpdatePassword(ctx, user.ID, "new-hash"); err != nil {
		t.Fatalf("update password: %v", err)
	}

	got, err := r.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find updated user: %v", err)
	}
	if got.Password != "new-hash" {
		t.Fatalf("password = %q, want new-hash", got.Password)
	}
	if got.UserName != user.UserName {
		t.Fatalf("user name changed unexpectedly: %q", got.UserName)
	}
}

func TestUserRepoBatchFindByIDs(t *testing.T) {
	r := NewUserRepo(newUserTestDB(t))
	ctx := context.Background()

	// 空入参直接返回空切片，不产生 SQL。
	users, err := r.BatchFindByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("batch find empty: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("batch find empty len = %d, want 0", len(users))
	}

	u1 := &model.User{UserName: "dave_01", Password: "h1"}
	u2 := &model.User{UserName: "dave_02", Password: "h2"}
	if err := r.Register(ctx, u1); err != nil {
		t.Fatalf("register u1: %v", err)
	}
	if err := r.Register(ctx, u2); err != nil {
		t.Fatalf("register u2: %v", err)
	}

	// 不存在的 ID 应被忽略，返回结果中只包含真实用户。
	users, err = r.BatchFindByIDs(ctx, []uint{u1.ID, u2.ID, u1.ID + 1000})
	if err != nil {
		t.Fatalf("batch find: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("batch find len = %d, want 2", len(users))
	}
}
