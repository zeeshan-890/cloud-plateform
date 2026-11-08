package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/logging/internal/retention"
	"github.com/jp-cloud/logging/internal/store"
)

type fakeRepo struct {
	mu      sync.Mutex
	entries []store.Entry
	lastQ   store.Query
}

func (f *fakeRepo) IngestBatch(_ context.Context, es []*store.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range es {
		if e.LoggedAt.IsZero() {
			e.LoggedAt = time.Now()
		}
		f.entries = append(f.entries, *e)
	}
	return nil
}
func (f *fakeRepo) Query(_ context.Context, org, proj string, q store.Query) ([]store.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastQ = q
	out := []store.Entry{}
	for i := len(f.entries) - 1; i >= 0 && len(out) < q.Limit; i-- {
		e := f.entries[i]
		if e.OrgID == org && e.ProjectID == proj && (q.Before == nil || e.LoggedAt.Before(*q.Before)) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeRepo) DeleteProject(_ context.Context, proj string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.entries[:0]
	var n int64
	for _, e := range f.entries {
		if e.ProjectID == proj {
			n++
		} else {
			keep = append(keep, e)
		}
	}
	f.entries = keep
	return n, nil
}
func (f *fakeRepo) DeleteOlderThan(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.entries[:0]
	var n int64
	for _, e := range f.entries {
		if e.LoggedAt.Before(cutoff) && n < int64(limit) {
			n++
		} else {
			keep = append(keep, e)
		}
	}
	f.entries = keep
	return n, nil
}

var (
	orgA  = uuid.NewString()
	projA = uuid.NewString()
)

type harness struct {
	h     *Handler
	mux   *http.ServeMux
	repo  *fakeRepo
	jm    *jwtutil.Manager
	roles map[string]string
}

func newHarness(t *testing.T) *harness {
	hs := &harness{repo: &fakeRepo{}, jm: jwtutil.NewManager("0123456789abcdef0123456789abcdef0123", time.Minute, time.Hour), roles: map[string]string{}}
	org := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		role, ok := hs.roles[parts[len(parts)-1]]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(r.URL.Path, "/access/") {
			switch role {
			case "owner", "admin":
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "full": true})
			case "viewer":
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "areas": map[string]string{"logs": "view"}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"role": role, "areas": map[string]string{"logs": "manage"}})
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
	}))
	proj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/projects/"+projA) && strings.Contains(r.URL.Path, "/orgs/"+orgA+"/") {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(org.Close)
	t.Cleanup(proj.Close)
	hs.h = &Handler{Store: hs.repo, JWT: hs.jm, Members: &membership.Client{OrgURL: org.URL, ProjectURL: proj.URL, HTTP: http.DefaultClient}}
	hs.mux = http.NewServeMux()
	hs.h.Routes(hs.mux)
	return hs
}

func (hs *harness) user(role string) string {
	id := uuid.NewString()
	if role != "" {
		hs.roles[id] = role
	}
	tok, _, _ := hs.jm.IssueAccess(id, "u@example.com", "U")
	return tok
}

func (hs *harness) do(method, path string, body any, token string, hdr ...string) *httptest.ResponseRecorder {
	var rdr io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	return rec
}

func logsPath(org, proj string) string { return "/orgs/" + org + "/projects/" + proj + "/logs" }

