package cd

import (
	"reflect"
	"strings"
	"testing"
)

func TestCleanRootDir(t *testing.T) {
	ok := map[string]string{"": "", ".": "", "./": "", "backend": "backend", "/backend/": "backend", "./apps/web": "apps/web", "apps\\api": "apps/api", " services/billing-api ": "services/billing-api"}
	for in, want := range ok {
		got, err := CleanRootDir(in)
		if err != nil || got != want {
			t.Errorf("CleanRootDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "a/../b", "../x", ".git", "a/.git/b", "a//b", "bad;rm", "a\nb", "$(x)", strings.Repeat("a", 201)} {
		if got, err := CleanRootDir(bad); err == nil {
			t.Errorf("CleanRootDir(%q) = %q, want an error", bad, got)
		}
	}
}

func TestCleanPatterns(t *testing.T) {
	got, err := CleanPatterns([]string{" packages/shared/** ", "", "./libs", "libs", "docs\\*.md"})
	if err != nil || !reflect.DeepEqual(got, []string{"packages/shared/**", "libs", "docs/*.md"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := CleanPatterns([]string{"/backend/**"}); err != nil || got[0] != "backend/**" {
		t.Errorf("a leading slash is tolerated: %v %v", got, err)
	}
	for _, bad := range [][]string{{"a/../b"}, {"x\ny"}, make([]string, 0)} {
		_, err := CleanPatterns(bad)
		if len(bad) == 0 {
			if err != nil {
				t.Errorf("empty list must be fine: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("CleanPatterns(%q) accepted", bad)
		}
	}
	many := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, strings.Repeat("a", i+1))
	}
	if _, err := CleanPatterns(many); err == nil {
		t.Error("more than the allowed number of patterns accepted")
	}
}

func TestMatch(t *testing.T) {
	yes := [][2]string{
		{"backend", "backend/main.go"}, {"backend", "backend"}, {"backend/", "backend/a/b.go"}, {"backend/**", "backend/a/b.go"},
		{"**/*.md", "README.md"}, {"**/*.md", "docs/deep/x.md"}, {"docs/*.md", "docs/x.md"}, {"*.md", "x.md"},
		{"packages/*/src/**", "packages/ui/src/a/b.ts"}, {"a?c", "abc"}, {"**/package.json", "web/package.json"}, {"**/package.json", "package.json"},
	}
	for _, c := range yes {
		if !Match(c[0], c[1]) {
			t.Errorf("Match(%q, %q) = false, want true", c[0], c[1])
		}
	}
	no := [][2]string{
		{"backend", "backend-old/main.go"}, {"backend", "frontend/backend/x"}, {"docs/*.md", "docs/sub/x.md"}, {"*.md", "docs/x.md"},
		{"a?c", "a/c"}, {"packages/*/src/**", "packages/ui/test/a.ts"}, {"backend/**", "frontend/a.go"},
	}
	for _, c := range no {
		if Match(c[0], c[1]) {
			t.Errorf("Match(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

func TestRelevantMonorepo(t *testing.T) {
	backend := Settings{RootDir: "backend", AutoDeploy: true}
	frontend := Settings{RootDir: "frontend", AutoDeploy: true}
	cases := []struct {
		name    string
		s       Settings
		changed []string
		want    bool
	}{
		{"touches only the other project", backend, []string{"frontend/src/App.tsx"}, false},
		{"touches own folder", backend, []string{"backend/main.go", "README.md"}, true},
		{"root-only change", backend, []string{"README.md"}, false},
		{"unknown change set deploys", backend, nil, true},
		{"empty (known) change set does not", backend, []string{}, false},
		{"shared lib via watch path", Settings{RootDir: "backend", WatchPaths: []string{"packages/shared/**"}}, []string{"packages/shared/util.go"}, true},
		{"ignore docs inside the folder", Settings{RootDir: "backend", IgnorePaths: []string{"**/*.md", "backend/docs/**"}}, []string{"backend/README.md", "backend/docs/a.txt"}, false},
		{"ignore does not hide real code", Settings{RootDir: "backend", IgnorePaths: []string{"**/*.md"}}, []string{"backend/README.md", "backend/app.go"}, true},
		{"no folder and no paths: everything", Settings{}, []string{"anything/at/all"}, true},
		{"watch paths without a folder", Settings{WatchPaths: []string{"api/**"}}, []string{"web/x.ts"}, false},
	}
	for _, c := range cases {
		if got := c.s.Relevant(c.changed); got != c.want {
			t.Errorf("%s: Relevant(%v) = %v, want %v", c.name, c.changed, got, c.want)
		}
	}
	// the same push is relevant to exactly one of two projects of one repository
	push := []string{"backend/handler.go"}
	if !backend.Relevant(push) || frontend.Relevant(push) {
		t.Error("a backend-only push must deploy backend and not frontend")
	}
}

func TestSkipMessage(t *testing.T) {
	for _, m := range []string{"fix typo [skip ci]", "wip [CI SKIP]", "docs [skip deploy]", "x [no deploy]", "x [skip-ci]", "x [skip cd]", "x ***NO_CI***"} {
		if !SkipMessage(m) {
			t.Errorf("%q should skip", m)
		}
	}
	for _, m := range []string{"", "fix the skip button", "ci: add workflow", "[skipping] nothing"} {
		if SkipMessage(m) {
			t.Errorf("%q must not skip", m)
		}
	}
}

func TestDecide(t *testing.T) {
	base := Defaults()
	base.RootDir = "backend"
	touch := []string{"backend/a.go"}
	check := func(name string, s Settings, branch, msg string, files []string, want Decision) {
		t.Helper()
		if got := s.Decide(branch, "main", msg, files); got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	check("production push", base, "main", "m", touch, Decision{Deploy: true})
	check("branch push is a preview", base, "feature/x", "m", touch, Decision{Deploy: true, Preview: true})
	check("skip message", base, "main", "[skip ci]", touch, Decision{Reason: "skipped by commit message"})
	check("other folder", base, "main", "m", []string{"frontend/x"}, Decision{Reason: "no changed file matches this project's folder or watch paths"})

	off := base
	off.AutoDeploy = false
	check("auto-deploy off", off, "main", "m", touch, Decision{Reason: "auto-deploy is off"})
	check("auto-deploy off still previews branches", off, "dev", "m", touch, Decision{Deploy: true, Preview: true})

	nb := base
	nb.BranchPreviews = false
	check("branch previews off", nb, "dev", "m", touch, Decision{Reason: "branch previews are off"})
	check("branch previews off keeps production", nb, "main", "m", touch, Decision{Deploy: true})

	prodBranch := base
	prodBranch.DeployBranch = "production"
	check("custom deploy branch deploys", prodBranch, "production", "m", touch, Decision{Deploy: true})
	check("default branch is now a preview", prodBranch, "main", "m", touch, Decision{Deploy: true, Preview: true})

	ci := base
	ci.RequireCI = true
	check("wait for CI", ci, "main", "m", touch, Decision{Deploy: true, Wait: true, Reason: "waiting for CI"})
	check("wait for CI on previews too", ci, "dev", "m", touch, Decision{Deploy: true, Preview: true, Wait: true, Reason: "waiting for CI"})

	if got := (Settings{}).Branch(""); got != "main" {
		t.Errorf("Branch fallback = %q", got)
	}
	if got := (Settings{DeployBranch: "release"}).Branch("main"); got != "release" {
		t.Errorf("Branch = %q", got)
	}
}
