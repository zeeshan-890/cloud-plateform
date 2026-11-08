package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------- generic JSON helpers

// Dig walks nested maps ("a","b") and returns the value (nil when missing).
func Dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// Str is Dig that returns "" unless the value is a string.
func Str(v any, keys ...string) string { s, _ := Dig(v, keys...).(string); return s }

// Arr is Dig that returns a slice (nil when not an array).
func Arr(v any, keys ...string) []any { a, _ := Dig(v, keys...).([]any); return a }

// Bool is Dig that returns false unless the value is a bool.
func Bool(v any, keys ...string) bool { b, _ := Dig(v, keys...).(bool); return b }

// Num is Dig that returns 0 unless the value is a JSON number.
func Num(v any, keys ...string) float64 { f, _ := Dig(v, keys...).(float64); return f }

// ---------------------------------------------------------------- shared admin

var (
	adminOnce sync.Once
	adminC    *Client
	adminSkip string
)

// AdminClient returns the shared platform administrator (bootstrapping the platform when it is not yet initialised).
// It renews its own tokens, so it can be used for the whole run. The calling test is skipped when no admin session can be obtained.
func AdminClient(t testing.TB) *Client {
	t.Helper()
	e := Load(t)
	adminOnce.Do(func() {
		c := e.NewClient()
		c.AutoRefresh = true
		c.LoginName, c.LoginPass = e.AdminMail, e.AdminPass
		st := c.Do("GET", "/auth/bootstrap/status", nil)
		if st.Status != 200 {
			adminSkip = fmt.Sprintf("platform not reachable: GET /auth/bootstrap/status -> %d %s", st.Status, truncate(string(st.Body), 200))
			return
		}
		if !Bool(st.Map(t), "initialized") {
			r := c.Do("POST", "/auth/bootstrap", map[string]string{"email": e.AdminMail, "password": e.AdminPass, "name": "E2E Admin", "bootstrap_token": os.Getenv("E2E_BOOTSTRAP_TOKEN")})
			if r.Status != 201 {
				adminSkip = fmt.Sprintf("bootstrap failed: %d %s", r.Status, truncate(string(r.Body), 300))
				return
			}
			c.SetTokens(r.Map(t))
			adminC = c
			return
		}
		r := c.Do("POST", "/auth/login", map[string]string{"login": e.AdminMail, "password": e.AdminPass})
		if r.Status != 200 {
			adminSkip = fmt.Sprintf("platform already bootstrapped and login as %s failed (%d): set E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD", e.AdminMail, r.Status)
			return
		}
		c.SetTokens(r.Map(t))
		adminC = c
	})
	if adminC == nil {
		t.Skip(adminSkip)
	}
	return adminC
}

// ---------------------------------------------------------------- orgs / projects / users

// Org is a created organization.
type Org struct{ ID, Slug, Name string }

// Project is a created project.
type Project struct {
	Org  *Org
	ID   string
	Slug string
}

// Path returns the API path below the project ("/runtime/instances" etc).
func (p *Project) Path(suffix string) string {
	return "/orgs/" + p.Org.ID + "/projects/" + p.ID + suffix
}

// Short8 is the first 8 hex characters of the project id (used in docker network / service names).
func (p *Project) Short8() string { return strings.ReplaceAll(p.ID, "-", "")[:8] }

// Network is the project's private docker network name.
func (p *Project) Network() string { return "jp_p_" + p.Short8() }

// NewOrg creates a uniquely named organization owned by c and deletes it on cleanup.
func NewOrg(t testing.TB, c *Client) *Org {
	t.Helper()
	slug := RandName("e2e")
	r := c.Must(t, "POST", "/orgs", map[string]string{"name": "E2E " + slug, "slug": slug}, 201)
	o := &Org{ID: Str(r.Map(t), "org", "id"), Slug: slug, Name: "E2E " + slug}
	if o.ID == "" {
		t.Fatalf("org id missing: %s", r.Body)
	}
	t.Cleanup(func() {
		old := c.Timeout
		c.Timeout = 10 * time.Minute
		defer func() { c.Timeout = old }()
		r := c.Do("DELETE", "/orgs/"+o.ID, nil)
		if r.Status != 204 && r.Status != 200 && r.Status != 404 {
			t.Logf("cleanup: DELETE org %s -> %d %s", o.ID, r.Status, truncate(string(r.Body), 300))
		}
	})
	return o
}

