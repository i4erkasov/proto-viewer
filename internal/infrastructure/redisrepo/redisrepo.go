package redisrepo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/redis/go-redis/v9"
)

type Repo struct {
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func New() *Repo {
	return &Repo{
		DialTimeout:  3 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
}

func (r *Repo) client(cfg domain.RedisConfig, db int) (*redis.Client, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("redis host is empty")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("redis port is invalid")
	}

	return redis.NewClient(&redis.Options{
		Addr:         cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Password:     cfg.Password,
		DB:           db,
		DialTimeout:  r.DialTimeout,
		ReadTimeout:  r.ReadTimeout,
		WriteTimeout: r.WriteTimeout,
	}), nil
}

func (r *Repo) Ping(ctx context.Context, cfg domain.RedisConfig) error {
	c, err := r.client(cfg, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Ping(ctx).Err()
}

func (r *Repo) DBsWithKeys(ctx context.Context, cfg domain.RedisConfig, minDB, maxDB int) ([]int, error) {
	if minDB < 0 {
		minDB = 0
	}
	if maxDB < minDB {
		maxDB = minDB
	}
	res := make([]int, 0, 8)
	for db := minDB; db <= maxDB; db++ {
		c, err := r.client(cfg, db)
		if err != nil {
			continue
		}
		sz, err := c.DBSize(ctx).Result()
		_ = c.Close()
		if err != nil {
			continue
		}
		if sz > 0 {
			res = append(res, db)
		}
	}
	return res, nil
}

func (r *Repo) Keys(ctx context.Context, cfg domain.RedisConfig, db int, pattern string, limit, scanCount int) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = "*"
	}
	if limit <= 0 {
		limit = 2000
	}
	if scanCount <= 0 {
		scanCount = 200
	}

	c, err := r.client(cfg, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	var cursor uint64
	keys := make([]string, 0, min(limit, 256))
	for {
		batch, next, err := c.Scan(ctx, cursor, pattern, int64(scanCount)).Result()
		if err != nil {
			return keys, nil
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 || len(keys) >= limit {
			break
		}
	}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

func (r *Repo) KeyType(ctx context.Context, cfg domain.RedisConfig, db int, key string) (domain.RedisKeyType, error) {
	c, err := r.client(cfg, db)
	if err != nil {
		return "", err
	}
	defer c.Close()

	tp, err := c.Type(ctx, key).Result()
	if err != nil {
		return "", err
	}
	tp = strings.ToLower(strings.TrimSpace(tp))
	switch tp {
	case "string":
		return domain.RedisKeyTypeString, nil
	case "hash":
		return domain.RedisKeyTypeHash, nil
	default:
		return domain.RedisKeyType(tp), nil
	}
}

func (r *Repo) HashFields(ctx context.Context, cfg domain.RedisConfig, db int, key string) ([]string, error) {
	c, err := r.client(cfg, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.HKeys(ctx, key).Result()
}

func (r *Repo) Get(ctx context.Context, cfg domain.RedisConfig, db int, key string) ([]byte, error) {
	c, err := r.client(cfg, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Get(ctx, key).Bytes()
}

func (r *Repo) HGet(ctx context.Context, cfg domain.RedisConfig, db int, key, field string) ([]byte, error) {
	c, err := r.client(cfg, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.HGet(ctx, key, field).Bytes()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
