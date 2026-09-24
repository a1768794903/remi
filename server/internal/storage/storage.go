package storage

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

type Connections struct {
	MySQL *sql.DB
	Redis *redis.Client
}

func Open(mysqlDSN, redisAddr, redisPassword string, redisDB int) (*Connections, error) {
	db, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPassword, DB: redisDB})
	return &Connections{MySQL: db, Redis: client}, nil
}

func (c *Connections) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.MySQL.PingContext(ctx); err != nil {
		return err
	}
	return c.Redis.Ping(ctx).Err()
}

func (c *Connections) Close() error {
	if err := c.Redis.Close(); err != nil {
		_ = c.MySQL.Close()
		return err
	}
	return c.MySQL.Close()
}
