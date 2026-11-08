package handlers

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jp-cloud/identity/internal/store"
)

// fakeRepo is an in-memory store.Repo. Each method is atomic under a mutex, mirroring the
// single-statement guarantees of the Postgres implementation.
type fakeRepo struct {
	mu          sync.Mutex
	users       map[string]*store.User
	sessions    map[string]*store.Session
	pats        map[string]*store.PAT
	patHash     map[string]string // hash -> pat id
	backup      map[string]map[string]bool // userID -> hash -> used
	settings    map[string]store.SettingRow
	initialized bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users: map[string]*store.User{}, sessions: map[string]*store.Session{}, pats: map[string]*store.PAT{},
		patHash: map[string]string{}, backup: map[string]map[string]bool{}, settings: map[string]store.SettingRow{},
	}
}

func (f *fakeRepo) byEmail(email string) *store.User {
	for _, u := range f.users {
		if u.Email == email {
			return u
		}
	}
	return nil
}

func (f *fakeRepo) CreateUser(_ context.Context, email, name, hash string) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byEmail(email) != nil {
		return nil, store.ErrAlreadyExists
	}
	u := &store.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: hash, CreatedAt: time.Now()}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeRepo) BootstrapAdmin(_ context.Context, email, name, hash string) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.initialized {
		return nil, store.ErrAlreadyInitialized
	}
	u := &store.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: hash, IsAdmin: true, CreatedAt: time.Now()}
	f.users[u.ID] = u
	f.initialized = true
	return u, nil
}

