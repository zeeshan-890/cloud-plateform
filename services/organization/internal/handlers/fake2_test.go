package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/organization/internal/store"
)

// ---- grants on the fake repo

func (f *fakeRepo) AddMember(_ context.Context, orgID, userID, role string, managed bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.members[orgID][userID]; !ok {
		f.members[orgID][userID] = role
		f.managed[orgID+"|"+userID] = managed
	}
	return nil
}

func (f *fakeRepo) IsManagedMember(_ context.Context, orgID, userID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.members[orgID][userID]; !ok {
		return false, store.ErrNotFound
	}
	return f.managed[orgID+"|"+userID], nil
}

func (f *fakeRepo) GetGrant(_ context.Context, orgID, projectID, userID string) (*store.Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.grants {
		if g.OrgID == orgID && g.ProjectID == projectID && g.UserID == userID {
			c := g
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepo) ListGrantsForUser(_ context.Context, orgID, userID string) ([]store.Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Grant{}
	for _, g := range f.grants {
		if g.OrgID == orgID && g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListGrantsForOrg(_ context.Context, orgID string) ([]store.Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Grant{}
	for _, g := range f.grants {
		if g.OrgID == orgID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fakeRepo) ReplaceGrants(_ context.Context, orgID, userID, by string, gs []store.Grant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.grants[:0]
	for _, g := range f.grants {
		if !(g.OrgID == orgID && g.UserID == userID) {
			keep = append(keep, g)
		}
	}
	f.grants = keep
	for _, g := range gs {
		g.OrgID, g.UserID, g.GrantedBy = orgID, userID, by
		f.grants = append(f.grants, g)
	}
	return nil
}

func (f *fakeRepo) DeleteGrantsForProject(_ context.Context, projectID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.grants[:0]
	for _, g := range f.grants {
		if g.ProjectID != projectID {
			keep = append(keep, g)
		}
	}
	f.grants = keep
	return nil
}

// ---- audit capture

type capAudit struct {
	mu sync.Mutex
	ev []audit.Event
}

func (c *capAudit) Write(_ context.Context, e audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ev = append(c.ev, e)
	return nil
}

func (c *capAudit) has(action string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.ev {
		if e.Action == action {
			return true
		}
	}
	return false
}

// ---- fake identity service

type fakeIdentity struct {
	mu        sync.Mutex
	calls     []string
	usernames map[string]string // username -> id
	users     map[string]map[string]any
	creates   []map[string]any
	failLast  bool
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{usernames: map[string]string{}, users: map[string]map[string]any{}}
}

func (f *fakeIdentity) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeIdentity) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	write := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	errBody := func(code int, msg string) { write(code, map[string]any{"error": map[string]string{"code": "x", "message": msg}}) }
	p := r.URL.Path
	switch {
	case r.Method == "POST" && p == "/internal/users":
		f.creates = append(f.creates, body)
		uname, _ := body["username"].(string)
		uname = strings.ToLower(uname)
		if len(uname) < 3 {
			errBody(400, "username must be 3-32 characters")
			return
		}
		if _, dup := f.usernames[uname]; dup {
			errBody(409, "username or email is already in use")
			return
		}
		id := uuid.NewString()
		f.usernames[uname] = id
		u := map[string]any{"id": id, "name": body["name"], "username": uname, "email": body["email"], "disabled": false, "must_change_password": true}
		f.users[id] = u
		out := map[string]any{"user": u}
		if pw, _ := body["password"].(string); pw == "" {
			out["generated_password"] = "Gen-Pass-123456789012"
		}
		write(201, out)
	case r.Method == "POST" && p == "/internal/users/lookup":
		ids, _ := body["ids"].([]any)
		var users []map[string]any
		for _, id := range ids {
			if u, ok := f.users[id.(string)]; ok {
				users = append(users, u)
			}
		}
		write(200, map[string]any{"users": users})
	case r.Method == "PATCH" && strings.HasPrefix(p, "/internal/managed-users/"):
		id := strings.TrimPrefix(p, "/internal/managed-users/")
		if u, ok := f.users[id]; ok {
			if d, ok := body["disabled"].(bool); ok {
				u["disabled"] = d
			}
		}
		write(200, map[string]any{"user": f.users[id]})
	case r.Method == "POST" && strings.HasSuffix(p, "/reset-password"):
		write(200, map[string]any{"must_change_password": true, "generated_password": "Reset-Pass-1234567890"})
	case r.Method == "DELETE" && strings.Contains(p, "/sessions/"):
		w.WriteHeader(204)
	case r.Method == "GET" && strings.HasSuffix(p, "/sessions"):
		write(200, map[string]any{"sessions": []any{}})
	case r.Method == "DELETE" && strings.HasPrefix(p, "/internal/managed-users/"):
		delete(f.users, strings.TrimPrefix(p, "/internal/managed-users/"))
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}
