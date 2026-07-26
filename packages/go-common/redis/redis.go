package redisx

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Connect creates a Redis client from a redis:// URL.
func Connect(ctx context.Context, redisURL string) (*redis.Client, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		// Allow host:port without scheme
		if !strings.Contains(redisURL, "://") {
			opt = &redis.Options{Addr: redisURL}
		} else {
			return nil, fmt.Errorf("parse redis url: %w", err)
		}
	}
	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
