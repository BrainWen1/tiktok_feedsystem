package database

// MySQL 数据库的创建、连接、自动迁移、关闭等操作

import (
	"fmt"
	"strings"
	"time"

	"feedsystem/internal/utils/logger"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var db *gorm.DB // 全局数据库连接对象

func ConnectDB(dsn string) (*gorm.DB, error) {
	var err error
	// 直接对包级变量赋值，避免使用 := 产生局部变量遮蔽，保证 CloseDB 能关闭真实连接
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true, // 禁用外键约束
	})

	if err != nil {
		logger.Fatal("failed to connect database", logger.Err(err))
	}

	if err = configConnPool(db); err != nil {
		return nil, err
	}
	return db, nil
}

// configConnPool 配置底层连接池，防止突发流量耗尽 MySQL 连接数。
// feed流、点赞、评论读多写多，需要足够的连接上限；同时限制空闲连接，避免长期占满 max_connections。
func configConnPool(gormDB *gorm.DB) error {
	sqlDB, err := gormDB.DB()
	if err != nil {
		return fmt.Errorf("database: get sql.DB for connection pool config: %w", err)
	}

	// 单实例最大连接数，避免突发流量打满 MySQL 默认 max_connections=151；
	// 多实例部署时按实例数分摊，单实例保持在 100 以内较为安全。
	sqlDB.SetMaxOpenConns(100)

	// 最大空闲连接数，覆盖稳态下的并发量，减少频繁建连握手开销；
	// 约为最大连接数的 20%，空闲过多会长期占用数据库连接资源。
	sqlDB.SetMaxIdleConns(20)

	// 连接最长存活时间，短于 MySQL wait_timeout（默认 8h）和常见负载均衡空闲回收时间，
	// 避免拿到已被服务端或中间层断开的失效连接。
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	// 连接最长空闲时间，长时间空闲的连接会被回收，把连接额度让给其他实例/突发流量。
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	return nil
}

func AutoMigrate(db *gorm.DB, models ...interface{}) {
	err := db.AutoMigrate(models...)
	if err != nil {
		logger.Fatal("failed to auto migrate", logger.Err(err))
	}
}

// MigrateNotificationState 完成通知表从“唯一索引去重”到“状态机 + active_key”的数据迁移。
//
// AutoMigrate 只负责表结构：它会新增 active_key / status 列和 uk_active 索引，
// 但既不会回填历史数据，也不会删除模型中已经去掉的 uk_ignore 索引。
// 因此这里补两步幂等迁移：
//  1. 为历史 active 记录回填 active_key，让唯一约束覆盖存量数据；
//  2. 删除旧的 uk_ignore 唯一索引，否则“取消后再点赞/关注”会报 Duplicate entry。
//
// 主进程和 worker 会并发启动，两步都按幂等实现。
func MigrateNotificationState(db *gorm.DB) {
	if db == nil {
		return
	}
	backfillNotificationActiveKey(db)
	dropLegacyNotificationUniqueIndex(db)
}

// backfillNotificationActiveKey 为历史 active 通知补上业务唯一键。
// 只处理 active_key IS NULL 的 active 记录；同一业务键只回填 id 最大的一条，
// 避免脏数据里存在重复 active 时触发 uk_active 唯一键冲突。
func backfillNotificationActiveKey(db *gorm.DB) {
	var pending int64
	err := db.Raw(`SELECT COUNT(*) FROM (
		SELECT 1 FROM notifications WHERE status = 'active' AND active_key IS NULL LIMIT 1
	) AS pending`).Scan(&pending).Error
	if err != nil {
		logger.Fatal("failed to inspect notifications needing active_key backfill", logger.Err(err))
	}
	if pending == 0 {
		return
	}

	err = db.Exec(`UPDATE notifications n
		JOIN (
			SELECT MAX(id) AS id FROM notifications
			WHERE status = 'active'
			GROUP BY actor_id, receiver_id, type, target_id
		) latest ON latest.id = n.id
		SET n.active_key = CONCAT(n.actor_id, ':', n.receiver_id, ':', n.type, ':', n.target_id)
		WHERE n.active_key IS NULL`).Error
	if err != nil {
		logger.Fatal("failed to backfill notification active_key", logger.Err(err))
	}
	logger.Info("notification active_key backfill finished")
}

// dropLegacyNotificationUniqueIndex 删除通知表历史上用于去重的 uk_ignore 唯一索引。
// 索引不存在时直接跳过；并发启动时后执行的一方会遇到 Error 1091，同样按已删除处理。
func dropLegacyNotificationUniqueIndex(db *gorm.DB) {
	var count int64
	err := db.Raw(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`,
		"notifications", "uk_ignore").Scan(&count).Error
	if err != nil {
		logger.Fatal("failed to inspect legacy index uk_ignore", logger.Err(err))
	}
	if count == 0 {
		return
	}

	if err := db.Exec("DROP INDEX uk_ignore ON notifications").Error; err != nil {
		// 1091 = 索引不存在：另一个进程已经删掉了，按幂等成功处理
		if strings.Contains(err.Error(), "1091") {
			logger.Info("legacy index uk_ignore already dropped by another process")
			return
		}
		logger.Fatal("failed to drop legacy index uk_ignore", logger.Err(err))
	}
	logger.Info("legacy index uk_ignore dropped")
}

func CloseDB() {
	// 连接未初始化时直接返回，避免对 nil 全局变量调用 DB() 导致 panic
	if db == nil {
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		logger.Fatal("failed to get sqlDB", logger.Err(err))
	}
	err = sqlDB.Close()
	if err != nil {
		logger.Fatal("failed to close database", logger.Err(err))
	}
}
