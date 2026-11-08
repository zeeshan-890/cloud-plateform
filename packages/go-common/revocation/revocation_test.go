package revocation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	jwtutil "github.com/jp-cloud/go-common/jwt"
)

func TestRevocation(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := &Store{Redis: rdb}
	ctx := context.Background()
	m := jwtutil.NewManager("0123456789abcdef0123456789abcdef", time.Minute, time.Hour)

	tok, _, _ := m.IssueAccessWith("u1", "a@b.c", "A", jwtutil.IssueOptions{SessionID: "s1"})
	c, err := m.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := s.Denied(ctx, c); d || err != nil {
		t.Fatalf("fresh token denied=%v err=%v", d, err)
	}
	_ = s.DenyToken(ctx, c.ID, time.Minute)
	if d, _ := s.Denied(ctx, c); !d {
		t.Fatal("jti deny must apply")
	}

	tok2, _, _ := m.IssueAccessWith("u1", "a@b.c", "A", jwtutil.IssueOptions{SessionID: "s2"})
	c2, _ := m.Parse(tok2)
	_ = s.DenySession(ctx, "s2", time.Minute)
	if d, _ := s.Denied(ctx, c2); !d {
		t.Fatal("session deny must apply")
	}

	tok3, _, _ := m.IssueAccessWith("u2", "x@y.z", "X", jwtutil.IssueOptions{})
	c3, _ := m.Parse(tok3)
	time.Sleep(5 * time.Millisecond)
	_ = s.RevokeUserBefore(ctx, "u2", time.Hour)
	if d, _ := s.Denied(ctx, c3); !d {
		t.Fatal("tokens issued before revoke-before must be denied")
	}
	time.Sleep(5 * time.Millisecond)
	tok4, _, _ := m.IssueAccessWith("u2", "x@y.z", "X", jwtutil.IssueOptions{})
	c4, _ := m.Parse(tok4)
	if d, _ := s.Denied(ctx, c4); d {
		t.Fatal("tokens issued after revoke-before must pass")
	}

	mr.Close()
	if _, err := s.Denied(ctx, c4); err == nil {
		t.Fatal("redis failure must surface as error so callers fail closed")
	}
}
