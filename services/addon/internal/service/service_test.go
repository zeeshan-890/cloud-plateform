package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jp-cloud/addon/internal/catalog"
	"github.com/jp-cloud/addon/internal/clients"
	"github.com/jp-cloud/addon/internal/driver"
	"github.com/jp-cloud/addon/internal/store"
)

// ------------------------------------------------------------------ fakes

type fakeRuntime struct {
	mu          sync.Mutex
	created     []clients.CreateReq
	deleted     []string
	deletePurge []bool
	actions     []string
	health      string // what Status reports
	state       string
	createErr   error
	execs       []driver.ExecSpec
	execOut     map[string]string // first cmd arg -> stdout
	execExit    int
	stdins      [][]byte
	statCount   string
}

func newRT() *fakeRuntime {
	return &fakeRuntime{health: "ready", state: "running", execOut: map[string]string{}, statCount: "7"}
}

func (f *fakeRuntime) Create(_ context.Context, r clients.CreateReq) (clients.CreateResp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return clients.CreateResp{}, f.createErr
	}
	f.created = append(f.created, r)
	return clients.CreateResp{ContainerID: "cid-" + r.Name, Host: "addon-" + r.Name, Port: r.Port}, nil
}
func (f *fakeRuntime) Status(_ context.Context, id string) (clients.RuntimeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clients.RuntimeStatus{ContainerID: id, State: f.state, Running: f.state == "running", Health: f.health}, nil
}
func (f *fakeRuntime) Action(_ context.Context, id, a string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, a+":"+id)
	return nil
}
func (f *fakeRuntime) Delete(_ context.Context, id string, purge bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	f.deletePurge = append(f.deletePurge, purge)
	return nil
}
func (f *fakeRuntime) Exec(_ context.Context, id string, spec driver.ExecSpec, stdin io.Reader, stdout io.Writer, _ time.Duration) (int, string, error) {
	f.mu.Lock()
	f.execs = append(f.execs, spec)
	out, ok := f.execOut[spec.Cmd[0]]
	exit := f.execExit
	f.mu.Unlock()
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		f.mu.Lock()
		f.stdins = append(f.stdins, b)
		f.mu.Unlock()
	}
	if !ok && strings.HasPrefix(strings.Join(spec.Cmd, " "), "psql") {
		out = f.statCount
	}
	_, err := io.WriteString(stdout, out)
	if exit != 0 {
		return exit, "boom", err
	}
	return 0, "", err
}

type fakeSecrets struct {
	mu      sync.Mutex
	data    map[string]map[string]string // env -> key -> value
	failKey string
	deleted []string
}

func newSecrets() *fakeSecrets { return &fakeSecrets{data: map[string]map[string]string{}} }

func (s *fakeSecrets) Upsert(_ context.Context, org, prj, env, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == s.failKey {
		return errors.New("secret store down")
	}
	if s.data[env] == nil {
		s.data[env] = map[string]string{}
	}
	s.data[env][name] = value
	return nil
}
func (s *fakeSecrets) Delete(_ context.Context, org, prj, env, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data[env], name)
	s.deleted = append(s.deleted, env+"/"+name)
	return nil
}
func (s *fakeSecrets) Env(_ context.Context, org, prj, env string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.data[env] {
		out[k] = v
	}
	return out, nil
}
func (s *fakeSecrets) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.data {
		n += len(m)
	}
	return n
}