func (f *fakeRepo) GetUserByEmail(_ context.Context, email string) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.byEmail(email); u != nil {
		c := *u
		return &c, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) GetUserByID(_ context.Context, id string) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) GetUsersByIDs(ctx context.Context, ids []string) ([]store.User, error) {
	out := []store.User{}
	for _, id := range ids {
		if u, err := f.GetUserByID(ctx, id); err == nil {
			out = append(out, *u)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAdmins(_ context.Context, limit int) ([]store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.User{}
	for _, u := range f.users {
		if u.IsAdmin && len(out) < limit {
			out = append(out, *u)
		}
	}
	return out, nil
}

func (f *fakeRepo) Stats(_ context.Context) (store.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := store.Stats{BootstrapDone: f.initialized}
	for _, u := range f.users {
		st.Users++
		if u.IsAdmin {
			st.Admins++
		}
		if u.TOTPEnabled {
			st.MFAEnabled++
			if u.IsAdmin {
				st.AdminsMFAEnabled++
			}
		}
	}
	return st, nil
}

func (f *fakeRepo) UpdatePassword(_ context.Context, id, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.PasswordHash, u.MustChangePassword = hash, false
	return nil
}

func (f *fakeRepo) CreateSession(_ context.Context, id, userID, jti, ua, ip string, exp time.Time) (*store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &store.Session{ID: id, UserID: userID, RefreshJTI: jti, UserAgent: ua, IP: ip, ExpiresAt: exp, CreatedAt: time.Now()}
	f.sessions[id] = s
	return s, nil
}

func (f *fakeRepo) RotateSession(_ context.Context, oldJTI, newJTI string, exp time.Time) (*store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.RefreshJTI == oldJTI && s.RevokedAt == nil && s.ExpiresAt.After(time.Now()) {
			s.RefreshJTI = newJTI
			s.ExpiresAt = exp
			c := *s
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) GetSessionByRefreshJTI(_ context.Context, jti string) (*store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.RefreshJTI == jti {
			c := *s
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) ListSessions(_ context.Context, userID string) ([]store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Session{}
	for _, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *fakeRepo) RevokeSession(_ context.Context, userID, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || s.UserID != userID || s.RevokedAt != nil {
		return "", store.ErrNotFound
	}
	now := time.Now()
	s.RevokedAt = &now
	return s.RefreshJTI, nil
}

func (f *fakeRepo) RevokeAllSessions(_ context.Context, userID string) ([]store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Session{}
	now := time.Now()
	for _, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &now
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *fakeRepo) CreatePAT(_ context.Context, userID, name, hash, prefix string, scopes []string, exp *time.Time) (*store.PAT, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &store.PAT{ID: uuid.NewString(), UserID: userID, Name: name, TokenHash: hash, TokenPrefix: prefix, Scopes: scopes, ExpiresAt: exp, CreatedAt: time.Now()}
	f.pats[p.ID] = p
	f.patHash[hash] = p.ID
	return p, nil
}

func (f *fakeRepo) ListPATs(_ context.Context, userID string) ([]store.PAT, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.PAT{}
	for _, p := range f.pats {
		if p.UserID == userID {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakeRepo) RevokePAT(_ context.Context, userID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pats[id]
	if !ok || p.UserID != userID {
		return store.ErrNotFound
	}
	delete(f.patHash, p.TokenHash)
	delete(f.pats, id)
	return nil
}

func (f *fakeRepo) LookupPATByHash(_ context.Context, hash string) (*store.PAT, *store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.patHash[hash]
	if !ok {
		return nil, nil, store.ErrNotFound
	}
	p := *f.pats[id]
	u := *f.users[p.UserID]
	return &p, &u, nil
}

func (f *fakeRepo) TouchPAT(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pats[id]; ok {
		now := time.Now()
		p.LastUsedAt = &now
	}
	return nil
}

func (f *fakeRepo) SetPendingTOTP(_ context.Context, userID, sealed string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[userID].TOTPPendingSecret = &sealed
	return nil
}

func (f *fakeRepo) EnableTOTP(_ context.Context, userID string, hashes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[userID]
	if u.TOTPPendingSecret == nil {
		return store.ErrNotFound
	}
	u.TOTPSecret, u.TOTPPendingSecret, u.TOTPEnabled = u.TOTPPendingSecret, nil, true
	f.backup[userID] = map[string]bool{}
	for _, h := range hashes {
		f.backup[userID][h] = false
	}
	return nil
}

func (f *fakeRepo) DisableTOTP(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[userID]
	u.TOTPEnabled, u.TOTPSecret, u.TOTPPendingSecret = false, nil, nil
	delete(f.backup, userID)
	return nil
}

func (f *fakeRepo) ConsumeBackupCode(_ context.Context, userID, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used, ok := f.backup[userID][hash]
	if !ok || used {
		return false, nil
	}
	f.backup[userID][hash] = true
	return true, nil
}

func (f *fakeRepo) CountBackupCodes(_ context.Context, userID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, used := range f.backup[userID] {
		if !used {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) IsBootstrapInitialized(_ context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initialized, nil
}

func (f *fakeRepo) ListSettings(_ context.Context) ([]store.SettingRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.SettingRow{}
	for _, r := range f.settings {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRepo) UpsertSettings(_ context.Context, rows []store.SettingRow, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		r.UpdatedAt = time.Now()
		f.settings[r.Key] = r
	}
	return nil
}

func (f *fakeRepo) DeleteSettings(_ context.Context, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.settings, k)
	}
	return nil
}

func (f *fakeRepo) GetUserByLogin(ctx context.Context, login string) (*store.User, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	if strings.Contains(login, "@") {
		return f.GetUserByEmail(ctx, login)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Username != "" && u.Username == login {
			c := *u
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) CreateManagedUser(_ context.Context, orgID, name, username, email, hash string, must bool) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Username == username || (email != "" && u.Email == email) {
			return nil, store.ErrAlreadyExists
		}
	}
	o := orgID
	u := &store.User{ID: uuid.NewString(), Email: email, Name: name, Username: username, PasswordHash: hash, MustChangePassword: must, ManagedOrgID: &o, CreatedAt: time.Now()}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeRepo) managed(orgID, id string) *store.User {
	u, ok := f.users[id]
	if !ok || u.ManagedOrgID == nil || *u.ManagedOrgID != orgID {
		return nil
	}
	return u
}

func (f *fakeRepo) GetManaged(_ context.Context, orgID, id string) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.managed(orgID, id); u != nil {
		c := *u
		return &c, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) UpdateManaged(_ context.Context, orgID, id string, name *string, disabled *bool) (*store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.managed(orgID, id)
	if u == nil {
		return nil, store.ErrNotFound
	}
	if name != nil {
		u.Name = *name
	}
	if disabled != nil {
		if *disabled {
			now := time.Now()
			u.DisabledAt = &now
		} else {
			u.DisabledAt = nil
		}
	}
	c := *u
	return &c, nil
}

func (f *fakeRepo) ResetManagedPassword(_ context.Context, orgID, id, hash string, must bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.managed(orgID, id)
	if u == nil {
		return store.ErrNotFound
	}
	u.PasswordHash, u.MustChangePassword = hash, must
	return nil
}

func (f *fakeRepo) DeleteManaged(_ context.Context, orgID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.managed(orgID, id) == nil {
		return store.ErrNotFound
	}
	delete(f.users, id)
	return nil
}

func (f *fakeRepo) TouchLogin(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		now := time.Now()
		u.LastLoginAt = &now
	}
	return nil
}

var _ store.Repo = (*fakeRepo)(nil)
var _ = strings.TrimSpace
