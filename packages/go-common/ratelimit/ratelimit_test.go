package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestFixedWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	l := &Limiter{Redis: rdb, Prefix: "t:"}
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		res, err := l.Allow(ctx, "ip1", 3, time.Minute)
		if err != nil || !res.Allowed {
			t.Fatalf("hit %d should pass: %+v %v", i, res, err)
		}
	}
	res, _ := l.Allow(ctx, "ip1", 3, time.Minute)
	if res.Allowed || res.RetryAfter <= 0 {
		t.Fatalf("4th hit must be limited: %+v", res)
	}
	if other, _ := l.Allow(ctx, "ip2", 3, time.Minute); !other.Allowed {
		t.Fatal("other key unaffected")
	}
	if ttl := mr.TTL("t:ip1"); ttl <= 0 {
		t.Fatalf("key must always have an expiry, ttl=%v", ttl)
	}
	mr.FastForward(2 * time.Minute)
	if res, _ := l.Allow(ctx, "ip1", 3, time.Minute); !res.Allowed {
		t.Fatal("window must reset")
	}
}
