package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DB_dsn string

	Redis_addr     string
	Redis_password string
	Redis_db       int

	JWT_secret      string
	JWT_expire_hour int
}

var AppConfig Config // 全局配置对象

func LoadConfig() error {
	// 候选路径列表，按顺序尝试，兼容不同执行位置
	candidates := []string{
		"configs/.env.dev",         // 终端在backend/执行
		"backend/configs/.env.dev", // 终端在仓库根目录执行
	}
	var err error
	for _, path := range candidates {
		err = godotenv.Load(path)
		if err != nil {
			log.Fatal("Error loading .env file:", path, err)
			return err
		}
	}

	// string字段
	AppConfig = Config{
		DB_dsn:         os.Getenv("MYSQL_DSN"),
		Redis_addr:     os.Getenv("REDIS_ADDR"),
		Redis_password: os.Getenv("REDIS_PASSWORD"),
		JWT_secret:     os.Getenv("JWT_SECRET"),
	}

	// int字段
	redisDBStr := os.Getenv("REDIS_DB")
	redisDB := 0
	if redisDBStr != "" {
		val, e := strconv.Atoi(redisDBStr)
		if e == nil {
			redisDB = val
		}
	}
	AppConfig.Redis_db = redisDB

	jwtExpStr := os.Getenv("JWT_EXPIRE_HOUR")
	jwtExpHour := 24 // 默认24小时
	if jwtExpStr != "" {
		val, e := strconv.Atoi(jwtExpStr)
		if e == nil {
			jwtExpHour = val
		}
	}
	AppConfig.JWT_expire_hour = jwtExpHour

	return nil
}
