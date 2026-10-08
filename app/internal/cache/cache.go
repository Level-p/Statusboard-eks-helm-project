// Package cache stores the rendered status summary in Redis/Valkey so that
// most page views never touch PostgreSQL. A cache failure is never fatal:
// the app falls back to the database.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMiss means the key is not in the cache.
var ErrMiss = errors.New("cache miss")

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Ping(ctx context.Context) error
}

// Redis works with Redis and Valkey (same protocol).
type Redis struct{ c *redis.Client }

func NewRedis(addr, password string) *Redis {
	return &Redis{c: redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		MaxRetries:   1, // fail fast: a slow cache must never slow the page down
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond,
	})}
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := r.c.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	return b, err
}

func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.c.Set(ctx, key, value, ttl).Err()
}

func (r *Redis) Delete(ctx context.Context, key string) error { return r.c.Del(ctx, key).Err() }

func (r *Redis) Ping(ctx context.Context) error { return r.c.Ping(ctx).Err() }

// None is used when no cache is configured: every read is a miss.
type None struct{}

func (None) Get(context.Context, string) ([]byte, error)              { return nil, ErrMiss }
func (None) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (None) Delete(context.Context, string) error                     { return nil }
func (None) Ping(context.Context) error                               { return nil }
