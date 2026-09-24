package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr                string
	MySQLDSN                string
	RedisAddr               string
	RedisPassword           string
	RedisDB                 int
	AuthMode                string
	ConversationGap         time.Duration
	ReadHeaderTimeout       time.Duration
	WebSocketReadLimitBytes int64
}

func FromEnv() Config {
	gapMinutes := envInt("CONVERSATION_GAP_MINUTES", 10)
	return Config{
		HTTPAddr:                envString("HTTP_ADDR", ":8080"),
		MySQLDSN:                envString("MYSQL_DSN", "remi:remi@tcp(localhost:3306)/remi?parseTime=true&charset=utf8mb4"),
		RedisAddr:               envString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:           os.Getenv("REDIS_PASSWORD"),
		RedisDB:                 envInt("REDIS_DB", 0),
		AuthMode:                envString("AUTH_MODE", "dev"),
		ConversationGap:         time.Duration(gapMinutes) * time.Minute,
		ReadHeaderTimeout:       10 * time.Second,
		WebSocketReadLimitBytes: 8 << 20,
	}
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