// NewProject creates a project in the org and deletes it (cascade) on cleanup.
func NewProject(t testing.TB, c *Client, o *Org) *Project {
	t.Helper()
	slug := RandName("p")
	r := c.Must(t, "POST", "/orgs/"+o.ID+"/projects", map[string]string{"name": "E2E " + slug, "slug": slug, "description": "e2e"}, 201)
	p := &Project{Org: o, ID: Str(r.Map(t), "project", "id"), Slug: slug}
	if p.ID == "" {
		t.Fatalf("project id missing: %s", r.Body)
	}
	t.Cleanup(func() {
		old := c.Timeout
		c.Timeout = 10 * time.Minute
		defer func() { c.Timeout = old }()
		r := c.Do("DELETE", p.Path(""), nil)
		if r.Status != 204 && r.Status != 404 {
			t.Logf("cleanup: DELETE project %s -> %d %s", p.ID, r.Status, truncate(string(r.Body), 300))
			if e := Load(t); e.SSH != "" {
				_, _ = e.TryRun("docker ps -aq --filter label=jp.project=" + p.ID + " | xargs -r docker rm -f")
			}
		}
	})
	return p
}

// User is an admin-created (managed) account.
type User struct {
	ID       string
	Username string
	Password string
	Org      *Org
	C        *Client
}

// Grant is one project grant for CreateUser.
type Grant map[string]any

// CreateUser creates a managed user through POST /orgs/{o}/users. The password is generated by the server.
// The user is logged in (unless mustChange) and deleted on cleanup.
func CreateUser(t testing.TB, admin *Client, o *Org, role string, mustChange bool, grants ...Grant) *User {
	t.Helper()
	uname := RandName("u")
	body := map[string]any{"name": "E2E " + uname, "username": uname, "role": role, "must_change_password": mustChange}
	if len(grants) > 0 {
		body["grants"] = grants
	}
	r := admin.Must(t, "POST", "/orgs/"+o.ID+"/users", body, 201)
	m := r.Map(t)
	u := &User{ID: Str(m, "user", "user_id"), Username: uname, Password: Str(m, "generated_password"), Org: o}
	if u.ID == "" || u.Password == "" {
		t.Fatalf("create user: missing user_id/generated_password in %s", truncate(string(r.Body), 300))
	}
	t.Cleanup(func() {
		r := admin.Do("DELETE", "/orgs/"+o.ID+"/users/"+u.ID, nil)
		if r.Status != 204 && r.Status != 200 && r.Status != 404 {
			t.Logf("cleanup: DELETE user %s -> %d", u.ID, r.Status)
		}
	})
	u.C = Load(t).NewClient()
	if !mustChange {
		u.Login(t)
	}
	return u
}

// Login performs POST /auth/login with the username and stores the tokens.
func (u *User) Login(t testing.TB) *Client {
	t.Helper()
	r := u.C.Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200)
	u.C.SetTokens(r.Map(t))
	return u.C
}

// FreshClient logs the user in again and returns an independent session.
func (u *User) FreshClient(t testing.TB) *Client {
	t.Helper()
	c := Load(t).NewClient()
	r := c.Must(t, "POST", "/auth/login", map[string]string{"login": u.Username, "password": u.Password}, 200)
	c.SetTokens(r.Map(t))
	return c
}

// ---------------------------------------------------------------- ssh / docker

// NeedSSH skips unless E2E_SSH is configured.
func NeedSSH(t testing.TB) *Env {
	t.Helper()
	e := Load(t)
	if e.SSH == "" {
		t.Skip("E2E_SSH not set")
	}
	return e
}

// Docker runs `docker <args>` on the target host.
func (e *Env) Docker(t testing.TB, args string) string {
	t.Helper()
	return e.Run(t, "docker "+args)
}

// Ctr is a container belonging to a project.
type Ctr struct{ ID, Name, Image, Kind, State string }

// ProjectContainers lists every container (any state) labelled jp.project=<id>.
func (e *Env) ProjectContainers(t testing.TB, projectID string) []Ctr {
	t.Helper()
	out := e.Run(t, `docker ps -a --filter label=jp.project=`+projectID+` --format '{{.ID}}|{{.Names}}|{{.Image}}|{{.Label "jp.kind"}}|{{.State}}'`)
	var cs []Ctr
	for _, l := range strings.Split(out, "\n") {
		p := strings.Split(strings.TrimSpace(l), "|")
		if len(p) == 5 {
			cs = append(cs, Ctr{p[0], p[1], p[2], p[3], p[4]})
		}
	}
	return cs
}

