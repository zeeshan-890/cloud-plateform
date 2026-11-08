// Package gateway is the public API edge: authentication (JWT + PAT), revocation, org membership,
// rate limiting and routing to the internal services.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/jp-cloud/gateway/internal/proxy"
	"github.com/jp-cloud/go-common/clientip"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/ratelimit"
	"github.com/jp-cloud/go-common/revocation"
)

// Config is everything the gateway needs from the environment.
type Config struct {
	Env                string
	Upstreams          map[string]string // service name -> base URL
	CORSOrigins        []string
	TrustedProxies     []string
	PublicBaseURL      string
	PublicDashboardURL string
	RegistrationMode   string
	StaticDir          string
	DashboardURL       string
	JWTSecret          string
}

// Deps are the runtime collaborators (faked in tests).
type Deps struct {
	JWT     *jwtutil.Manager
	Redis   redis.UniversalClient
	Members *membership.Client
	HTTP    *http.Client // internal client: signs with INTERNAL_TOKEN
	Log     *slog.Logger
}

// Server wires the gateway.
type Server struct {
	cfg       Config
	d         Deps
	proxies   map[string]*proxy.Reverse
	resolver  *clientip.Resolver
	revoker   *revocation.Store
	limiter   *ratelimit.Limiter
	dashboard http.Handler
}

// New builds a Server. dashboard may be nil (no UI served).
func New(cfg Config, d Deps, dashboard http.Handler) (*Server, error) {
	s := &Server{cfg: cfg, d: d, proxies: map[string]*proxy.Reverse{}, dashboard: dashboard}
	for name, u := range cfg.Upstreams {
		var opts []proxy.Option
		if name == "identity" {
			opts = append(opts, proxy.Signed()) // identity trusts X-JP-Client-IP from the gateway only
		}
		p, err := proxy.New(u, opts...)
		if err != nil {
			return nil, fmt.Errorf("%s upstream: %w", name, err)
		}
		s.proxies[name] = p
	}
	s.resolver = clientip.New(cfg.TrustedProxies)
	s.revoker = &revocation.Store{Redis: d.Redis}
	s.limiter = &ratelimit.Limiter{Redis: d.Redis, Prefix: "jp:rl:gw:"}
	if s.d.Log == nil {
		s.d.Log = slog.Default()
	}
	return s, nil
}

// allowedInbound X- headers a client may send through the gateway. Everything else starting with
// "X-" (identity, internal, forwarding headers) is dropped before routing.
var allowedInbound = map[string]bool{
	"x-request-id": true, "x-trace-id": true, "x-requested-with": true,
	"x-hub-signature": true, "x-hub-signature-256": true,
	"x-github-event": true, "x-github-delivery": true, "x-github-token": true,
}

// stripInbound removes every client-supplied identity/internal/forwarding header.
func stripInbound(h http.Header) {
	for k := range h {
		lk := strings.ToLower(k)
		if lk == "forwarded" || (strings.HasPrefix(lk, "x-") && !allowedInbound[lk]) {
			h.Del(k)
		}
	}
}

// edge resolves the client IP (trusting X-Forwarded-For only from TRUSTED_PROXIES) and strips
// all spoofable inbound headers.
func (s *Server) edge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.resolver.FromRequest(r)
		stripInbound(r.Header)
		if id := middleware.RequestIDFrom(r.Context()); id != "" {
			r.Header.Set("X-Request-ID", id)
		}
		next.ServeHTTP(w, r.WithContext(proxy.WithClientIP(r.Context(), ip)))
	})
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.register(mux)
	return middleware.Chain(mux, middleware.RequestID, middleware.Logging(s.d.Log), middleware.CORS(s.cfg.CORSOrigins), s.edge)
}

// ---------------------------------------------------------------- route helpers

type opts struct {
	public   bool
	admin    bool // platform admin (claim adm) required
	stream   bool // no write deadline, no request timeout
	timeout  time.Duration
	maxBody  int64
	minRole  membership.Role // org routes: override the default (viewer for reads, member for writes)
	limit    int64           // per-IP requests per window (0 = none)
	window   time.Duration
	perUser  bool
	rlName   string
	selfOnly bool
}

const (
	defaultTimeout = 60 * time.Second
	defaultMaxBody = 4 << 20
)

func isSafe(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (s *Server) wrap(up string, o opts, orgScoped bool, h http.Handler) http.Handler {
	if o.timeout == 0 {
		o.timeout = defaultTimeout
	}
	if o.maxBody == 0 {
		o.maxBody = defaultMaxBody
	}
	inner := h
	if !o.public {
		if orgScoped {
			inner = s.withOrg(o, inner)
		} else {
			inner = s.injectUser(inner)
		}
		if o.admin {
			inner = s.requireAdmin(inner)
		}
		inner = s.authenticate(inner)
	}
	if o.limit > 0 {
		inner = s.rateLimit(o, inner)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if !o.stream {
			dl := time.Now().Add(o.timeout)
			_ = rc.SetWriteDeadline(dl)
			_ = rc.SetReadDeadline(dl)
			ctx, cancel := context.WithTimeout(r.Context(), o.timeout)
			defer cancel()
			r = r.WithContext(ctx)
		} else {
			_ = rc.SetWriteDeadline(time.Time{})
		}
		if r.ContentLength > o.maxBody {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, o.maxBody)
		}
		inner.ServeHTTP(w, r)
	})
}

// forward strips the /api/v1 prefix and proxies to the upstream service.
func (s *Server) forward(up string) http.Handler {
	p := s.proxies[up]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p == nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "upstream "+up+" is not configured")
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v1")
		if r2.URL.Path == "" {
			r2.URL.Path = "/"
		}
		p.ServeHTTP(w, r2)
	})
}

