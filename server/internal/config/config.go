package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	ValkeyAddr string
	DatabaseURL string
}

func LoadConfig() Config {
	cfg := Config{
		ValkeyAddr:  os.Getenv("VALKEY_ADDR"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	if cfg.ValkeyAddr == "" {
		cfg.ValkeyAddr = "localhost:6379"
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "postgres://root:development_password@localhost:5432/redcyberfox"
	}

	return cfg
}

func InitValkey(ctx context.Context, addr string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	// Add startup readiness check
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Valkey at %s: %w", addr, err)
	}

	return rdb, nil
}

func InitDatabase(ctx context.Context, url string) (*pgxpool.Pool, error) {
	dbpool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database config: %w", err)
	}

	// Add startup readiness check
	if err := dbpool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database at %s: %w", url, err)
	}

	return dbpool, nil
}
