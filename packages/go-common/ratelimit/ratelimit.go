// Package ratelimit implements an atomic fixed-window counter on Redis (INCR + PEXPIRE in a
// single Lua script, so a key can never be left without an expiry).
package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

var incr = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then redis.call('PEXPIRE', KEYS[1], ARGV[1]); ttl = tonumber(ARGV[1]) end
return {c, ttl}
`)

// Limiter counts events per key per window.
type Limiter struct {
	Redis  redis.Scripter
	Prefix string
}

// Result of a limiter check.
type Result struct {
	Allowed    bool
	Count      int64
	Remaining  int64
	RetryAfter time.Duration
}

// Allow records one hit for key and reports whether it is within limit for the window.
// On a Redis error it returns Allowed=true together with the error (callers decide how to fail).
func (l *Limiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (Result, error) {
	res, err := incr.Run(ctx, l.Redis, []string{l.Prefix + key}, window.Milliseconds()).Slice()
	if err != nil {
		return Result{Allowed: true}, err
	}
	var count, ttl int64
	if len(res) == 2 {
		count, _ = res[0].(int64)
		ttl, _ = res[1].(int64)
	}
	out := Result{Count: count, Allowed: count <= limit}
	if rem := limit - count; rem > 0 {
		out.Remaining = rem
	}
	if !out.Allowed {
		out.RetryAfter = time.Duration(ttl) * time.Millisecond
	}
	return out, nil
}

// Count returns the current count for a key without incrementing.
func (l *Limiter) Count(ctx context.Context, rdb redis.Cmdable, key string) int64 {
	n, _ := rdb.Get(ctx, l.Prefix+key).Int64()
	return n
}

// Reset clears a key.
func (l *Limiter) Reset(ctx context.Context, rdb redis.Cmdable, key string) {
	_ = rdb.Del(ctx, l.Prefix+key).Err()
}