// ---------------------------------------------------------------- middleware

func (s *Server) rateLimit(o opts, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := o.rlName + ":" + proxy.ClientIPFrom(r.Context())
		if o.perUser {
			if c, ok := middleware.ClaimsFrom(r.Context()); ok {
				key = o.rlName + ":u:" + c.UserID
			}
		}
		res, err := s.limiter.Allow(r.Context(), key, o.limit, o.window)
		if err != nil {
			s.d.Log.Warn("rate limiter unavailable; allowing", "err", err)
		} else if !res.Allowed {
			secs := int(res.RetryAfter.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many requests; retry in "+strconv.Itoa(secs)+"s")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type patInfo struct {
	User struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
		Managed bool   `json:"managed"`
	} `json:"user"`
	Scope string `json:"scope"`
}

// authenticate accepts JWT access tokens and jp_pat_* personal access tokens. Revoked tokens are
// rejected (Redis outage fails closed). PAT scope is carried into the minted token (`scp`).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		token := strings.TrimSpace(h[7:])
		var claims *jwtutil.Claims
		if strings.HasPrefix(token, "jp_pat_") {
			info, status := s.verifyPAT(r.Context(), token)
			switch status {
			case http.StatusOK:
			case http.StatusUnauthorized:
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			default:
				httpx.Error(w, http.StatusBadGateway, "bad_gateway", "token verification unavailable")
				return
			}
			scope := info.Scope
			if scope == "" {
				scope = jwtutil.ScopeWrite
			}
			access, _, err := s.d.JWT.IssueAccessWith(info.User.ID, info.User.Email, info.User.Name, jwtutil.IssueOptions{
				Admin: info.User.IsAdmin && scope == jwtutil.ScopeAdmin, Managed: info.User.Managed, Scope: scope, TTL: 5 * time.Minute,
			})
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue access token")
				return
			}
			c, err := s.d.JWT.Parse(access)
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue access token")
				return
			}
			claims = c
			r.Header.Set("Authorization", "Bearer "+access) // downstream services validate a JWT
		} else {
			c, err := s.d.JWT.Parse(token)
			if err != nil || c.Type != "access" {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}
			cctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			denied, derr := s.revoker.Denied(cctx, c)
			cancel()
			if derr != nil {
				s.d.Log.Error("revocation check failed", "err", derr)
				httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "authentication backend unavailable")
				return
			}
			if denied {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "token revoked")
				return
			}
			claims = c
		}
		if claims.PasswordChangeOnly() && !pwChangeAllowed(r) {
			httpx.Error(w, http.StatusForbidden, "password_change_required", "you must change your password before using the platform")
			return
		}
		if claims.ReadOnly() && !isSafe(r.Method) {
			httpx.Error(w, http.StatusForbidden, "forbidden", "this personal access token is read-only")
			return
		}
		ctx := context.WithValue(r.Context(), middleware.ClaimsKey, claims)
		ctx = context.WithValue(ctx, middleware.UserIDKey, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// pwChangeAllowed lists what the limited token of an account with must_change_password may call.
func pwChangeAllowed(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "POST /api/v1/auth/password", "GET /api/v1/auth/me", "POST /api/v1/auth/logout":
		return true
	}
	return false
}

func (s *Server) verifyPAT(ctx context.Context, token string) (*patInfo, int) {
	body, _ := json.Marshal(map[string]string{"token": token})
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(s.cfg.Upstreams["identity"], "/")+"/internal/pats/verify", strings.NewReader(string(body)))
	if err != nil {
		return nil, http.StatusBadGateway
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, http.StatusUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, http.StatusBadGateway
	}
	var info patInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&info); err != nil || info.User.ID == "" {
		return nil, http.StatusBadGateway
	}
	return &info, http.StatusOK
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := middleware.ClaimsFrom(r.Context())
		if !ok || !c.Admin {
			httpx.Error(w, http.StatusForbidden, "forbidden", "platform administrator required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// injectUser sets identity headers from VERIFIED claims.
func (s *Server) injectUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := middleware.ClaimsFrom(r.Context())
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		r.Header.Set("X-User-ID", c.UserID)
		r.Header.Set("X-User-Email", c.Email)
		next.ServeHTTP(w, r)
	})
}

// withOrg verifies membership and fails CLOSED: a lookup failure is a 502 and a non-member a 403.
// The caller's role is passed downstream; viewers are limited to safe methods.
func (s *Server) withOrg(o opts, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := middleware.ClaimsFrom(r.Context())
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		orgID := r.PathValue("orgId")
		role, err := s.d.Members.Role(r, orgID, c.UserID)
		if err != nil {
			s.d.Log.Error("membership lookup failed", "org", orgID, "err", err)
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "membership check unavailable")
			return
		}
		if role == "" {
			httpx.Error(w, http.StatusForbidden, "forbidden", "not a member of this organization")
			return
		}
		min := o.minRole
		if min == "" {
			min = membership.Viewer
			if !isSafe(r.Method) {
				min = membership.Member
			}
		}
		if !role.AtLeast(min) {
			httpx.Error(w, http.StatusForbidden, "forbidden", fmt.Sprintf("%s role or higher required", min))
			return
		}
		r.Header.Set("X-User-ID", c.UserID)
		r.Header.Set("X-User-Email", c.Email)
		r.Header.Set("X-Org-ID", orgID)
		r.Header.Set("X-Org-Role", string(role))
		next.ServeHTTP(w, r)
	})
}
