// Package cache provides the small caching surface the storefront needs.
package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
)

// Cache stores rendered responses. Every method degrades to a miss rather than
// an error: a cache outage must slow the storefront down, never break it.
type Cache interface {
	GetJSON(ctx context.Context, key string, dst any) bool
	SetJSON(ctx context.Context, key string, v any, ttl time.Duration)
}

// Noop is used when Redis is disabled or unreachable.
type Noop struct{}

func (Noop) GetJSON(context.Context, string, any) bool           { return false }
func (Noop) SetJSON(context.Context, string, any, time.Duration) {}

// Redis is the real implementation.
type Redis struct{ client *redis.Client }

// New returns a Redis-backed cache, or Noop when Redis is disabled or cannot be
// reached at boot. The caller gets a working Cache either way.
func New(ctx context.Context, cfg config.RedisConfig) Cache {
	if cfg.Disabled {
		return Noop{}
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return Noop{}
	}
	return &Redis{client: client}
}

func (r *Redis) GetJSON(ctx context.Context, key string, dst any) bool {
	raw, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

func (r *Redis) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	// Fire and forget: a failed cache write is not a failed request.
	_ = r.client.Set(ctx, key, raw, ttl).Err()
}
