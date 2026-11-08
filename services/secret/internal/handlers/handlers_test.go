package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/secret/internal/crypto"
	"github.com/jp-cloud/secret/internal/store"
)

const (
	org  = "11111111-1111-4111-8111-111111111111"
	proj = "22222222-2222-4222-8222-222222222222"
)

type ver struct {
	ct, nonce []byte
	kv        int
}

type memRepo struct {
	secrets map[string]*store.SecretMeta // env/name
	vers    map[string]map[int]*ver
	audit   []string
}

func newMem() *memRepo {
	return &memRepo{secrets: map[string]*store.SecretMeta{}, vers: map[string]map[int]*ver{}}
}

func (m *memRepo) EnsureDefaultEnvironments(context.Context, string, string) error { return nil }
func (m *memRepo) ListEnvironments(context.Context, string, string) ([]store.Environment, error) {
	return []store.Environment{}, nil
}
func (m *memRepo) CreateEnvironment(_ context.Context, o, p, n string) (*store.Environment, error) {
	return &store.Environment{Name: n}, nil
}
func (m *memRepo) ListSecrets(_ context.Context, o, p, env string) ([]store.SecretMeta, error) {
	out := []store.SecretMeta{}
	for _, s := range m.secrets {
		if env == "all" || s.Environment == env {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (m *memRepo) GetSecret(_ context.Context, o, p, env, name string) (*store.SecretMeta, error) {
	if s, ok := m.secrets[env+"/"+name]; ok {
		c := *s
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (m *memRepo) CreateSecret(_ context.Context, o, p, env, name, hint string, ct, nonce []byte, kv int, actor string) (*store.SecretMeta, error) {
	k := env + "/" + name
	if _, ok := m.secrets[k]; ok {
		return nil, store.ErrConflict
	}
	s := &store.SecretMeta{ID: uuid.NewString(), OrgID: o, ProjectID: p, Environment: env, Name: name, CurrentVersion: 1, ValueHint: hint}
	m.secrets[k] = s
	m.vers[s.ID] = map[int]*ver{1: {ct, nonce, kv}}
	c := *s
	return &c, nil
}
func (m *memRepo) UpdateSecretWithRename(_ context.Context, meta *store.SecretMeta, newName, hint string, ct, nonce []byte, kv int, actor string) (*store.SecretMeta, error) {
	s := m.secrets[meta.Environment+"/"+meta.Name]
	if len(ct) > 0 {
		s.CurrentVersion++
		m.vers[s.ID][s.CurrentVersion] = &ver{ct, nonce, kv}
		s.ValueHint = hint
	}
	if newName != "" && newName != s.Name {
		delete(m.secrets, meta.Environment+"/"+meta.Name)
		s.Name = newName
		m.secrets[meta.Environment+"/"+newName] = s
	}
	c := *s
	return &c, nil
}
func (m *memRepo) DeleteSecret(_ context.Context, o, p, env, name string) error {
	if _, ok := m.secrets[env+"/"+name]; !ok {
		return store.ErrNotFound
	}
	delete(m.secrets, env+"/"+name)
	return nil
}
func (m *memRepo) ListVersions(context.Context, string) ([]store.SecretVersionMeta, error) {
	return []store.SecretVersionMeta{}, nil
}
func (m *memRepo) GetCiphertext(_ context.Context, id string, v int) ([]byte, []byte, int, error) {
	x := m.vers[id][v]
	return x.ct, x.nonce, x.kv, nil
}
func (m *memRepo) Audit(_ context.Context, o, p, env, name, action, actor, rid string, meta map[string]any) {
	m.audit = append(m.audit, action+":"+name)
}
func (m *memRepo) ListAudit(_ context.Context, o, p string, limit, offset int) ([]store.AuditEntry, error) {
	return []store.AuditEntry{{Action: "x"}}, nil
}
func (m *memRepo) EnvRows(_ context.Context, o, p string, envs []string) ([]store.EnvRow, error) {
	var out []store.EnvRow
	for _, s := range m.secrets {
		for _, e := range envs {
			if s.Environment == e {
				v := m.vers[s.ID][s.CurrentVersion]
				out = append(out, store.EnvRow{Environment: e, Name: s.Name, Ciphertext: v.ct, Nonce: v.nonce, KeyVersion: v.kv})
			}
		}
	}
	return out, nil
}
func (m *memRepo) DeleteProject(context.Context, string) (int64, error) {
	n := int64(len(m.secrets))
	m.secrets = map[string]*store.SecretMeta{}
	return n, nil
}
func (m *memRepo) OutdatedVersions(_ context.Context, cur, limit int, _ string) ([]store.VersionRow, error) {
	var out []store.VersionRow
	for id, vs := range m.vers {
		for n, v := range vs {
			if v.kv != cur && len(out) < limit {
				out = append(out, store.VersionRow{ID: id + "|" + string(rune('0'+n)), Ciphertext: v.ct, Nonce: v.nonce, KeyVersion: v.kv})
			}
		}
	}
	return out, nil
}
func (m *memRepo) SetCiphertext(_ context.Context, id string, ct, nonce []byte, kv int) error {
	parts := strings.Split(id, "|")
	m.vers[parts[0]][int(parts[1][0]-'0')] = &ver{ct, nonce, kv}
	return nil
}

type env struct {
	mux  *http.ServeMux
	repo *memRepo
	tok  map[string]string
	enc  *crypto.Envelope
}

func setup(t *testing.T) *env {
	t.Helper()
	roles := map[string]string{"viewer": "viewer", "member": "member", "admin": "admin", "operator": "member", "revealer": "member", "secretsview": "member", "restricted": "member"}
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
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	enc, err := crypto.NewEnvelope(crypto.Options{Production: true, Key: key, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	jm := jwtutil.NewManager("test-secret-test-secret-test-secret-123", time.Hour, time.Hour)
	repo := newMem()
	h := &Handler{Store: repo, Crypto: enc, JWT: jm, Authz: membership.New(orgSrv.URL, projSrv.URL)}
	mux := http.NewServeMux()
	h.Routes(mux)
	e := &env{mux: mux, repo: repo, tok: map[string]string{}, enc: enc}
	for u := range roles {
		e.tok[u], _, _ = jm.IssueAccess(u, u+"@x", u)
	}
	return e
}

func (e *env) do(user, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("Authorization", "Bearer "+e.tok[user])
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

const base = "/orgs/" + org + "/projects/" + proj

func TestListNeverReturnsPlaintextAndRevealIsAdminAudited(t *testing.T) {
	e := setup(t)
	if w := e.do("operator", "POST", base+"/environments/production/secrets", `{"name":"DB_PASSWORD","value":"hunter2-super-secret"}`); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{base + "/secrets", base + "/environments/production/secrets", base + "/env/production"} {
		for _, u := range []string{"viewer", "member", "admin"} {
			w := e.do(u, "GET", path, "")
			if w.Code != 200 {
				t.Fatalf("%s %s = %d", u, path, w.Code)
			}
			if strings.Contains(w.Body.String(), "hunter2") {
				t.Fatalf("plaintext leaked to %s via %s: %s", u, path, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "value_hint") {
				t.Fatalf("hint missing: %s", w.Body.String())
			}
		}
	}
	one := base + "/environments/production/secrets/DB_PASSWORD"
	for _, u := range []string{"viewer", "member", "operator", "secretsview"} {
		if w := e.do(u, "GET", one, ""); w.Code != 200 || strings.Contains(w.Body.String(), "hunter2") {
			t.Fatalf("%s must not reveal: %d %s", u, w.Code, w.Body.String())
		}
	}
	e.repo.audit = nil
	if w := e.do("revealer", "GET", one, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "hunter2-super-secret") {
		t.Fatalf("revealer must see plaintext: %d %s", w.Code, w.Body.String())
	}
	e.repo.audit = nil
	w := e.do("admin", "GET", one, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hunter2-super-secret") {
		t.Fatalf("admin reveal = %d %s", w.Code, w.Body.String())
	}
	if len(e.repo.audit) != 1 || e.repo.audit[0] != "secret.reveal:DB_PASSWORD" {
		t.Fatalf("audit = %v", e.repo.audit)
	}
	// the stored bytes are ciphertext
	for _, vs := range e.repo.vers {
		for _, v := range vs {
			if bytes.Contains(v.ct, []byte("hunter2")) {
				t.Fatal("stored plaintext")
			}
		}
	}
}

func TestRBACAndValidation(t *testing.T) {
	e := setup(t)
	cases := []struct {
		user, method, path, body string
		want                     int
	}{
		{"viewer", "POST", base + "/environments/production/secrets", `{"name":"A","value":"v"}`, 403},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"A","value":"v"}`, 201},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"A","value":"v"}`, 409},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"1BAD","value":"v"}`, 400},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"has space","value":"v"}`, 400},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"../x","value":"v"}`, 400},
		{"operator", "POST", base + "/environments/prod/secrets", `{"name":"B","value":"v"}`, 400},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"B","value":""}`, 400},
		{"operator", "POST", base + "/environments/production/secrets", `{"name":"B","value":"` + strings.Repeat("x", MaxValueBytes+1) + `"}`, 413},
		{"operator", "PUT", base + "/environments/production/secrets/A", `{"value":"v2"}`, 200},
		{"secretsview", "DELETE", base + "/environments/production/secrets/A", "", 403},
		{"restricted", "GET", base + "/secrets", "", 403},
		{"member", "POST", base + "/environments/production/secrets", `{"name":"Z","value":"v"}`, 403},
		{"viewer", "GET", base + "/secrets/audit", "", 403},
		{"member", "GET", base + "/secrets/audit", "", 403},
		{"admin", "GET", base + "/secrets/audit?limit=5", "", 200},
		{"admin", "DELETE", base + "/environments/production/secrets/A", "", 204},
		{"admin", "DELETE", base + "/environments/production/secrets/A", "", 404},
		{"", "GET", base + "/secrets", "", 401},
		{"admin", "GET", "/orgs/" + org + "/projects/33333333-3333-4333-8333-333333333333/secrets", "", 404},
	}
	for _, c := range cases {
		if w := e.do(c.user, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s %s %s = %d, want %d (%s)", c.user, c.method, c.path, w.Code, c.want, strings.TrimSpace(w.Body.String()))
		}
	}
}

func internalReq(e *env, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "int")
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func TestInternalEndpointsNeedTokenAndEnvMerge(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	e := setup(t)
	ip := "/internal/orgs/" + org + "/projects/" + proj
	for _, rt := range []struct{ m, p string }{{"GET", ip + "/env"}, {"PUT", ip + "/environments/production/secrets/X"}, {"POST", "/internal/rotate-master-key"}, {"DELETE", "/internal/projects/" + proj}} {
		req := httptest.NewRequest(rt.m, rt.p, strings.NewReader(`{"value":"v"}`))
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("%s %s without token = %d", rt.m, rt.p, w.Code)
		}
		req = httptest.NewRequest(rt.m, rt.p, strings.NewReader(`{"value":"v"}`))
		req.Header.Set("Authorization", "Bearer "+e.tok["admin"])
		w = httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("%s %s with user JWT = %d", rt.m, rt.p, w.Code)
		}
	}
	put := func(env, name, val string) {
		if w := internalReq(e, "PUT", ip+"/environments/"+env+"/secrets/"+name, `{"value":"`+val+`","actor":"addon"}`); w.Code != 201 && w.Code != 200 {
			t.Fatalf("upsert %s/%s = %d %s", env, name, w.Code, w.Body.String())
		}
	}
	put("all", "SHARED", "s1")
	put("all", "OVERRIDDEN", "from-all")
	put("production", "OVERRIDDEN", "from-prod")
	put("production", "PROD_ONLY", "prod-secret")
	put("preview", "PREVIEW_ONLY", "prev")
	put("production", "DATABASE_URL", "postgres://u:p@addon-pg:5432/db")
	put("production", "DATABASE_URL", "postgres://u:p2@addon-pg:5432/db") // upsert updates

	get := func(environment string) map[string]string {
		w := internalReq(e, "GET", ip+"/env?environment="+environment, "")
		if w.Code != 200 {
			t.Fatalf("env %s = %d %s", environment, w.Code, w.Body.String())
		}
		var out struct {
			Env map[string]string `json:"env"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Env
	}
	prod := get("production")
	if prod["OVERRIDDEN"] != "from-prod" || prod["SHARED"] != "s1" || prod["PROD_ONLY"] != "prod-secret" || prod["DATABASE_URL"] != "postgres://u:p2@addon-pg:5432/db" {
		t.Fatalf("production env = %v", prod)
	}
	if _, leaked := prod["PREVIEW_ONLY"]; leaked {
		t.Fatal("production env contains preview secret")
	}
	prev := get("preview")
	if prev["PREVIEW_ONLY"] != "prev" || prev["OVERRIDDEN"] != "from-all" || prev["SHARED"] != "s1" {
		t.Fatalf("preview env = %v", prev)
	}
	for k := range prev {
		if k == "PROD_ONLY" || k == "DATABASE_URL" {
			t.Fatalf("PREVIEW RECEIVED PRODUCTION SECRET %s", k)
		}
	}
	if w := internalReq(e, "GET", ip+"/env?environment=bogus", ""); w.Code != 400 {
		t.Fatalf("bogus env = %d", w.Code)
	}
	if w := internalReq(e, "DELETE", "/internal/projects/"+proj, ""); w.Code != 200 || len(e.repo.secrets) != 0 {
		t.Fatalf("cascade = %d, left %d", w.Code, len(e.repo.secrets))
	}
}

func TestRotationReencryptsUnderNewKey(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "int")
	e := setup(t)
	// a row written under key version 0 (legacy key, readable through the current key here)
	ct, nonce, _ := e.enc.Encrypt([]byte("legacy-value"))
	e.repo.secrets["production/LEGACY"] = &store.SecretMeta{ID: "sid", Environment: "production", Name: "LEGACY", CurrentVersion: 1}
	e.repo.vers["sid"] = map[int]*ver{1: {ct, nonce, 7}} // labelled with an old key version
	w := internalReq(e, "POST", "/internal/rotate-master-key", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reencrypted":1`) {
		t.Fatalf("rotate = %d %s", w.Code, w.Body.String())
	}
	if e.repo.vers["sid"][1].kv != e.enc.KeyVersion {
		t.Fatal("key_version not updated")
	}
	w = internalReq(e, "GET", "/internal/orgs/"+org+"/projects/"+proj+"/environments/production/secrets/LEGACY/value", "")
	if !strings.Contains(w.Body.String(), "legacy-value") {
		t.Fatalf("value lost after rotation: %s", w.Body.String())
	}
	// second run is a no-op
	if w := internalReq(e, "POST", "/internal/rotate-master-key", ""); !strings.Contains(w.Body.String(), `"reencrypted":0`) {
		t.Fatalf("second rotate = %s", w.Body.String())
	}
}
