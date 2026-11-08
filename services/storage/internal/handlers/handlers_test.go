package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/storage/internal/miniox"
	"github.com/jp-cloud/storage/internal/store"
)

const org, prj = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
const prj2 = "33333333-3333-3333-3333-333333333333"

type memStore struct {
	mu      sync.Mutex
	buckets map[string]*store.Bucket
	objs    map[string]*store.Object // project|key
}

func newMem() *memStore {
	return &memStore{buckets: map[string]*store.Bucket{}, objs: map[string]*store.Object{}}
}

func (m *memStore) EnsureBucket(_ context.Context, o, p string) (*store.Bucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.buckets[p]; ok {
		return b, nil
	}
	b := &store.Bucket{ID: "b-" + p, OrgID: o, ProjectID: p, Name: store.BucketName(p), Mode: "minio"}
	m.buckets[p] = b
	return b, nil
}
func (m *memStore) GetBucket(_ context.Context, p string) (*store.Bucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.buckets[p]; ok {
		return b, nil
	}
	return nil, store.ErrNotFound
}
func (m *memStore) DeleteBucket(_ context.Context, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buckets, p)
	for k, o := range m.objs {
		if o.ProjectID == p {
			delete(m.objs, k)
		}
	}
	return nil
}
func (m *memStore) UpsertObject(_ context.Context, b *store.Bucket, key, ct, etag string, size int64) (*store.Object, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var prev int64
	if o, ok := m.objs[b.ProjectID+"|"+key]; ok {
		prev = o.SizeBytes
	}
	o := &store.Object{BucketID: b.ID, OrgID: b.OrgID, ProjectID: b.ProjectID, Key: key, SizeBytes: size, ContentType: ct, ETag: etag}
	m.objs[b.ProjectID+"|"+key] = o
	return o, prev, nil
}
func (m *memStore) GetObject(_ context.Context, p, key string) (*store.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.objs[p+"|"+key]; ok {
		c := *o
		return &c, nil
	}
	return nil, store.ErrNotFound
}
func (m *memStore) ListObjects(_ context.Context, p, prefix, after string, limit int) ([]store.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Object
	for _, o := range m.objs {
		if o.ProjectID == p && strings.HasPrefix(o.Key, prefix) && o.Key > after {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *memStore) DeleteObject(_ context.Context, p, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objs[p+"|"+key]; !ok {
		return store.ErrNotFound
	}
	delete(m.objs, p+"|"+key)
	return nil
}
func (m *memStore) DeleteByPrefix(_ context.Context, p, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k, o := range m.objs {
		if o.ProjectID == p && strings.HasPrefix(o.Key, prefix) {
			keys = append(keys, o.Key)
			delete(m.objs, k)
		}
	}
	return keys, nil
}
func (m *memStore) Usage(_ context.Context, p string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b, c int64
	for _, o := range m.objs {
		if o.ProjectID == p {
			b += o.SizeBytes
			c++
		}
	}
	return b, c, nil
}

type env struct {
	srv *httptest.Server
	jm  *jwtutil.Manager
	be  *miniox.Memory
	st  *memStore
	h   *Handler
}

func setup(t *testing.T) *env {
	jm := jwtutil.NewManager("test-secret-0123456789-0123456789", time.Hour, time.Hour)
	e := &env{jm: jm, be: miniox.NewMemory(), st: newMem()}
	e.h = &Handler{Store: e.st, Backend: e.be, JWT: jm, Authz: newTestOrg(t, map[string]string{prj: org, prj2: org}).Client,
		MaxJSONUpload: 1 << 20, MaxObject: 4 << 20, QuotaBytes: 100}
	mux := http.NewServeMux()
	e.h.Routes(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, user string, body io.Reader, hdr map[string]string) (int, []byte, http.Header) {
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if user != "" {
		tok, _, _ := e.jm.IssueAccess(user, user+"@x", user)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func base(p string) string { return "/orgs/" + org + "/projects/" + p + "/storage" }

func TestValidateKey(t *testing.T) {
	ok := []string{"a", "dir/file.txt", "dir/sub dir/über.png", strings.Repeat("a", 1024), "..hidden", "a..b", "x/.well-known/y"}
	bad := []string{"", "/abs", "../x", "a/../b", "a/./b", "a/..", "a\\b", "a//b", "a\x00b", "a\nb", "a\tb", "a\x7fb", strings.Repeat("a", 1025), "\xff\xfe", "a b", "x/."}
	for _, k := range ok {
		if err := ValidateKey(k); err != nil {
			t.Errorf("%q rejected: %v", k, err)
		}
	}
	for _, k := range bad {
		if ValidateKey(k) == nil {
			t.Errorf("%q accepted", k)
		}
	}
}

func TestBucketNameFromProjectID(t *testing.T) {
	n := store.BucketName(prj)
	if !strings.HasPrefix(n, "jp-22222222-") || len(n) != len("jp-22222222-")+6 || strings.ToUpper(n) == n {
		t.Fatalf("name=%s", n)
	}
	if store.BucketName(prj) != n || store.BucketName(prj2) == n {
		t.Fatal("must be deterministic and project specific")
	}
}

func TestRBACMatrix(t *testing.T) {
	e := setup(t)
	b := base(prj)
	chk := func(name, method, path, user string, body string, want int) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		if c, out, _ := e.do(t, method, path, user, rd, nil); c != want {
			t.Errorf("%s: %s %s as %q -> %d want %d (%s)", name, method, path, user, c, want, out)
		}
	}
	chk("anon", "GET", b+"/objects", "", "", 401)
	chk("stranger", "GET", b+"/objects", "nobody", "", 403)
	chk("viewer list", "GET", b+"/objects", "viewer", "", 200)
	chk("viewer upload", "PUT", b+"/objects?key=a.txt", "viewer", "x", 403)
	chk("member upload", "PUT", b+"/objects?key=a.txt", "member", "x", 201)
	chk("viewer download", "GET", b+"/download?key=a.txt", "viewer", "", 200)
	chk("member delete one", "DELETE", b+"/objects?key=a.txt", "member", "", 204)
	chk("viewer bulk delete", "DELETE", b+"/objects?prefix=a", "viewer", "", 403)
	chk("no grant", "GET", b+"/objects", "nogrant", "", 404)
	chk("manager bulk delete", "DELETE", b+"/objects?prefix=a", "member", "", 200)
	chk("member delete bucket", "DELETE", b+"?confirm=true", "member", "", 404) // route absent: needs /bucket
	chk("viewer delete bucket", "DELETE", b+"/bucket?confirm=true", "viewer", "", 403)
	chk("delete bucket unconfirmed", "DELETE", b+"/bucket", "member", "", 400)
	chk("delete bucket", "DELETE", b+"/bucket?confirm=true", "member", "", 204)
}

func TestStreamUploadSniffsAndDownloadsSafely(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	html := "<html><script>alert(1)</script></html>"
	c, out, _ := e.do(t, "PUT", base(prj)+"/objects?key=pages/x.html", "member", strings.NewReader(html), map[string]string{"Content-Type": "application/octet-stream"})
	if c != 201 {
		t.Fatalf("%d %s", c, out)
	}
	var up struct{ Object store.Object }
	_ = json.Unmarshal(out, &up)
	if !strings.HasPrefix(up.Object.ContentType, "text/html") || up.Object.SizeBytes != int64(len(html)) {
		t.Fatalf("content type must be sniffed: %+v", up.Object)
	}
	c, body, h := e.do(t, "GET", base(prj)+"/download?key=pages/x.html", "viewer", nil, nil)
	if c != 200 || string(body) != html {
		t.Fatalf("%d %q", c, body)
	}
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("active content must be served as attachment with hardening headers: %v", h)
	}
	// png stays inline
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("0", 32)
	e.do(t, "PUT", base(prj)+"/objects?key=img.png", "member", strings.NewReader(png), nil)
	_, _, h = e.do(t, "GET", base(prj)+"/download?key=img.png", "viewer", nil, nil)
	if h.Get("Content-Type") != "image/png" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") {
		t.Fatalf("%v", h)
	}
	// 404
	if c, _, _ := e.do(t, "GET", base(prj)+"/download?key=nope", "viewer", nil, nil); c != 404 {
		t.Fatalf("%d", c)
	}
}

func TestProjectIsolation(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	e.do(t, "PUT", base(prj)+"/objects?key=secret.txt", "member", strings.NewReader("top secret"), nil)
	e.do(t, "PUT", base(prj2)+"/objects?key=other.txt", "member", strings.NewReader("other"), nil)
	if c, _, _ := e.do(t, "GET", base(prj2)+"/download?key=secret.txt", "viewer", nil, nil); c != 404 {
		t.Fatalf("project 2 must not read project 1's object: %d", c)
	}
	k1, k2 := e.be.Keys(store.BucketName(prj)), e.be.Keys(store.BucketName(prj2))
	if len(k1) != 1 || k1[0] != "secret.txt" || len(k2) != 1 || k2[0] != "other.txt" {
		t.Fatalf("objects must live in separate buckets: %v %v", k1, k2)
	}
	_, body, _ := e.do(t, "GET", base(prj2)+"/objects", "viewer", nil, nil)
	if strings.Contains(string(body), "secret.txt") {
		t.Fatal("listing leaked another project's keys")
	}
}

func TestKeyValidationOnEveryPath(t *testing.T) {
	e := setup(t)
	for _, k := range []string{"../etc/passwd", "a/../../b", "/abs", "a%00b", "a%0Ab"} {
		for _, m := range []string{"PUT"} {
			if c, _, _ := e.do(t, m, base(prj)+"/objects?key="+k, "member", strings.NewReader("x"), nil); c != 400 {
				t.Errorf("%s key %q -> %d", m, k, c)
			}
		}
		if c, _, _ := e.do(t, "GET", base(prj)+"/download?key="+k, "viewer", nil, nil); c != 400 {
			t.Errorf("download key %q -> %d", k, c)
		}
	}
	j, _ := json.Marshal(map[string]string{"key": "../x", "content_base64": base64.StdEncoding.EncodeToString([]byte("x"))})
	if c, _, _ := e.do(t, "POST", base(prj)+"/objects", "member", bytes.NewReader(j), nil); c != 400 {
		t.Errorf("json upload with traversal -> %d", c)
	}
}

func TestQuotaEnforced(t *testing.T) {
	e := setup(t) // quota 100 bytes
	put := func(key string, n int) int {
		c, _, _ := e.do(t, "PUT", base(prj)+"/objects?key="+key, "member", bytes.NewReader(bytes.Repeat([]byte("a"), n)), nil)
		return c
	}
	if c := put("a", 60); c != 201 {
		t.Fatalf("first: %d", c)
	}
	if c := put("b", 60); c != 413 {
		t.Fatalf("over quota must be refused: %d", c)
	}
	if keys := e.be.Keys(store.BucketName(prj)); len(keys) != 1 {
		t.Fatalf("rejected upload must not leave an object: %v", keys)
	}
	if c := put("a", 90); c != 201 {
		t.Fatalf("overwriting counts only the delta: %d", c)
	}
	if c := put("c", 20); c != 413 {
		t.Fatalf("quota is now almost full: %d", c)
	}
	c, out, _ := e.do(t, "GET", base(prj)+"/usage", "viewer", nil, nil)
	var u struct{ Usage usageView }
	_ = json.Unmarshal(out, &u)
	if c != 200 || u.Usage.UsedBytes != 90 || u.Usage.QuotaBytes != 100 || u.Usage.Objects != 1 {
		t.Fatalf("%d %s", c, out)
	}
}

func TestSizeLimitsStreamAndJSON(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	e.h.MaxObject = 1000
	big := bytes.Repeat([]byte("z"), 1500)
	if c, _, _ := e.do(t, "PUT", base(prj)+"/objects?key=big", "member", bytes.NewReader(big), nil); c != 413 {
		t.Fatalf("known-length oversize: %d", c)
	}
	// unknown length (chunked) oversize
	req, _ := http.NewRequest("PUT", e.srv.URL+base(prj)+"/objects?key=big2", io.NopCloser(bytes.NewReader(big)))
	tok, _, _ := e.jm.IssueAccess("member", "m@x", "member")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("chunked oversize: %d", resp.StatusCode)
	}
	if keys := e.be.Keys(store.BucketName(prj)); len(keys) != 0 {
		t.Fatalf("oversize uploads must be removed: %v", keys)
	}
	e.h.MaxJSONUpload = 100
	j, _ := json.Marshal(map[string]string{"key": "j", "content_base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("q"), 500))})
	if c, _, _ := e.do(t, "POST", base(prj)+"/objects", "member", bytes.NewReader(j), nil); c != 413 {
		t.Fatalf("json oversize: %d", c)
	}
	j, _ = json.Marshal(map[string]string{"key": "ok.txt", "content_base64": base64.StdEncoding.EncodeToString([]byte("hello")), "content_type": "text/plain"})
	if c, _, _ := e.do(t, "POST", base(prj)+"/objects", "member", bytes.NewReader(j), nil); c != 201 {
		t.Fatalf("json upload: %d", c)
	}
}

func TestReservedBackupPrefix(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	if c, _, _ := e.do(t, "PUT", base(prj)+"/objects?key=_backups/x", "member", strings.NewReader("x"), nil); c != 403 {
		t.Fatalf("users must not write reserved keys: %d", c)
	}
	// internal service path may
	if c, _, _ := e.do(t, "PUT", "/internal/projects/"+prj+"/objects?key=_backups/addon/1/dump&org_id="+org, "", strings.NewReader("dump"), nil); c != 201 {
		t.Fatalf("internal put: %d", c)
	}
	if c, _, _ := e.do(t, "DELETE", base(prj)+"/objects?key=_backups/addon/1/dump", "member", nil, nil); c != 403 {
		t.Fatalf("users must not delete backups through the object API: %d", c)
	}
	if c, _, _ := e.do(t, "DELETE", base(prj)+"/objects?prefix=_back", "member", nil, nil); c != 403 {
		t.Fatalf("prefix covering reserved keys: %d", c)
	}
	c, body, _ := e.do(t, "GET", "/internal/projects/"+prj+"/objects?key=_backups/addon/1/dump", "", nil, nil)
	if c != 200 || string(body) != "dump" {
		t.Fatalf("internal get: %d %q", c, body)
	}
}

func TestPaginationAndPrefix(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	for i := 0; i < 7; i++ {
		e.do(t, "PUT", base(prj)+fmt.Sprintf("/objects?key=logs/%02d.txt", i), "member", strings.NewReader("x"), nil)
	}
	e.do(t, "PUT", base(prj)+"/objects?key=other/z.txt", "member", strings.NewReader("x"), nil)
	var seen []string
	cursor := ""
	for page := 0; page < 10; page++ {
		_, out, _ := e.do(t, "GET", base(prj)+"/objects?prefix=logs/&limit=3&cursor="+cursor, "viewer", nil, nil)
		var r struct {
			Objects    []store.Object `json:"objects"`
			NextCursor string         `json:"next_cursor"`
		}
		_ = json.Unmarshal(out, &r)
		for _, o := range r.Objects {
			seen = append(seen, o.Key)
		}
		if r.NextCursor == "" {
			break
		}
		cursor = r.NextCursor
	}
	if len(seen) != 7 || seen[0] != "logs/00.txt" || seen[6] != "logs/06.txt" {
		t.Fatalf("pagination: %v", seen)
	}
	if c, _, _ := e.do(t, "GET", base(prj)+"/objects?limit=5000", "viewer", nil, nil); c != 400 {
		t.Fatalf("limit cap: %d", c)
	}
}

func TestSignedURL(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	e.do(t, "PUT", base(prj)+"/objects?key=a.txt", "member", strings.NewReader("x"), nil)
	c, out, _ := e.do(t, "POST", base(prj)+"/signed-url", "viewer", strings.NewReader(`{"key":"a.txt"}`), nil)
	var r map[string]any
	_ = json.Unmarshal(out, &r)
	if c != 200 || r["mode"] != "proxy" || !strings.Contains(r["download_path"].(string), "/storage/download?key=a.txt") {
		t.Fatalf("without STORAGE_PUBLIC_URL downloads are proxied through the API: %d %v", c, r)
	}
	e.h.PublicURL = "https://files.example.com"
	_, out, _ = e.do(t, "POST", base(prj)+"/signed-url", "viewer", strings.NewReader(`{"key":"a.txt"}`), nil)
	_ = json.Unmarshal(out, &r)
	if r["mode"] != "presigned" || !strings.HasPrefix(r["url"].(string), "https://files.example.com/") {
		t.Fatalf("%v", r)
	}
	if c, _, _ := e.do(t, "POST", base(prj)+"/signed-url", "viewer", strings.NewReader(`{"key":"missing"}`), nil); c != 404 {
		t.Fatalf("%d", c)
	}
}

func TestProjectDeleteCascade(t *testing.T) {
	e := setup(t)
	e.h.QuotaBytes = 0
	e.do(t, "PUT", base(prj)+"/objects?key=a.txt", "member", strings.NewReader("x"), nil)
	if c, _, _ := e.do(t, "DELETE", "/internal/projects/"+prj, "", nil, nil); c != 200 {
		t.Fatalf("%d", c)
	}
	if len(e.be.Buckets) != 0 || len(e.st.buckets) != 0 || len(e.st.objs) != 0 {
		t.Fatalf("bucket and objects must be gone: %v", e.be.Buckets)
	}
	if c, _, _ := e.do(t, "DELETE", "/internal/projects/"+prj, "", nil, nil); c != 200 {
		t.Fatalf("cascade must be idempotent: %d", c)
	}
}

func TestInternalRequiresToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "tok")
	e := setup(t)
	for _, p := range [][2]string{{"PUT", "/internal/projects/" + prj + "/objects?key=a"}, {"GET", "/internal/projects/" + prj + "/objects?key=a"}, {"DELETE", "/internal/projects/" + prj}} {
		if c, _, _ := e.do(t, p[0], p[1], "", nil, nil); c != 403 {
			t.Errorf("%v -> %d", p, c)
		}
	}
}

func TestMinIOConfigRefusesDefaultsInProduction(t *testing.T) {
	t.Setenv("MINIO_ENDPOINT", "minio:9000")
	t.Setenv("MINIO_ACCESS_KEY", "")
	t.Setenv("MINIO_SECRET_KEY", "")
	if _, err := miniox.ConfigFromEnv(); err == nil {
		t.Fatal("missing credentials must fail (no minioadmin fallback)")
	}
	t.Setenv("JP_ENV", "production")
	t.Setenv("MINIO_ACCESS_KEY", "minioadmin")
	t.Setenv("MINIO_SECRET_KEY", "minioadmin")
	if _, err := miniox.ConfigFromEnv(); err == nil {
		t.Fatal("minioadmin must be refused in production")
	}
	t.Setenv("MINIO_ACCESS_KEY", "jpAbCdEf123456")
	t.Setenv("MINIO_SECRET_KEY", "0123456789abcdef0123456789abcdef")
	if _, err := miniox.ConfigFromEnv(); err != nil {
		t.Fatalf("random credentials must be accepted: %v", err)
	}
}