type fakeStorage struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (f *fakeStorage) Put(_ context.Context, org, p, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objs[p+"/"+key] = b
	return nil
}
func (f *fakeStorage) Get(_ context.Context, p, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objs[p+"/"+key]
	if !ok {
		return nil, clients.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (f *fakeStorage) Delete(_ context.Context, p, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objs, p+"/"+key)
	return nil
}

type memStore struct {
	mu      sync.Mutex
	addons  map[string]*store.Addon
	backups map[string]*store.Backup
}

func (m *memStore) Create(_ context.Context, a *store.Addon) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.addons {
		if o.ProjectID == a.ProjectID && o.Name == a.Name {
			return store.ErrConflict
		}
	}
	a.ID = fmt.Sprintf("00000000-0000-0000-0000-%012d", len(m.addons)+1)
	c := *a
	m.addons[a.ID] = &c
	return nil
}
func (m *memStore) Update(_ context.Context, a *store.Addon) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *a
	m.addons[a.ID] = &c
	return nil
}
func (m *memStore) List(context.Context, string, string, string, bool) ([]store.Addon, error) {
	return nil, nil
}
func (m *memStore) ListByProject(_ context.Context, p string) ([]store.Addon, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Addon
	for _, a := range m.addons {
		if a.ProjectID == p {
			out = append(out, *a)
		}
	}
	return out, nil
}
func (m *memStore) Get(_ context.Context, o, p, id string) (*store.Addon, error) {
	return m.GetByID(context.Background(), id)
}
func (m *memStore) GetByID(_ context.Context, id string) (*store.Addon, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.addons[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	c := *a
	return &c, nil
}
func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.addons, id)
	return nil
}
func (m *memStore) ListStaleProvisioning(context.Context, time.Duration) ([]store.Addon, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Addon
	for _, a := range m.addons {
		if a.Status == store.StatusProvisioning {
			out = append(out, *a)
		}
	}
	return out, nil
}
func (m *memStore) CreateBackup(_ context.Context, b *store.Backup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *b
	m.backups[b.ID] = &c
	return nil
}
func (m *memStore) UpdateBackup(_ context.Context, b *store.Backup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *b
	m.backups[b.ID] = &c
	return nil
}
func (m *memStore) ListBackups(_ context.Context, id string) ([]store.Backup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Backup
	for _, b := range m.backups {
		if b.AddonID == id {
			out = append(out, *b)
		}
	}
	return out, nil
}
func (m *memStore) GetBackup(_ context.Context, a, id string) (*store.Backup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.backups[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	c := *b
	return &c, nil
}
func (m *memStore) DeleteBackup(_ context.Context, id string) error {
	delete(m.backups, id)
	return nil
}

type env struct {
	svc *Service
	rt  *fakeRuntime
	sec *fakeSecrets
	st  *memStore
	obj *fakeStorage
}

const org, prj = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"

func newEnv() *env {
	e := &env{rt: newRT(), sec: newSecrets(), st: &memStore{addons: map[string]*store.Addon{}, backups: map[string]*store.Backup{}}, obj: &fakeStorage{objs: map[string][]byte{}}}
	e.svc = &Service{Store: e.st, Runtime: e.rt, Secrets: e.sec, Storage: e.obj, ReadyTimeout: 300 * time.Millisecond, PollEvery: 10 * time.Millisecond}
	return e
}

func (e *env) provision(name, engine string) (*store.Addon, *Error) {
	return e.svc.Provision(context.Background(), ProvisionReq{OrgID: org, ProjectID: prj, Name: name, Engine: engine, CreatedBy: "u1"})
}

// ------------------------------------------------------------------ provisioning

func TestProvisionPostgresBuildsRealContainer(t *testing.T) {
	e := newEnv()
	a, err := e.provision("primary", "postgres")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if a.Status != store.StatusReady || a.ContainerID != "cid-primary" || a.Kind != store.KindCatalog || a.Endpoint != "addon-primary:5432" {
		t.Fatalf("addon=%+v", a)
	}
	if len(e.rt.created) != 1 {
		t.Fatal("exactly one container must be created")
	}
	c := e.rt.created[0]
	if c.Image != "postgres:16-alpine" || c.Port != 5432 || c.Name != "primary" || c.Volume != "/var/lib/postgresql/data" || c.OrgID != org || c.ProjectID != prj {
		t.Fatalf("container spec: %+v", c)
	}
	pw := c.Env["POSTGRES_PASSWORD"]
	if len(pw) < 24 || c.Env["POSTGRES_USER"] != "app" {
		t.Fatalf("server must be configured with generated credentials: %v", c.Env)
	}
	if c.Health == nil || c.Health.Type != "cmd" || c.Health.TimeoutSeconds != 120 {
		t.Fatalf("readiness probe missing: %+v", c.Health)
	}
	want := "postgres://app:" + pw + "@addon-primary:5432/app?sslmode=disable"
	for _, envName := range []string{"production", "preview"} {
		got := e.sec.data[envName]
		if got["DATABASE_URL"] != want || got["POSTGRES_URL_PRIMARY"] != want {
			t.Fatalf("secrets in %s: %v", envName, got)
		}
	}
	if a.SecretRef != "POSTGRES_URL_PRIMARY" || len(a.SecretKeys) != 2 {
		t.Fatalf("secret refs: %+v", a)
	}
	// the password is never persisted in the add-on record
	if strings.Contains(string(a.Metadata), pw) || strings.Contains(a.ConnectionHint, pw) {
		t.Fatal("password leaked into the record")
	}
}

func TestProvisionRollbackWhenNotReady(t *testing.T) {
	e := newEnv()
	e.rt.health = "starting"
	a, err := e.provision("slowdb", "mysql")
	if a != nil || err == nil || err.Status != 504 || err.Code != "not_ready" {
		t.Fatalf("a=%v err=%v", a, err)
	}
	if !strings.Contains(err.Message, "readiness probe did not pass") {
		t.Fatalf("real error must be surfaced: %s", err.Message)
	}
	if len(e.rt.deleted) != 1 || !e.rt.deletePurge[0] {
		t.Fatalf("container must be removed with its volume: %v %v", e.rt.deleted, e.rt.deletePurge)
	}
	if e.sec.count() != 0 {
		t.Fatalf("no secret may be written before readiness: %v", e.sec.data)
	}
	if len(e.st.addons) != 0 {
		t.Fatal("record must be rolled back")
	}
	// the name is free again
	e.rt.health = "ready"
	if _, err := e.provision("slowdb", "mysql"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestProvisionRollbackWhenContainerExits(t *testing.T) {
	e := newEnv()
	e.rt.state, e.rt.health = "exited", "none"
	_, err := e.provision("crashy", "redis")
	if err == nil || !strings.Contains(err.Message, "exited during startup") {
		t.Fatalf("err=%v", err)
	}
	if len(e.rt.deleted) != 1 || len(e.st.addons) != 0 {
		t.Fatal("rollback incomplete")
	}
}

func TestProvisionRollbackWhenRuntimeFails(t *testing.T) {
	e := newEnv()
	e.rt.createErr = errors.New("no such image")
	_, err := e.provision("xyz1", "postgres")
	if err == nil || err.Status != 502 || !strings.Contains(err.Message, "no such image") {
		t.Fatalf("err=%v", err)
	}
	if len(e.st.addons) != 0 || e.sec.count() != 0 {
		t.Fatal("rollback incomplete")
	}
}

func TestProvisionRollbackWhenSecretWriteFails(t *testing.T) {
	e := newEnv()
	e.sec.failKey = "POSTGRES_URL_PRIMARY"
	_, err := e.provision("primary", "postgres")
	if err == nil || err.Code != "secret_error" {
		t.Fatalf("err=%v", err)
	}
	if e.sec.count() != 0 {
		t.Fatalf("already written secrets must be removed: %v", e.sec.data)
	}
	if len(e.rt.deleted) != 1 || len(e.st.addons) != 0 {
		t.Fatal("container/record must be rolled back")
	}
}

func TestProvisionConflictingSecretIsNeverOverwritten(t *testing.T) {
	e := newEnv()
	e.sec.data["production"] = map[string]string{"DATABASE_URL": "postgres://existing"}
	_, err := e.provision("primary", "postgres")
	if err == nil || err.Status != 409 || err.Code != "secret_conflict" || !strings.Contains(err.Message, "secret_name") {
		t.Fatalf("err=%v", err)
	}
	if len(e.rt.created) != 0 {
		t.Fatal("no container may be started when secrets would collide")
	}
	if e.sec.data["production"]["DATABASE_URL"] != "postgres://existing" {
		t.Fatal("existing secret was overwritten")
	}
	// explicit secret_name resolves the conflict
	a, err := e.svc.Provision(context.Background(), ProvisionReq{OrgID: org, ProjectID: prj, Name: "primary", Engine: "postgres", SecretName: "ANALYTICS_DATABASE_URL"})
	if err != nil {
		t.Fatal(err)
	}
	if e.sec.data["production"]["ANALYTICS_DATABASE_URL"] == "" || e.sec.data["production"]["DATABASE_URL"] != "postgres://existing" {
		t.Fatalf("%v %v", a, e.sec.data)
	}
}

func TestProvisionValidation(t *testing.T) {
	e := newEnv()
	cases := []struct {
		name, engine string
		status       int
		code         string
	}{
		{"Bad Name!", "postgres", 400, "validation_error"},
		{"ab", "postgres", 400, "validation_error"},
		{"1abc", "postgres", 400, "validation_error"},
		{"good-name", "no-such-engine", 400, "unknown_engine"},
		{"good-name", "wordpress", 422, "not_provisionable"},
		{"good-name", "sqlite", 422, "not_provisionable"},
		{"good-name", "minio", 422, "not_provisionable"},
	}
	for _, c := range cases {
		_, err := e.provision(c.name, c.engine)
		if err == nil || err.Status != c.status || err.Code != c.code {
			t.Errorf("%s/%s: err=%v", c.name, c.engine, err)
		}
	}
	if len(e.rt.created) != 0 {
		t.Fatal("nothing may start for invalid requests")
	}
	if _, err := e.svc.Provision(context.Background(), ProvisionReq{OrgID: org, ProjectID: prj, Name: "okname", Engine: "redis", Envs: []string{"prod; rm"}}); err == nil || err.Status != 400 {
		t.Errorf("bad env accepted: %v", err)
	}
	if _, err := e.svc.Provision(context.Background(), ProvisionReq{OrgID: org, ProjectID: prj, Name: "okname", Engine: "redis", SecretName: "bad name"}); err == nil || err.Status != 400 {
		t.Errorf("bad secret name accepted: %v", err)
	}
}

func TestDuplicateNameConflicts(t *testing.T) {
	e := newEnv()
	if _, err := e.provision("cache", "redis"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.provision("cache", "valkey"); err == nil || err.Status != 409 {
		t.Fatalf("err=%v", err)
	}
}

func TestCustomAddonDoesNotTouchCatalog(t *testing.T) {
	e := newEnv()
	before := len(catalogIDs())
	a, err := e.svc.Provision(context.Background(), ProvisionReq{OrgID: org, ProjectID: prj, Name: "my-app",
		Custom: &CustomSpec{Image: "ghcr.io/acme/app:1.2", Port: 8080, Env: map[string]string{"MODE": "x"}, Volume: "/data"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != store.KindCustom || a.Engine != "custom" || e.rt.created[0].Image != "ghcr.io/acme/app:1.2" || e.rt.created[0].Env["MODE"] != "x" {
		t.Fatalf("%+v %+v", a, e.rt.created[0])
	}
	if e.sec.data["production"]["MY_APP_URL"] != "http://addon-my-app:8080" {
		t.Fatalf("%v", e.sec.data)
	}
	if len(catalogIDs()) != before {
		t.Fatal("custom add-ons must never mutate the global catalog")
	}
}

func TestValidateCustom(t *testing.T) {
	ok := CustomSpec{Image: "nginx:1.27", Port: 80}
	if ValidateCustom(ok) != nil {
		t.Fatal("valid spec rejected")
	}
	bad := []CustomSpec{
		{Image: "", Port: 80}, {Image: "NGINX", Port: 80}, {Image: "nginx latest", Port: 80}, {Image: "a;rm -rf /", Port: 80},
		{Image: "-v /:/host alpine", Port: 80}, {Image: "nginx", Port: 0}, {Image: "nginx", Port: 70000},
		{Image: "nginx", Port: 80, Env: map[string]string{"bad name": "x"}}, {Image: "nginx", Port: 80, Env: map[string]string{"A": "x\x00y"}},
		{Image: "nginx", Port: 80, Volume: "relative"}, {Image: "nginx", Port: 80, Volume: "/data/../etc"}, {Image: "nginx", Port: 80, Volume: "/var/run/docker.sock:/x"},
		{Image: "nginx", Port: 80, Volume: "/"},
	}
	for i, c := range bad {
		if ValidateCustom(c) == nil {
			t.Errorf("case %d accepted: %+v", i, c)
		}
	}
	for _, img := range []string{"redis", "library/redis:7", "ghcr.io/org/app:v1.2.3", "registry.example.com:5000/team/app:latest", "quay.io/a/b@sha256:" + strings.Repeat("a", 64)} {
		if ValidateCustom(CustomSpec{Image: img, Port: 1}) != nil {
			t.Errorf("image %s rejected", img)
		}
	}
}

// ------------------------------------------------------------------ lifecycle

func TestLifecycleAndDelete(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("primary", "postgres")
	if l := e.svc.LiveStatus(context.Background(), a); l.Status != "running" {
		t.Fatalf("live=%+v", l)
	}
	e.rt.health = "unhealthy"
	if l := e.svc.LiveStatus(context.Background(), a); l.Status != "unhealthy" {
		t.Fatalf("live=%+v", l)
	}
	if err := e.svc.Action(context.Background(), a, "stop"); err != nil || a.Status != store.StatusStopped {
		t.Fatalf("%v %s", err, a.Status)
	}
	if err := e.svc.Action(context.Background(), a, "start"); err != nil || a.Status != store.StatusReady {
		t.Fatalf("%v", err)
	}
	if err := e.svc.Action(context.Background(), a, "explode"); err == nil {
		t.Fatal("unknown action accepted")
	}
	if err := e.svc.Delete(context.Background(), a, false, "u1"); err != nil {
		t.Fatal(err)
	}
	if e.rt.deletePurge[len(e.rt.deletePurge)-1] {
		t.Fatal("default delete must keep the volume")
	}
	if e.sec.count() != 0 || len(e.st.addons) != 0 {
		t.Fatalf("secrets/record not removed: %v", e.sec.data)
	}
	e.rt.health = "ready"
	a2, _ := e.provision("second", "redis")
	_ = e.svc.Delete(context.Background(), a2, true, "u1")
	if !e.rt.deletePurge[len(e.rt.deletePurge)-1] {
		t.Fatal("purge=true must remove the volume")
	}
}

func TestDeleteProjectCascade(t *testing.T) {
	e := newEnv()
	e.provision("one1", "postgres")
	e.provision("two2", "redis")
	n, fails := e.svc.DeleteProject(context.Background(), prj)
	if n != 2 || len(fails) != 0 || len(e.rt.deleted) != 2 || !e.rt.deletePurge[0] || len(e.st.addons) != 0 {
		t.Fatalf("n=%d fails=%v deleted=%v", n, fails, e.rt.deleted)
	}
}

func TestSweepCleansInterruptedProvisioning(t *testing.T) {
	e := newEnv()
	e.st.addons["x"] = &store.Addon{ID: "x", Status: store.StatusProvisioning, ContainerID: "cid-x"}
	if n := e.svc.Sweep(context.Background()); n != 1 {
		t.Fatal(n)
	}
	if e.st.addons["x"].Status != store.StatusFailed || len(e.rt.deleted) != 1 {
		t.Fatalf("%+v", e.st.addons["x"])
	}
}

// ------------------------------------------------------------------ connection

func TestConnectionMasksPassword(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("primary", "postgres")
	pw := e.rt.created[0].Env["POSTGRES_PASSWORD"]
	c, err := e.svc.Connection(context.Background(), a, false)
	if err != nil || strings.Contains(c.URL, pw) || !c.Masked || c.PublicURL != nil || c.Host != "addon-primary" || c.Port != 5432 {
		t.Fatalf("masked connection leaked: %+v %v", c, err)
	}
	c, err = e.svc.Connection(context.Background(), a, true)
	if err != nil || !strings.Contains(c.URL, pw) || c.Masked {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestMaskURL(t *testing.T) {
	m := MaskURL("postgres://app:s3cr3t@host:5432/db?x=1")
	if strings.Contains(m, "s3cr3t") || !strings.Contains(m, "app:") || !strings.Contains(m, "host:5432") {
		t.Fatal(m)
	}
	if MaskURL("http://host:80") != "http://host:80" {
		t.Fatal("no credentials: unchanged")
	}
}

// ------------------------------------------------------------------ backups

func TestBackupAndRestoreVerified(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("primary", "postgres")
	e.rt.execOut["pg_dump"] = "PGDMP-binary-dump-content"
	b, err := e.svc.Backup(context.Background(), a, "u1")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if b.Status != "completed" || b.SizeBytes != int64(len("PGDMP-binary-dump-content")) || len(b.SHA256) != 64 || b.ObjectsCount != 7 {
		t.Fatalf("manifest: %+v", b)
	}
	key := prj + "/" + b.ObjectKey
	if string(e.obj.objs[key]) != "PGDMP-binary-dump-content" || !strings.HasPrefix(b.ObjectKey, "_backups/addon/") {
		t.Fatalf("object not stored: %v", e.obj.objs)
	}
	dumpSpec := e.rt.execs[1]
	pw := e.rt.created[0].Env["POSTGRES_PASSWORD"]
	if dumpSpec.Env["PGPASSWORD"] != pw || strings.Contains(strings.Join(dumpSpec.Cmd, " "), pw) {
		t.Fatalf("credentials must go via env only: %+v", dumpSpec)
	}

	rb, err := e.svc.Restore(context.Background(), a, b.ID, "u1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if rb.LastRestoreStatus != "verified" {
		t.Fatalf("%+v", rb)
	}
	var restoreIn []byte
	for _, s := range e.rt.stdins {
		if len(s) > 0 {
			restoreIn = s
		}
	}
	if string(restoreIn) != "PGDMP-binary-dump-content" {
		t.Fatalf("restore must stream the stored dump into the container, got %q", restoreIn)
	}
}

func TestRestoreDetectsCorruptedObject(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("primary", "postgres")
	e.rt.execOut["pg_dump"] = "good-data"
	b, _ := e.svc.Backup(context.Background(), a, "u1")
	e.obj.objs[prj+"/"+b.ObjectKey] = []byte("tampered")
	before := len(e.rt.execs)
	_, err := e.svc.Restore(context.Background(), a, b.ID, "u1")
	if err == nil || err.Code != "checksum_mismatch" {
		t.Fatalf("err=%v", err)
	}
	if len(e.rt.execs) != before {
		t.Fatal("nothing may be executed in the container when the checksum does not match")
	}
}

func TestRestoreVerificationFailsOnCountMismatch(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("primary", "postgres")
	e.rt.execOut["pg_dump"] = "data"
	b, _ := e.svc.Backup(context.Background(), a, "u1")
	e.rt.statCount = "3"
	_, err := e.svc.Restore(context.Background(), a, b.ID, "u1")
	if err == nil || err.Code != "verification_failed" {
		t.Fatalf("err=%v", err)
	}
}

func TestBackupFailureLeavesNoObject(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("primary", "postgres")
	e.rt.execOut["pg_dump"] = "partial"
	e.rt.execExit = 1
	_, err := e.svc.Backup(context.Background(), a, "u1")
	if err == nil || err.Code != "backup_failed" {
		t.Fatalf("err=%v", err)
	}
	if len(e.obj.objs) != 0 {
		t.Fatalf("partial dump must be deleted: %v", e.obj.objs)
	}
	for _, b := range e.st.backups {
		if b.Status != "failed" || b.Error == "" {
			t.Fatalf("%+v", b)
		}
	}
}

func TestBackupUnsupportedEngines(t *testing.T) {
	e := newEnv()
	a, _ := e.provision("cache", "memcached")
	if _, err := e.svc.Backup(context.Background(), a, "u"); err == nil || err.Status != 422 {
		t.Fatalf("%v", err)
	}
	r, _ := e.provision("rds", "redis")
	e.rt.execOut["sh"] = "RDB"
	b, err := e.svc.Backup(context.Background(), r, "u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Restore(context.Background(), r, b.ID, "u"); err == nil || err.Code != "not_supported" {
		t.Fatalf("redis restore must be refused: %v", err)
	}
}

func TestSQLOnlyPostgres(t *testing.T) {
	e := newEnv()
	pg, _ := e.provision("primary", "postgres")
	e.rt.execOut["psql"] = "ok\n"
	out, err := e.svc.SQL(context.Background(), pg, "select 1")
	if err != nil || out != "ok\n" {
		t.Fatalf("%q %v", out, err)
	}
	rd, _ := e.provision("cache", "redis")
	if _, err := e.svc.SQL(context.Background(), rd, "select 1"); err == nil {
		t.Fatal("sql on redis must fail")
	}
}

func TestURLsParse(t *testing.T) {
	for _, eng := range []string{"postgres", "mysql", "mariadb", "mongodb", "redis", "valkey", "dragonfly", "rabbitmq", "clickhouse", "meilisearch", "elasticsearch", "opensearch", "nats", "qdrant", "couchdb"} {
		e := newEnv()
		a, err := e.provision("t-"+eng, eng)
		if err != nil {
			t.Fatalf("%s: %v", eng, err)
		}
		raw := e.sec.data["production"][a.SecretRef]
		if _, perr := url.Parse(raw); perr != nil || !strings.Contains(raw, "addon-t-"+eng) {
			t.Errorf("%s: bad URL %q", eng, raw)
		}
	}
}

func catalogIDs() []string {
	var ids []string
	for _, it := range catalog.All() {
		ids = append(ids, it.ID)
	}
	return ids
}
