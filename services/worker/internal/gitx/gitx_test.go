package gitx

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fixture struct {
	bare           string
	first, second  string
	prHead, branch string
}

// newRepo builds a real bare repository: main with 2 commits, feature branch, PR #7 ref.
func newRepo(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	_ = os.MkdirAll(work, 0o755)
	git(t, work, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "one")
	first := git(t, work, "rev-parse", "HEAD")
	_ = os.WriteFile(filepath.Join(work, "a.txt"), []byte("two"), 0o644)
	git(t, work, "commit", "-q", "-am", "two")
	second := git(t, work, "rev-parse", "HEAD")
	git(t, work, "checkout", "-q", "-b", "feature/x")
	_ = os.WriteFile(filepath.Join(work, "feat.txt"), []byte("f"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "feat")
	prHead := git(t, work, "rev-parse", "HEAD")
	bare := filepath.Join(root, "repo.git")
	git(t, root, "clone", "-q", "--bare", work, bare)
	git(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	git(t, bare, "update-ref", "refs/pull/7/head", prHead)
	return fixture{bare: bare, first: first, second: second, prHead: prHead, branch: "feature/x"}
}

func opts(t *testing.T, f fixture) Options {
	return Options{CloneURL: "file://" + f.bare, Dir: t.TempDir(), AllowFile: true, Timeout: time.Minute}
}

func TestCloneBranchAndExactSHA(t *testing.T) {
	f := newRepo(t)
	o := opts(t, f)
	o.Branch, o.SHA = "main", f.second
	var logs bytes.Buffer
	o.Out = &logs
	res, err := Clone(context.Background(), o)
	if err != nil || res.SHA != f.second {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(o.Dir, "a.txt")); string(b) != "two" {
		t.Fatalf("content = %q", b)
	}
	// short sha
	o2 := opts(t, f)
	o2.Branch, o2.SHA = "main", f.second[:9]
	if res, err := Clone(context.Background(), o2); err != nil || res.SHA != f.second {
		t.Fatalf("short sha: %v %v", res, err)
	}
	// HEAD / empty sha => branch tip
	o3 := opts(t, f)
	o3.Branch, o3.SHA = "main", "HEAD"
	if res, err := Clone(context.Background(), o3); err != nil || res.SHA != f.second {
		t.Fatalf("HEAD: %v %v", res, err)
	}
}

func TestCloneOlderCommitOnBranchAfterBranchMoved(t *testing.T) {
	f := newRepo(t)
	o := opts(t, f)
	o.Branch, o.SHA = "main", f.first // branch tip is `second`, the deployment wants `first`
	res, err := Clone(context.Background(), o)
	if err != nil || res.SHA != f.first {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(o.Dir, "a.txt")); string(b) != "one" {
		t.Fatalf("checked out wrong tree: %q", b)
	}
}

func TestMissingBranchAndUnknownSHAFail(t *testing.T) {
	f := newRepo(t)
	o := opts(t, f)
	o.Branch = "does-not-exist"
	if _, err := Clone(context.Background(), o); err == nil || !strings.Contains(err.Error(), "cannot fetch branch") {
		t.Fatalf("missing branch must fail, got %v", err)
	}
	o = opts(t, f)
	o.Branch, o.SHA = "main", strings.Repeat("ab", 20)
	if _, err := Clone(context.Background(), o); err == nil {
		t.Fatal("unknown sha must fail")
	}
	// a valid commit of ANOTHER branch must not satisfy a request for main by accident? It is reachable
	// in the repo, so it is allowed: the SHA is what gets built and verified.
	o = opts(t, f)
	o.Branch, o.SHA = "main", f.prHead
	if res, err := Clone(context.Background(), o); err != nil || res.SHA != f.prHead {
		t.Fatalf("explicit reachable sha: %v %v", res, err)
	}
}

func TestPullRequestHead(t *testing.T) {
	f := newRepo(t)
	o := opts(t, f)
	o.PRNumber, o.SHA = 7, f.prHead
	res, err := Clone(context.Background(), o)
	if err != nil || res.SHA != f.prHead {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if _, err := os.Stat(filepath.Join(o.Dir, "feat.txt")); err != nil {
		t.Fatal("PR head content missing")
	}
	o = opts(t, f)
	o.PRNumber = 99
	if _, err := Clone(context.Background(), o); err == nil {
		t.Fatal("missing PR ref must fail")
	}
}

func TestOptionInjectionAndHostileInputRejected(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	urls := []string{
		"--upload-pack=touch " + marker,
		"-oProxyCommand=touch " + marker,
		"ext::sh -c touch% " + marker,
		"https://github.com/o/r --upload-pack=x",
		"https://user:pass@github.com/o/r.git",
		"https://github.com:8443/o/r.git",
		"http://github.com/o/r.git",
		"ftp://github.com/o/r.git",
		"git://github.com/o/r.git",
		"file:///etc",
		"/etc/passwd",
		"https://-host/o/r.git",
		"git@evil.com:o/r.git",
		"",
		"https://github.com/o/r\n.git",
	}
	for _, u := range urls {
		o := Options{CloneURL: u, Branch: "main", Dir: t.TempDir(), Timeout: 5 * time.Second,
			Resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("140.82.112.3")}, nil }}
		if _, err := Clone(context.Background(), o); err == nil {
			t.Errorf("URL %q must be rejected", u)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("option injection executed a command")
	}
	for _, b := range []string{"--upload-pack=x", "-x", "a b", "a..b", "x;y", "a/", "$(id)", strings.Repeat("a", 300)} {
		o := Options{CloneURL: "https://github.com/o/r.git", Branch: b, Dir: t.TempDir(),
			Resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("140.82.112.3")}, nil }}
		if _, err := Clone(context.Background(), o); err == nil {
			t.Errorf("branch %q must be rejected", b)
		}
	}
	o := Options{CloneURL: "https://github.com/o/r.git", Branch: "main", SHA: "main; rm -rf /", Dir: t.TempDir()}
	if _, err := Clone(context.Background(), o); err == nil {
		t.Error("bad sha accepted")
	}
}