func TestIngestRequiresMemberAndProjectBinding(t *testing.T) {
	hs := newHarness(t)
	msg := map[string]any{"message": "line one"}
	for role, want := range map[string]int{"viewer": 403, "": 403, "member": 201, "admin": 201, "owner": 201} {
		if rec := hs.do("POST", logsPath(orgA, projA), msg, hs.user(role)); rec.Code != want {
			t.Errorf("role %q ingest = %d want %d", role, rec.Code, want)
		}
	}
	// a member of org A cannot write into a project that is not in that org
	if rec := hs.do("POST", logsPath(orgA, uuid.NewString()), msg, hs.user("member")); rec.Code != 404 {
		t.Errorf("unbound project must be 404, got %d", rec.Code)
	}
	if rec := hs.do("POST", logsPath(orgA, projA), msg, ""); rec.Code != 401 {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

func TestQueryViewerAllowedOutsiderDenied(t *testing.T) {
	hs := newHarness(t)
	if rec := hs.do("GET", logsPath(orgA, projA), nil, hs.user("viewer")); rec.Code != 200 {
		t.Errorf("viewer query: %d", rec.Code)
	}
	if rec := hs.do("GET", logsPath(orgA, projA), nil, hs.user("")); rec.Code != 403 {
		t.Errorf("outsider query: %d", rec.Code)
	}
}

func TestIngestLimitsAndQueryClamp(t *testing.T) {
	hs := newHarness(t)
	member := hs.user("member")
	many := make([]string, 501)
	for i := range many {
		many[i] = "x"
	}
	if rec := hs.do("POST", logsPath(orgA, projA), map[string]any{"lines": many}, member); rec.Code != 413 {
		t.Errorf("501 lines should be 413, got %d", rec.Code)
	}
	longline := strings.Repeat("a", 40000)
	if rec := hs.do("POST", logsPath(orgA, projA), map[string]any{"message": longline}, member); rec.Code != 201 {
		t.Fatalf("long line is truncated, not rejected: %d", rec.Code)
	}
	if got := len(hs.repo.entries[0].Message); got != maxMessageLen {
		t.Errorf("message not truncated: %d", got)
	}
	hs.do("GET", logsPath(orgA, projA)+"?limit=100000", nil, member)
	if hs.repo.lastQ.Limit != store.MaxLimit {
		t.Errorf("query limit must be clamped, got %d", hs.repo.lastQ.Limit)
	}
	if rec := hs.do("GET", logsPath(orgA, projA)+"?since=notatime", nil, member); rec.Code != 400 {
		t.Errorf("bad since: %d", rec.Code)
	}
}

func TestPagination(t *testing.T) {
	hs := newHarness(t)
	member := hs.user("member")
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		hs.repo.entries = append(hs.repo.entries, store.Entry{OrgID: orgA, ProjectID: projA, Message: "m", LoggedAt: base.Add(time.Duration(i) * time.Minute)})
	}
	rec := hs.do("GET", logsPath(orgA, projA)+"?limit=2", nil, member)
	var out struct {
		Entries []store.Entry `json:"entries"`
		Next    string        `json:"next_before"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Entries) != 2 || out.Next == "" {
		t.Fatalf("page 1: %s", rec.Body)
	}
	rec = hs.do("GET", logsPath(orgA, projA)+"?limit=2&before="+out.Next, nil, member)
	var p2 struct{ Entries []store.Entry }
	_ = json.Unmarshal(rec.Body.Bytes(), &p2)
	if len(p2.Entries) != 2 || !p2.Entries[0].LoggedAt.Before(out.Entries[1].LoggedAt) {
		t.Fatalf("page 2 must continue older: %s", rec.Body)
	}
}

func TestInternalEndpointsProtected(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	hs := newHarness(t)
	body := map[string]any{"org_id": orgA, "project_id": projA, "message": "m"}
	if rec := hs.do("POST", "/internal/logs", body, hs.user("owner")); rec.Code != 403 {
		t.Errorf("internal ingest with user JWT: %d", rec.Code)
	}
	if rec := hs.do("POST", "/internal/logs", body, "", internalauth.Header, "internal-token-for-tests"); rec.Code != 201 {
		t.Errorf("internal ingest: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/internal/projects/"+projA, nil, ""); rec.Code != 403 {
		t.Errorf("internal delete without token: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/internal/projects/"+projA, nil, "", internalauth.Header, "internal-token-for-tests"); rec.Code != 204 || len(hs.repo.entries) != 0 {
		t.Errorf("internal delete: %d, left=%d", rec.Code, len(hs.repo.entries))
	}
}

func TestRetentionSweepDeletesOldInBatches(t *testing.T) {
	repo := &fakeRepo{}
	now := time.Now()
	for i := 0; i < 12; i++ {
		repo.entries = append(repo.entries, store.Entry{Message: "old", LoggedAt: now.AddDate(0, 0, -30)})
	}
	for i := 0; i < 3; i++ {
		repo.entries = append(repo.entries, store.Entry{Message: "new", LoggedAt: now.Add(-time.Hour)})
	}
	n := retention.Sweep(context.Background(), repo, now.AddDate(0, 0, -14), slog.Default())
	if n != 12 || len(repo.entries) != 3 {
		t.Fatalf("deleted=%d left=%d", n, len(repo.entries))
	}
}

