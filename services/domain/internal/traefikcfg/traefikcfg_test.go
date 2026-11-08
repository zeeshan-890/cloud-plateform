package traefikcfg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type parsedDoc struct {
	HTTP struct {
		Routers  map[string]map[string]any `yaml:"routers"`
		Services map[string]any            `yaml:"services"`
	} `yaml:"http"`
}

func parse(t *testing.T, b []byte) (map[string]any, string) {
	t.Helper()
	var top map[string]any
	if err := yaml.Unmarshal(b, &top); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, b)
	}
	if len(top) != 1 {
		t.Fatalf("expected only the http key, got %v", top)
	}
	var d parsedDoc
	if err := yaml.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.HTTP.Routers) != 1 {
		t.Fatalf("expected exactly one router, got %d:\n%s", len(d.HTTP.Routers), b)
	}
	if len(d.HTTP.Services) != 0 {
		t.Fatalf("expected no inline services, got %v", d.HTTP.Services)
	}
	for name, r := range d.HTTP.Routers {
		return r, name
	}
	return nil, ""
}

func TestRenderGolden(t *testing.T) {
	got, err := Render(Route{Hostname: "app.example.com", Service: "jp-app-1a2b3c4d", TLS: true}, "letsencrypt")
	if err != nil {
		t.Fatal(err)
	}
	want := "http:\n    routers:\n        " + RouterName("app.example.com", false) + ":\n" +
		"            rule: Host(`app.example.com`)\n" +
		"            entryPoints:\n                - web\n                - websecure\n" +
		"            service: jp-app-1a2b3c4d@docker\n" +
		"            tls:\n                certResolver: letsencrypt\n" +
		"            middlewares:\n                - jp-retry@file\n"
	if string(got) != want {
		t.Fatalf("golden mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
	if strings.Contains(string(got), "#") {
		t.Fatal("generated file must not contain comments (no user data in comments)")
	}
}

func TestRenderNoTLS(t *testing.T) {
	b, err := Render(Route{Hostname: "app.example.com", Service: "jp-app-1a2b3c4d"}, "letsencrypt")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := parse(t, b)
	if _, has := r["tls"]; has {
		t.Fatal("tls block must be absent when TLS disabled")
	}
	eps := r["entryPoints"].([]any)
	if len(eps) != 1 || eps[0] != "web" {
		t.Fatalf("entrypoints=%v", eps)
	}
	if r["service"] != "jp-app-1a2b3c4d@docker" {
		t.Fatalf("service=%v", r["service"])
	}
}

func TestRenderWildcard(t *testing.T) {
	b, err := Render(Route{Hostname: "preview.example.com", Wildcard: true, Service: "jp-prev-deadbeef"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := parse(t, b)
	rule := r["rule"].(string)
	if want := "HostRegexp(`^[a-z0-9-]+" + `\.preview\.example\.com$` + "`)"; rule != want {
		t.Fatalf("rule=%q", rule)
	}
}

func TestRenderRejectsInjection(t *testing.T) {
	bad := []Route{
		{Hostname: "a`) || Host(`b.com", Service: "jp-app-1"},
		{Hostname: "a.com\nx: y", Service: "jp-app-1"},
		{Hostname: "a.com", Service: "jp-app-1\n  evil: true"},
		{Hostname: "a.com", Service: "x@file"},
		{Hostname: "a.com", Service: "Jp-App"},
		{Hostname: "a.com", Service: ""},
		{Hostname: "UPPER.com", Service: "jp-app-1"},
		{Hostname: "1.2.3.4", Service: "jp-app-1"},
	}
	for _, r := range bad {
		if _, err := Render(r, "letsencrypt"); err == nil {
			t.Errorf("Render(%+v) should fail", r)
		}
	}
}

func TestResolverNameIsMarshalledSafely(t *testing.T) {
	// a hostile resolver name from env must stay a single scalar
	b, err := Render(Route{Hostname: "a.example.com", Service: "jp-app-1", TLS: true}, "x\n  evil: true")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := parse(t, b)
	tls := r["tls"].(map[string]any)
	if tls["certResolver"] != "x\n  evil: true" || len(tls) != 1 {
		t.Fatalf("tls=%v", tls)
	}
}

func TestFileNameCanonical(t *testing.T) {
	re := regexp.MustCompile(`^domain-[0-9a-f]{16}\.yml$`)
	for _, h := range []string{"a.com", "very-long-name.sub.example.org"} {
		if !re.MatchString(FileName(h, false)) {
			t.Fatalf("bad file name %s", FileName(h, false))
		}
	}
	if FileName("a.com", false) == FileName("a.com", true) {
		t.Fatal("wildcard must have a distinct file")
	}
	if FileName("a.com", false) != FileName("a.com", false) {
		t.Fatal("not deterministic")
	}
}

func TestWriteAtomicAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "le")
	rt := Route{Hostname: "app.example.com", Service: "jp-app-1a2b3c4d", TLS: true}
	p, changed, err := m.Write(rt)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	st1, _ := os.Stat(p)
	_, changed, err = m.Write(rt)
	if err != nil || changed {
		t.Fatalf("second identical write must be a no-op: changed=%v err=%v", changed, err)
	}
	st2, _ := os.Stat(p)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("file was rewritten although unchanged")
	}
	rt.Service = "jp-app-1a2b3c4d-green"
	_, changed, err = m.Write(rt)
	if err != nil || !changed {
		t.Fatalf("service change must rewrite: %v %v", changed, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 { // router + shared jp-middlewares.yml
		t.Fatalf("no temp files may remain: %v", entries)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "jp-app-1a2b3c4d-green@docker") {
		t.Fatalf("content: %s", b)
	}
	if err := m.Remove("app.example.com", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("app.example.com", false); err != nil {
		t.Fatal("removing a missing file must not fail")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
}

func TestReconcileDerivesFromDesiredAndRemovesOrphans(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "le")
	// pre-existing state: legacy file, orphan canonical file, stale tmp, foreign file
	legacy := filepath.Join(dir, "domain-evil-example-com.yml")
	orphan := filepath.Join(dir, FileName("gone.example.com", false))
	tmp := filepath.Join(dir, ".domain-123.tmp")
	foreign := filepath.Join(dir, "gateway.yml")
	for _, f := range []string{legacy, orphan, tmp, foreign} {
		if err := os.WriteFile(f, []byte("http: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	routes := []Route{
		{Hostname: "keep.example.com", Service: "jp-app-aaaaaaaa", TLS: true},
		{Hostname: "other.example.com", Service: "jp-app-bbbbbbbb"},
	}
	res, err := m.Reconcile(routes)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 2 || res.Removed != 3 || len(res.Errors) != 0 {
		t.Fatalf("result=%+v", res)
	}
	for _, f := range []string{legacy, orphan, tmp} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", f)
		}
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign file gateway.yml must be preserved")
	}
	for _, r := range routes {
		b, err := os.ReadFile(filepath.Join(dir, FileName(r.Hostname, false)))
		if err != nil {
			t.Fatal(err)
		}
		rule, _ := parse(t, b)
		if !strings.Contains(rule["rule"].(string), r.Hostname) {
			t.Fatalf("rule=%v", rule["rule"])
		}
	}
	// second run is a no-op
	res, _ = m.Reconcile(routes)
	if res.Written != 0 || res.Unchanged != 2 || res.Removed != 0 {
		t.Fatalf("idempotent reconcile: %+v", res)
	}
	// dropping a route removes its file
	res, _ = m.Reconcile(routes[:1])
	if res.Removed != 1 {
		t.Fatalf("expected removal: %+v", res)
	}
}

func TestReconcileRequiresDir(t *testing.T) {
	if _, err := New("", "").Reconcile(nil); err == nil {
		t.Fatal("empty dir must error")
	}
}

func TestReconcileSkipsInvalidRoutes(t *testing.T) {
	m := New(t.TempDir(), "")
	res, err := m.Reconcile([]Route{{Hostname: "bad host", Service: "jp-app-1"}, {Hostname: "ok.example.com", Service: "jp-app-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 1 || len(res.Errors) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestSharedRetryMiddlewareFile(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "le")
	path := filepath.Join(dir, "jp-middlewares.yml")
	if _, _, err := m.Write(Route{Hostname: "a.example.com", Service: "jp-app-1a2b3c4d"}); err != nil {
		t.Fatal(err)
	}
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal("jp-middlewares.yml not created")
	}
	if _, _, err := m.Write(Route{Hostname: "b.example.com", Service: "jp-app-1a2b3c4d"}); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(path)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("middleware file must be written once, not rewritten per route")
	}
	var doc struct {
		HTTP struct {
			Middlewares map[string]struct {
				Retry struct {
					Attempts int `yaml:"attempts"`
				} `yaml:"retry"`
			} `yaml:"middlewares"`
		} `yaml:"http"`
	}
	b, _ := os.ReadFile(path)
	if err := yaml.Unmarshal(b, &doc); err != nil || doc.HTTP.Middlewares["jp-retry"].Retry.Attempts != 3 {
		t.Fatalf("invalid middleware file (%v): %s", err, b)
	}
	if RetryMiddleware != "jp-retry@file" {
		t.Fatal(RetryMiddleware)
	}
	// survives reconcile, even with no routes
	if _, err := m.Reconcile(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("Reconcile must not delete jp-middlewares.yml")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "domain-*.yml")); len(matches) != 0 {
		t.Fatalf("routers should be gone: %v", matches)
	}
}
