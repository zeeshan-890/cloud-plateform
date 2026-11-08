// Package revocation implements access-token revocation on Redis:
//   - per-token denylist (logout) keyed by jti,
//   - per-session denylist (session revoked) keyed by sid,
//   - per-user "revoke everything issued before T" (logout-all, password change).
package revocation

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	jwtutil "github.com/jp-cloud/go-common/jwt"
)

func jtiKey(jti string) string  { return "jp:deny:" + jti }
func sidKey(sid string) string  { return "jp:deny:sid:" + sid }
func userKey(uid string) string { return "jp:revoke_before:" + uid }

// Store revokes and checks tokens.
type Store struct {
	Redis redis.Cmdable
}

// DenyToken blocks a single access token until it would have expired anyway.
func (s *Store) DenyToken(ctx context.Context, jti string, ttl time.Duration) error {
	if jti == "" || ttl <= 0 {
		return nil
	}
	return s.Redis.Set(ctx, jtiKey(jti), "1", ttl).Err()
}

// DenySession blocks all access tokens linked to a session for ttl (use the access TTL).
func (s *Store) DenySession(ctx context.Context, sid string, ttl time.Duration) error {
	if sid == "" {
		return nil
	}
	return s.Redis.Set(ctx, sidKey(sid), "1", ttl).Err()
}

// RevokeUserBefore invalidates every token of the user issued at or before now.
func (s *Store) RevokeUserBefore(ctx context.Context, uid string, ttl time.Duration) error {
	return s.Redis.Set(ctx, userKey(uid), strconv.FormatInt(time.Now().UnixMilli(), 10), ttl).Err()
}

// Denied reports whether the token has been revoked. A Redis failure is returned as an error
// so callers can fail closed.
func (s *Store) Denied(ctx context.Context, c *jwtutil.Claims) (bool, error) {
	keys := []string{jtiKey(c.ID), userKey(c.UserID)}
	if c.SessionID != "" {
		keys = append(keys, sidKey(c.SessionID))
	}
	vals, err := s.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		return false, err
	}
	if vals[0] != nil {
		return true, nil
	}
	if len(vals) > 2 && vals[2] != nil {
		return true, nil
	}
	if vals[1] != nil {
		if str, ok := vals[1].(string); ok {
			if cutoff, perr := strconv.ParseInt(str, 10, 64); perr == nil {
				issued := c.IssuedAtMs
				if issued == 0 && c.IssuedAt != nil {
					issued = c.IssuedAt.UnixMilli()
				}
				if issued <= cutoff {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
