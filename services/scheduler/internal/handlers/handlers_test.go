package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/scheduler/internal/cron"
	"github.com/redis/go-redis/v9"
)

const (
	org  = "11111111-1111-4111-8111-111111111111"
	proj = "22222222-2222-4222-8222-222222222222"
)

func setup(t *testing.T) (*http.ServeMux, map[string]string, *cron.Store) {
	t.Helper()
	roles := map[string]string{"viewer": "viewer", "member": "member"}
	orgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if serveAccess(w, p) {
			return
		}
		if len(p) == 5 && p[2] == org && roles[p[4]] != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"role": roles[p[4]]})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(orgSrv.Close)
	projSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/orgs/"+org+"/projects/"+proj {
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(projSrv.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	jm := jwtutil.NewManager("test-secret-test-secret-test-secret-123", time.Hour, time.Hour)
	st := cron.NewStore(rdb)
	h := &Handler{Cron: st, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL)}
	mux := http.NewServeMux()
	h.Routes(mux)
	toks := map[string]string{}
	for _, u := range []string{"viewer", "member", "stranger"} {
		toks[u], _, _ = jm.IssueAccess(u, u+"@x", u)
	}
	return mux, toks, st
}

func call(mux *http.ServeMux, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestCronRBACValidationAndCascade(t *testing.T) {
	mux, tk, st := setup(t)
	base := "/orgs/" + org + "/projects/" + proj + "/cron"
	if w := call(mux, tk["viewer"], "POST", base, `{"name":"a","cron":"@daily"}`); w.Code != 403 {
		t.Fatalf("viewer create = %d", w.Code)
	}
	if w := call(mux, tk["stranger"], "GET", base, ""); w.Code != 403 {
		t.Fatalf("stranger list = %d", w.Code)
	}
	if w := call(mux, "", "GET", base, ""); w.Code != 401 {
		t.Fatalf("anon = %d", w.Code)
	}
	if w := call(mux, tk["member"], "POST", base, `{"name":"a","cron":"not a cron"}`); w.Code != 400 {
		t.Fatalf("bad cron = %d", w.Code)
	}
	w := call(mux, tk["member"], "POST", base, `{"name":"nightly","cron":"0 3 * * *","command":["node","job.js"]}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Schedule cron.Schedule `json:"schedule"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Schedule.NextRunAt.IsZero() {
		t.Fatal("next run not computed")
	}
	if w := call(mux, tk["viewer"], "GET", base, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "nightly") {
		t.Fatalf("list = %d", w.Code)
	}
	if w := call(mux, tk["viewer"], "DELETE", base+"/"+out.Schedule.ID, ""); w.Code != 403 {
		t.Fatalf("viewer delete = %d", w.Code)
	}
	// schedule is not reachable through another project's path
	if w := call(mux, tk["member"], "DELETE", "/orgs/"+org+"/projects/33333333-3333-4333-8333-333333333333/cron/"+out.Schedule.ID, ""); w.Code == 204 {
		t.Fatal("cross-project delete succeeded")
	}
	// internal cascade requires the internal token
	t.Setenv("INTERNAL_TOKEN", "tok")
	if w := call(mux, tk["member"], "DELETE", "/internal/projects/"+proj, ""); w.Code != 403 {
		t.Fatalf("internal without token = %d", w.Code)
	}
	req := httptest.NewRequest("DELETE", "/internal/projects/"+proj, nil)
	req.Header.Set("X-Internal-Token", "tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("cascade = %d", rec.Code)
	}
	if list, _ := st.List(req.Context(), org, proj); len(list) != 0 {
		t.Fatal("schedules not removed")
	}
}

func TestCronClaimIsExclusive(t *testing.T) {
	_, _, st := setup(t)
	s := &cron.Schedule{OrgID: org, ProjectID: proj, Name: "x", Cron: "@every 1m"}
	if err := st.Create(httptest.NewRequest("GET", "/", nil).Context(), s); err != nil {
		t.Fatal(err)
	}
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	a, _ := st.Claim(ctx, s)
	b, _ := st.Claim(ctx, s)
	if !a || b {
		t.Fatalf("claim a=%v b=%v, want exactly one winner", a, b)
	}
}