// Inspect is the subset of `docker inspect` the suite asserts on.
type Inspect struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Running bool
		Status  string
		Health  *struct{ Status string }
	}
	RestartCount int
	Config       struct {
		Env    []string
		Labels map[string]string
		Image  string
	}
	HostConfig struct {
		CapDrop      []string
		CapAdd       []string
		SecurityOpt  []string
		Memory       int64
		PidsLimit    *int64
		Privileged   bool
		NetworkMode  string
		Binds        []string
		PortBindings map[string][]struct{ HostIp, HostPort string }
	}
	Mounts []struct{ Type, Source, Destination string }
	Net    struct {
		Networks map[string]struct{ IPAddress string }
		Ports    map[string][]struct{ HostIp, HostPort string }
	} `json:"NetworkSettings"`
}

// Inspect runs docker inspect on a container id/name.
func (e *Env) Inspect(t testing.TB, id string) *Inspect {
	t.Helper()
	out := e.Run(t, "docker inspect "+id)
	var arr []Inspect
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 1 {
		t.Fatalf("docker inspect %s: %v: %s", id, err, truncate(out, 300))
	}
	return &arr[0]
}

// EnvHas reports whether the container environment contains NAME (any value).
func (i *Inspect) EnvHas(name string) bool {
	_, ok := i.EnvValue(name)
	return ok
}

// EnvValue returns the container's environment variable value.
func (i *Inspect) EnvValue(name string) (string, bool) {
	for _, kv := range i.Config.Env {
		if strings.HasPrefix(kv, name+"=") {
			return strings.TrimPrefix(kv, name+"="), true
		}
	}
	return "", false
}

// TraefikDynamicFiles lists the files of the Traefik file-provider directory (read from inside the traefik container).
func (e *Env) TraefikDynamicFiles(t testing.TB) []string {
	t.Helper()
	name := e.Run(t, `docker ps --format '{{.Names}}' | grep -i traefik | head -1`)
	if name == "" {
		t.Skip("no traefik container found")
	}
	out, err := e.TryRun("docker exec " + name + " ls -1 /etc/traefik/dynamic")
	if err != nil {
		t.Fatalf("list traefik dynamic dir: %v: %s", err, out)
	}
	var fs []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			fs = append(fs, l)
		}
	}
	return fs
}

// TraefikDynamicGrep greps all dynamic files for a fixed string and returns matching lines.
func (e *Env) TraefikDynamicGrep(t testing.TB, needle string) string {
	t.Helper()
	name := e.Run(t, `docker ps --format '{{.Names}}' | grep -i traefik | head -1`)
	out, _ := e.TryRun("docker exec " + name + " sh -c 'cat /etc/traefik/dynamic/* 2>/dev/null' | grep -F -- " + shQuote(needle) + " || true")
	return out
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// RunStdin runs a remote command feeding stdin.
func (e *Env) RunStdin(t testing.TB, cmd, stdin string) string {
	t.Helper()
	c := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "-o", "LogLevel=ERROR", e.SSH, cmd)
	c.Stdin = strings.NewReader(stdin)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("ssh %q: %v\n%s", cmd, err, truncate(string(out), 2000))
	}
	return strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------- fixture image

var (
	imgMu    sync.Mutex
	imgBuilt = map[string]bool{}
)

// ImageBase is the repository (without tag) of the fixture image; override with E2E_IMAGE_REPO.
func ImageBase() string { return getenv("E2E_IMAGE_REPO", "localhost:5000/e2e/addon-probe") }

