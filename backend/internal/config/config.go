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

	MQ_Host     string
	MQ_Port     int
	MQ_Username string
	MQ_Password string

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
		MQ_Host:        os.Getenv("MQ_HOST"),
		MQ_Username:    os.Getenv("MQ_USERNAME"),
		MQ_Password:    os.Getenv("MQ_PASSWORD"),
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

	mqPortStr := os.Getenv("MQ_PORT")
	mqPort := 5672 // 默认5672
	if mqPortStr != "" {
		val, e := strconv.Atoi(mqPortStr)
		if e == nil {
			mqPort = val
		}
	}
	AppConfig.MQ_Port = mqPort

	return nil
}
