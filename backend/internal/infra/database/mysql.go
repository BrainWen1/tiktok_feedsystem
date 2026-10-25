package database

// MySQL 数据库的创建、连接、自动迁移、关闭等操作

import (
	"fmt"
	"log"
	"time"

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
		log.Fatalf("failed to connect database: %v", err)
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
		log.Fatalf("failed to auto migrate: %v", err)
	}
}

func CloseDB() {
	// 连接未初始化时直接返回，避免对 nil 全局变量调用 DB() 导致 panic
	if db == nil {
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get sqlDB: %v", err)
	}
	err = sqlDB.Close()
	if err != nil {
		log.Fatalf("failed to close database: %v", err)
	}
}
