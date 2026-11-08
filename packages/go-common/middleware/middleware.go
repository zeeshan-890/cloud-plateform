package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
)

type ctxKey string

const (
	RequestIDKey ctxKey = "request_id"
	ClaimsKey    ctxKey = "claims"
	UserIDKey    ctxKey = "user_id"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validRequestID accepts short, printable ids only (prevents log/header injection).
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// CORS returns a CORS middleware. Origins are matched exactly against the allowlist;
// arbitrary origins are never reflected. A literal "*" entry answers "*" WITHOUT
// credentials (browsers refuse credentialed wildcard responses, and so do we).
func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	allowAll := false
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			allowAll = true
			continue
		}
		allowed[strings.TrimRight(o, "/")] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
				if containsOrigin(allowed, origin) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					setCORSCommon(w)
				} else if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
					setCORSCommon(w)
				}
			}
			if r.Method == http.MethodOptions && origin != "" && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setCORSCommon(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func containsOrigin(m map[string]struct{}, origin string) bool {
	_, ok := m[strings.TrimRight(origin, "/")]
	return ok
}

func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			rid, _ := r.Context().Value(RequestIDKey).(string)
			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", rid,
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush keeps streaming responses working through the logging middleware.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// DenyCheck, when set, is consulted with the token's jti; returning true rejects the
// token (used for logout/revocation backed by Redis). Set it once at startup.
var DenyCheck func(ctx context.Context, jti string) bool

// DenyCheckClaims is a richer revocation hook (session / revoke-before checks). When set it
// is consulted in addition to DenyCheck; returning true rejects the token.
var DenyCheckClaims func(ctx context.Context, c *jwtutil.Claims) bool

// BearerAuth validates JWT access tokens and injects claims into context.
func BearerAuth(jm *jwtutil.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" || !strings.HasPrefix(strings.ToLower(h), "bearer ") {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			token := strings.TrimSpace(h[7:])
			claims, err := jm.Parse(token)
			if err != nil {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}
			if claims.Type != "access" {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "access token required")
				return
			}
			if DenyCheck != nil && DenyCheck(r.Context(), claims.ID) {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "token revoked")
				return
			}
			if DenyCheckClaims != nil && DenyCheckClaims(r.Context(), claims) {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "token revoked")
				return
			}
			ctx := context.WithValue(r.Context(), ClaimsKey, claims)
			ctx = context.WithValue(ctx, UserIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ClaimsFrom(ctx context.Context) (*jwtutil.Claims, bool) {
	c, ok := ctx.Value(ClaimsKey).(*jwtutil.Claims)
	return c, ok
}

func UserIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(UserIDKey).(string)
	return v
}

func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(RequestIDKey).(string)
	return v
}

func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
