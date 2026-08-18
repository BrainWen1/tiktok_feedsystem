package database

// MySQL 数据库的创建、连接、自动迁移、关闭等操作

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var db *gorm.DB // 全局数据库连接对象

func ConnectDB(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true, // 禁用外键约束
	})

	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	return db, nil
}

func AutoMigrate(db *gorm.DB, models ...interface{}) {
	err := db.AutoMigrate(models...)
	if err != nil {
		log.Fatalf("failed to auto migrate: %v", err)
	}
}

func CloseDB() {
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get sqlDB: %v", err)
	}
	err = sqlDB.Close()
	if err != nil {
		log.Fatalf("failed to close database: %v", err)
	}
}