func TestSSRFAndPrivateAddressesBlocked(t *testing.T) {
	for host, ip := range map[string]string{
		"internal.example.com": "10.0.0.5", "loop.example.com": "127.0.0.1", "meta.example.com": "169.254.169.254",
		"cgnat.example.com": "100.64.1.1", "v6.example.com": "::1", "ula.example.com": "fd00::1", "zero.example.com": "0.0.0.0",
	} {
		ip := net.ParseIP(ip)
		o := Options{CloneURL: "https://" + host + "/o/r.git", Branch: "main", Dir: t.TempDir(),
			Resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{ip}, nil }}
		if _, _, err := ValidateURL(context.Background(), o); err == nil {
			t.Errorf("%s -> %s must be blocked", host, ip)
		}
	}
	for _, lit := range []string{"https://127.0.0.1/o/r.git", "https://169.254.169.254/latest", "https://[::1]/x/y", "https://10.1.2.3/o/r"} {
		if _, _, err := ValidateURL(context.Background(), Options{CloneURL: lit}); err == nil {
			t.Errorf("%s must be blocked", lit)
		}
	}
	// a name with one public and one private answer (rebinding style) is blocked
	o := Options{CloneURL: "https://mixed.example.com/o/r.git", Resolver: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("140.82.112.3"), net.ParseIP("10.0.0.1")}, nil
	}}
	if _, _, err := ValidateURL(context.Background(), o); err == nil {
		t.Error("mixed answers must be blocked")
	}
	ok := Options{CloneURL: "https://github.com/o/r.git", Resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("140.82.112.3")}, nil }}
	if k, _, err := ValidateURL(context.Background(), ok); err != nil || k != KindHTTPS {
		t.Fatalf("public https rejected: %v", err)
	}
	if k, host, err := ValidateURL(context.Background(), Options{CloneURL: "git@github.com:owner/repo.git"}); err != nil || k != KindSSH || host != "github.com" {
		t.Fatalf("ssh: %v %v %v", k, host, err)
	}
	allow := ok
	allow.AllowedHosts = []string{"gitlab.com"}
	if _, _, err := ValidateURL(context.Background(), allow); err == nil {
		t.Error("allow-list ignored")
	}
}

func TestSSHRequiresDeployKeyAndFileDisabledInProd(t *testing.T) {
	o := Options{CloneURL: "git@github.com:o/r.git", Branch: "main", Dir: t.TempDir()}
	if _, err := Clone(context.Background(), o); err == nil || !strings.Contains(err.Error(), "deploy key") {
		t.Fatalf("err = %v", err)
	}
	f := newRepo(t)
	o = Options{CloneURL: "file://" + f.bare, Branch: "main", Dir: t.TempDir()} // AllowFile unset == production behaviour
	if _, err := Clone(context.Background(), o); err == nil {
		t.Fatal("file:// must be refused without the test-only flag")
	}
	o = opts(t, f)
	o.Branch, o.Token, o.SSHKey = "main", "tok", "key"
	if _, err := Clone(context.Background(), o); err == nil {
		t.Fatal("two credentials must be refused")
	}
}

func TestTimeoutAndSizeLimit(t *testing.T) {
	f := newRepo(t)
	o := opts(t, f)
	o.Branch, o.Timeout = "main", time.Nanosecond
	if _, err := Clone(context.Background(), o); err == nil {
		t.Fatal("expected timeout error")
	}
	o = opts(t, f)
	o.Branch, o.MaxRepoBytes = "main", 1
	if _, err := Clone(context.Background(), o); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("size limit not enforced: %v", err)
	}
}

func TestEmbeddedKnownHostsArePinnedGitHubKeys(t *testing.T) {
	s := string(knownHostsGitHub)
	if !strings.Contains(s, "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl") ||
		!strings.Contains(s, "ecdsa-sha2-nistp256") || strings.Contains(s, "*") {
		t.Fatal("known_hosts content unexpected")
	}
}

func TestValidBranch(t *testing.T) {
	for _, b := range []string{"main", "feature/x-1", "release_1.2", "v1.0.0"} {
		if !ValidBranch(b) {
			t.Errorf("%q should be valid", b)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for these tests")
	}
}
