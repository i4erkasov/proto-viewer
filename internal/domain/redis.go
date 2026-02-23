package domain

import "context"

type RedisKeyType string

const (
	RedisKeyTypeString RedisKeyType = "string"
	RedisKeyTypeHash   RedisKeyType = "hash"
)

type RedisConfig struct {
	Host     string
	Port     int
	TLS      bool
	Username string
	Password string
}

type RedisBrowseOptions struct {
	DBMin      int
	DBMax      int
	KeysLimit  int
	ScanCount  int
	KeyPattern string
}

type RedisRepository interface {
	Ping(ctx context.Context, cfg RedisConfig) error
	DBsWithKeys(ctx context.Context, cfg RedisConfig, minDB, maxDB int) ([]int, error)
	Keys(ctx context.Context, cfg RedisConfig, db int, pattern string, limit, scanCount int) ([]string, error)
	KeyType(ctx context.Context, cfg RedisConfig, db int, key string) (RedisKeyType, error)
	HashFields(ctx context.Context, cfg RedisConfig, db int, key string) ([]string, error)
	Get(ctx context.Context, cfg RedisConfig, db int, key string) ([]byte, error)
	HGet(ctx context.Context, cfg RedisConfig, db int, key, field string) ([]byte, error)
}
