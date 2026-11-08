package jwtutil

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
)

// Scopes carried in the `scp` claim of tokens minted from personal access tokens.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
	// ScopePWChange marks the limited token issued while must_change_password is set: it may only
	// call change-password, /auth/me and logout.
	ScopePWChange = "pwchange"
)

// Claims is the jp access token payload.
type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Type   string `json:"typ"` // access | refresh
	// Admin is true for platform administrators (claim `adm`).
	Admin bool `json:"adm,omitempty"`
	// Scope restricts a token minted from a PAT (claim `scp`: read|write|admin).
	// Empty means an interactive (full) session token.
	Scope string `json:"scp,omitempty"`
	// Managed marks admin-created accounts (claim `mgd`): they cannot create organizations.
	Managed bool `json:"mgd,omitempty"`
	// SessionID links an access token to its login session (claim `sid`) so revoking the
	// session also revokes its access tokens.
	SessionID string `json:"sid,omitempty"`
	// IssuedAtMs is the issue time in unix milliseconds (claim `iatms`), used for
	// "revoke everything issued before" checks with sub-second precision.
	IssuedAtMs int64 `json:"iatms,omitempty"`
	jwt.RegisteredClaims
}

// ReadOnly reports whether the token may only perform safe (read) operations.
func (c *Claims) ReadOnly() bool { return c != nil && c.Scope == ScopeRead }

// PasswordChangeOnly reports whether this is the limited token of an account that must change its password.
func (c *Claims) PasswordChangeOnly() bool { return c != nil && c.Scope == ScopePWChange }

// IssueOptions are optional extras for access tokens.
type IssueOptions struct {
	Admin     bool
	Managed   bool
	Scope     string
	SessionID string
	// TTL overrides the manager's access TTL when > 0 (never extended beyond it).
	TTL time.Duration
	// JTI forces the token id (used to link refresh tokens to sessions).
	JTI string
}

// Manager issues and validates JWTs.
type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	issuer     string
}

func NewManager(secret string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		issuer:     "jp",
	}
}

func (m *Manager) IssueAccess(userID, email, name string) (string, time.Time, error) {
	return m.issue(userID, email, name, "access", m.accessTTL, IssueOptions{})
}

// IssueAccessWith issues an access token carrying admin/scope/session claims.
func (m *Manager) IssueAccessWith(userID, email, name string, opt IssueOptions) (string, time.Time, error) {
	ttl := m.accessTTL
	if opt.TTL > 0 && opt.TTL < ttl {
		ttl = opt.TTL
	}
	return m.issue(userID, email, name, "access", ttl, opt)
}

func (m *Manager) IssueRefresh(userID, email, name string) (string, time.Time, error) {
	return m.issue(userID, email, name, "refresh", m.refreshTTL, IssueOptions{})
}

// IssueRefreshWith issues a refresh token bound to a session id.
func (m *Manager) IssueRefreshWith(userID, email, name string, opt IssueOptions) (string, time.Time, error) {
	return m.issue(userID, email, name, "refresh", m.refreshTTL, opt)
}

func (m *Manager) issue(userID, email, name, typ string, ttl time.Duration, opt IssueOptions) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(ttl)
	jti := opt.JTI
	if jti == "" {
		jti = uuid.NewString()
	}
	claims := Claims{
		UserID:     userID,
		Email:      email,
		Name:       name,
		Type:       typ,
		Admin:      opt.Admin,
		Managed:    opt.Managed,
		Scope:      opt.Scope,
		SessionID:  opt.SessionID,
		IssuedAtMs: now.UnixMilli(),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    m.issuer,
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (m *Manager) AccessTTL() time.Duration  { return m.accessTTL }
func (m *Manager) RefreshTTL() time.Duration { return m.refreshTTL }