// FixtureImage builds the addon-probe fixture on the server (scp + docker build) once and returns "<repo>:1".
func FixtureImage(t testing.TB) string {
	t.Helper()
	e := NeedSSH(t)
	ref := ImageBase() + ":1"
	imgMu.Lock()
	defer imgMu.Unlock()
	if imgBuilt[ref] {
		return ref
	}
	remote := "/tmp/jp-e2e-" + randHex(3)
	e.Run(t, "mkdir -p "+remote)
	if out, err := exec.Command("scp", "-o", "BatchMode=yes", "-o", "LogLevel=ERROR", "-r", "fixtures/addon-probe", e.SSH+":"+remote+"/").CombinedOutput(); err != nil {
		t.Fatalf("scp fixture: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = e.TryRun("rm -rf " + remote) })
	if out, err := e.TryRun("cd " + remote + "/addon-probe && docker build -q -t " + ref + " ."); err != nil {
		t.Fatalf("docker build fixture: %v\n%s", err, truncate(out, 3000))
	}
	pushBestEffort(t, e, ref)
	imgBuilt[ref] = true
	return ref
}

// DerivedImage builds <repo>:<tag> FROM the fixture with extra Dockerfile lines (e.g. "ENV APP_VERSION=2").
func DerivedImage(t testing.TB, tag string, lines ...string) string {
	t.Helper()
	base := FixtureImage(t)
	e := NeedSSH(t)
	ref := ImageBase() + ":" + tag
	imgMu.Lock()
	defer imgMu.Unlock()
	if imgBuilt[ref] {
		return ref
	}
	df := "FROM " + base + "\n" + strings.Join(lines, "\n") + "\n"
	e.RunStdin(t, "docker build -q -t "+ref+" -", df)
	pushBestEffort(t, e, ref)
	imgBuilt[ref] = true
	return ref
}

func pushBestEffort(t testing.TB, e *Env, ref string) {
	if os.Getenv("E2E_NO_PUSH") != "" {
		return
	}
	if out, err := e.TryRun("docker push " + ref); err != nil {
		t.Logf("docker push %s failed (image stays local-only): %s", ref, truncate(out, 300))
	}
}

// ---------------------------------------------------------------- app deployment helpers

// DeployApp creates a runtime instance (rolling deploy) of image in the project, asserting a 201.
func DeployApp(t testing.TB, c *Client, p *Project, image string, extra map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"image_ref": image, "port": 8080}
	for k, v := range extra {
		body[k] = v
	}
	r := c.Must(t, "POST", p.Path("/runtime/instances"), body, 201)
	return r.Map(t)
}

// SystemHost returns the project's platform generated hostname (kind=system domain).
func SystemHost(t testing.TB, c *Client, p *Project) string {
	t.Helper()
	r := c.Must(t, "GET", p.Path("/domains"), nil, 200)
	for _, d := range Arr(r.Map(t), "domains") {
		if Str(d, "kind") == "system" {
			return Str(d, "hostname")
		}
	}
	t.Fatalf("project %s has no system domain: %s", p.ID, truncate(string(r.Body), 400))
	return ""
}

// WaitEdge polls the Traefik edge until path answers want (status) for the Host header.
func WaitEdge(t testing.TB, e *Env, host, path string, want int, timeout time.Duration) string {
	t.Helper()
	var body string
	Eventually(t, timeout, 500*time.Millisecond, fmt.Sprintf("edge %s%s -> %d", host, path, want), func() (bool, string) {
		code, b, err := e.EdgeGet(host, path)
		body = b
		if err != nil {
			return false, err.Error()
		}
		return code == want, fmt.Sprintf("status %d body %s", code, truncate(b, 200))
	})
	return body
}

// ProjectApps returns the app (kind != addon) containers of a project.
func (e *Env) ProjectApps(t testing.TB, projectID string) []Ctr {
	var out []Ctr
	for _, c := range e.ProjectContainers(t, projectID) {
		if c.Kind == "app" || c.Kind == "" || c.Kind == "preview" {
			out = append(out, c)
		}
	}
	return out
}

// Running filters containers that are in the running state.
func Running(cs []Ctr) []Ctr {
	var out []Ctr
	for _, c := range cs {
		if c.State == "running" {
			out = append(out, c)
		}
	}
	return out
}

// RunningApp returns the single running app container, failing if there is none.
func (e *Env) RunningApp(t testing.TB, projectID string) Ctr {
	t.Helper()
	apps := Running(e.ProjectApps(t, projectID))
	if len(apps) == 0 {
		t.Fatalf("no running app container for project %s (all: %+v)", projectID, e.ProjectContainers(t, projectID))
	}
	return apps[0]
}

// DNS edits the CoreDNS test zone through scripts/e2e-dns.sh on the server (E2E_REMOTE_REPO = checkout path there).
func (e *Env) DNS(t testing.TB, args string) {
	t.Helper()
	repo := os.Getenv("E2E_REMOTE_REPO")
	if repo == "" {
		t.Skip("E2E_REMOTE_REPO (path of the cloud-plateform checkout on the server, containing scripts/e2e-dns.sh) not set")
	}
	e.Run(t, "cd "+repo+" && ./scripts/e2e-dns.sh "+args)
	time.Sleep(4 * time.Second) // coredns auto reload is 2s
}
