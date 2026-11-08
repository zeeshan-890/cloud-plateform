// Package service implements add-on provisioning, lifecycle, connection info and backups.
// It is used by the public handlers and by the internal API the database service calls.
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jp-cloud/addon/internal/catalog"
	"github.com/jp-cloud/addon/internal/clients"
	"github.com/jp-cloud/addon/internal/driver"
	"github.com/jp-cloud/addon/internal/store"
)

// Error carries an HTTP-mappable failure.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func errf(status int, code, f string, a ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(f, a...)}
}

var (
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,28}[a-z0-9]$`)
	secretRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,127}$`)
	// docker image reference: [registry[:port]/]path[:tag][@sha256:digest]
	imageRe   = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`)
	validEnvs = map[string]bool{"production": true, "preview": true, "staging": true, "development": true}
)

// DefaultEnvs receive the credentials unless the caller narrows them.
var DefaultEnvs = []string{"production", "preview"}

type Service struct {
	Store   store.Store
	Runtime clients.Runtime
	Secrets clients.Secrets
	Storage clients.Storage
	Log     *slog.Logger
	Publish func(ctx context.Context, typ, actor, org string, payload map[string]any)

	ReadyTimeout time.Duration // default 120s
	PollEvery    time.Duration // default 2s
	ExecTimeout  time.Duration // default 30m
	Now          func() time.Time

	inflight atomic.Int32
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) readyTimeout() time.Duration {
	if s.ReadyTimeout > 0 {
		return s.ReadyTimeout
	}
	return 120 * time.Second
}

func (s *Service) pollEvery() time.Duration {
	if s.PollEvery > 0 {
		return s.PollEvery
	}
	return 2 * time.Second
}

func (s *Service) emit(ctx context.Context, typ, actor, org string, p map[string]any) {
	if s.Publish != nil {
		s.Publish(ctx, typ, actor, org, p)
	}
}

// ValidName reports whether name is a valid add-on slug.
func ValidName(n string) bool { return nameRe.MatchString(n) }

// CustomSpec describes a user supplied container (stored per project only).
type CustomSpec struct {
	Image  string
	Port   int
	Env    map[string]string
	Volume string
}

// ValidateCustom checks a custom spec.
func ValidateCustom(c CustomSpec) *Error {
	if len(c.Image) > 255 || !imageRe.MatchString(c.Image) {
		return errf(400, "validation_error", "image is not a valid image reference")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errf(400, "validation_error", "port must be between 1 and 65535")
	}
	if len(c.Env) > 50 {
		return errf(400, "validation_error", "at most 50 environment variables")
	}
	for k, v := range c.Env {
		if !envNameRe.MatchString(k) {
			return errf(400, "validation_error", "invalid environment variable name %q", k)
		}
		if len(v) > 8192 || strings.ContainsRune(v, 0) {
			return errf(400, "validation_error", "environment variable %s has an invalid value", k)
		}
	}
	if c.Volume != "" {
		if !strings.HasPrefix(c.Volume, "/") || strings.Contains(c.Volume, "..") || len(c.Volume) > 255 || strings.ContainsAny(c.Volume, ":\x00\n,") || c.Volume == "/" {
			return errf(400, "validation_error", "volume must be an absolute container path without '..'")
		}
	}
	return nil
}

// ProvisionReq is a provisioning request.
type ProvisionReq struct {
	OrgID, ProjectID string
	Name, Engine     string
	SecretName       string
	Envs             []string
	CreatedBy        string
	Kind             string
	Options          driver.Options
	Custom           *CustomSpec
}

type kv struct{ Key, Value string }

// Provision runs the full flow: validate -> reserve -> start container -> readiness -> secrets -> ready.
// Any failure rolls back container, volume, secrets and the reserved row, and returns the real error.
func (s *Service) Provision(ctx context.Context, req ProvisionReq) (*store.Addon, *Error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !nameRe.MatchString(name) {
		return nil, errf(400, "validation_error", "name must be 3-30 chars: lowercase letters, digits and '-', starting with a letter")
	}
	envs, e := normalizeEnvs(req.Envs)
	if e != nil {
		return nil, e
	}
	kind := req.Kind
	if kind == "" {
		kind = store.KindCatalog
	}

	var plan driver.Plan
	engine := strings.ToLower(strings.TrimSpace(req.Engine))
	if req.Custom != nil {
		if e := ValidateCustom(*req.Custom); e != nil {
			return nil, e
		}
		kind, engine = store.KindCustom, "custom"
		plan = driver.CustomPlan(name, req.Custom.Image, req.Custom.Port, req.Custom.Env, req.Custom.Volume)
	} else {
		it, ok := catalog.Get(engine)
		if !ok {
			return nil, errf(400, "unknown_engine", "engine %q is not in the catalog; use the custom add-on endpoint for your own image", engine)
		}
		if !it.Provisionable {
			return nil, errf(422, "not_provisionable", "%s cannot be provisioned: %s", it.Name, catalog.ReasonText[it.Reason])
		}
		p, err := driver.Build(it, name, driver.NewPassword(32), req.Options)
		if err != nil {
			return nil, errf(500, "internal", "%v", err)
		}
		plan = p
	}

	// Secret keys: default (or explicit) + a unique per-add-on key + extras.
	unique := catalog.SecretName(engine, name)
	if req.Custom != nil {
		unique = strings.ToUpper(strings.NewReplacer("-", "_").Replace(name)) + "_URL"
	}
	primary := strings.TrimSpace(req.SecretName)
	if primary != "" && !secretRe.MatchString(primary) {
		return nil, errf(400, "validation_error", "secret_name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	explicit := primary != ""
	if primary == "" {
		primary = plan.DefaultKey
	}
	var secrets []kv
	secrets = append(secrets, kv{unique, plan.URL})
	if primary != "" && primary != unique {
		secrets = append(secrets, kv{primary, plan.URL})
	}
	suffixes := make([]string, 0, len(plan.Extras))
	for k := range plan.Extras {
		suffixes = append(suffixes, k)
	}
	sort.Strings(suffixes)
	for _, suf := range suffixes {
		secrets = append(secrets, kv{unique + "_" + suf, plan.Extras[suf]})
	}

	// Reserve the name (unique per project) before touching anything else.
	a := &store.Addon{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Kind: kind, Engine: engine, Name: name, Status: store.StatusProvisioning,
		Endpoint: fmt.Sprintf("%s:%d", plan.Host, plan.Port), Image: plan.Image, SecretRef: unique, Envs: envs,
		ConnectionHint: fmt.Sprintf("Use $%s inside the project network (host %s, port %d); add-ons are not publicly exposed.", firstNonEmpty(primary, unique), plan.Host, plan.Port),
	}
	if req.CreatedBy != "" {
		cb := req.CreatedBy
		a.CreatedBy = &cb
	}
	for _, k := range secrets {
		a.SecretKeys = append(a.SecretKeys, k.Key)
	}
	if err := s.Store.Create(ctx, a); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, errf(409, "conflict", "an add-on named %q already exists in this project", name)
		}
		return nil, errf(500, "internal", "could not reserve add-on")
	}

	// From here on work with a detached context so a client disconnect cannot leave half a deployment.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.readyTimeout()+90*time.Second)
	defer cancel()
	var written []struct{ env, key string }
	cid := ""
	fail := func(e *Error) (*store.Addon, *Error) {
		s.rollback(a, cid, written)
		return nil, e
	}

	// Conflicts with existing secrets: never overwrite silently.
	for _, env := range envs {
		existing, err := s.Secrets.Env(work, req.OrgID, req.ProjectID, env)
		if err != nil {
			return fail(errf(502, "bad_gateway", "cannot check existing secrets: %v", err))
		}
		for _, k := range secrets {
			if _, taken := existing[k.Key]; taken {
				if k.Key == primary && !explicit {
					return fail(errf(409, "secret_conflict", "this project already has a secret named %s in %s; pass \"secret_name\" with a different name", k.Key, env))
				}
				return fail(errf(409, "secret_conflict", "secret %s already exists in %s", k.Key, env))
			}
		}
	}

	rreq := clients.CreateReq{OrgID: req.OrgID, ProjectID: req.ProjectID, Name: name, Image: plan.Image, Port: plan.Port,
		Env: plan.Env, Volume: plan.Volume, Command: plan.Command, Health: &plan.Health}
	if rreq.Env == nil {
		rreq.Env = map[string]string{}
	}
	cr, err := s.Runtime.Create(work, rreq)
	if err != nil {
		return fail(errf(502, "runtime_error", "could not start container: %v", err))
	}
	cid = cr.ContainerID
	a.ContainerID = cid
	a.VolumeName = ""
	_ = s.Store.Update(work, a)

	if msg := s.waitReady(work, cid); msg != "" {
		return fail(errf(504, "not_ready", "%s", msg))
	}
	for _, env := range envs {
		for _, k := range secrets {
			if err := s.Secrets.Upsert(work, req.OrgID, req.ProjectID, env, k.Key, k.Value); err != nil {
				written = append(written, struct{ env, key string }{env, k.Key}) // may be partially written: remove too
				return fail(errf(502, "secret_error", "could not store credentials: %v", err))
			}
			written = append(written, struct{ env, key string }{env, k.Key})
		}
	}
	a.Status = store.StatusReady
	a.Error = ""
	if err := s.Store.Update(work, a); err != nil {
		return fail(errf(500, "internal", "could not record add-on"))
	}
	s.emit(work, "addon.created", req.CreatedBy, req.OrgID, map[string]any{"project_id": req.ProjectID, "addon_id": a.ID, "engine": engine, "name": name, "kind": kind})
	return a, nil
}

func normalizeEnvs(in []string) ([]string, *Error) {
	if len(in) == 0 {
		return append([]string(nil), DefaultEnvs...), nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if !validEnvs[e] {
			return nil, errf(400, "validation_error", "unknown environment %q (production, preview, staging, development)", e)
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// waitReady polls the runtime until the readiness probe passes; returns "" on success.
func (s *Service) waitReady(ctx context.Context, cid string) string {
	deadline := time.Now().Add(s.readyTimeout())
	var last clients.RuntimeStatus
	for {
		st, err := s.Runtime.Status(ctx, cid)
		switch {
		case errors.Is(err, clients.ErrNotFound):
			return "container disappeared while starting"
		case err == nil:
			last = st
			if st.State == "exited" || st.State == "dead" {
				return fmt.Sprintf("container exited during startup (exit code %d) %s", st.ExitCode, st.Error)
			}
			if st.RestartCount >= 3 {
				return fmt.Sprintf("container keeps restarting (%d restarts, state %s) %s", st.RestartCount, st.State, st.Error)
			}
			if st.Running && (st.Health == "ready" || st.Health == "healthy") {
				return ""
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Sprintf("readiness probe did not pass within %s (state=%s health=%s) %s", s.readyTimeout(), last.State, last.Health, last.Error)
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.pollEvery()):
		}
	}
}

func (s *Service) rollback(a *store.Addon, cid string, written []struct{ env, key string }) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, w := range written {
		if err := s.Secrets.Delete(ctx, a.OrgID, a.ProjectID, w.env, w.key); err != nil {
			s.log().Error("rollback secret", "key", w.key, "err", err)
		}
	}
	if cid != "" {
		if err := s.Runtime.Delete(ctx, cid, true); err != nil {
			s.log().Error("rollback container", "container", cid, "err", err)
		}
	}
	if err := s.Store.Delete(ctx, a.ID); err != nil {
		s.log().Error("rollback row", "addon", a.ID, "err", err)
	}
}

// Sweep cleans add-ons whose provisioning was interrupted (service restart).
func (s *Service) Sweep(ctx context.Context) int {
	stale, err := s.Store.ListStaleProvisioning(ctx, s.readyTimeout()+3*time.Minute)
	if err != nil {
		s.log().Error("sweep", "err", err)
		return 0
	}
	n := 0
	for i := range stale {
		a := stale[i]
		if a.ContainerID != "" {
			_ = s.Runtime.Delete(ctx, a.ContainerID, true)
		}
		a.Status, a.Error = store.StatusFailed, "provisioning was interrupted; container removed. Delete this add-on and create it again."
		if s.Store.Update(ctx, &a) == nil {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- lifecycle

// Live is the runtime view of an add-on.
type Live struct {
	Status string `json:"status"` // running | unhealthy | stopped | starting | missing | unknown
	State  string `json:"state,omitempty"`
	Health string `json:"health,omitempty"`
	Error  string `json:"error,omitempty"`
}

// LiveStatus asks the runtime for the real container state.
func (s *Service) LiveStatus(ctx context.Context, a *store.Addon) Live {
	if a.ContainerID == "" || a.Status == store.StatusFailed {
		return Live{Status: map[bool]string{true: "failed", false: "missing"}[a.Status == store.StatusFailed], Error: a.Error}
	}
	st, err := s.Runtime.Status(ctx, a.ContainerID)
	if errors.Is(err, clients.ErrNotFound) {
		return Live{Status: "missing", Error: "container not found on the runtime"}
	}
	if err != nil {
		return Live{Status: "unknown", Error: err.Error()}
	}
	l := Live{State: st.State, Health: st.Health, Error: st.Error}
	switch {
	case !st.Running && (st.State == "exited" || st.State == "created" || st.State == "dead"):
		l.Status = "stopped"
	case st.Running && (st.Health == "ready" || st.Health == "healthy"):
		l.Status = "running"
	case st.Running && st.Health == "unhealthy":
		l.Status = "unhealthy"
	case st.Running:
		l.Status = "starting"
	default:
		l.Status = "unknown"
	}
	return l
}

// Action performs stop/start/restart.
func (s *Service) Action(ctx context.Context, a *store.Addon, action string) *Error {
	if action != "stop" && action != "start" && action != "restart" {
		return errf(400, "bad_request", "unknown action")
	}
	if a.ContainerID == "" || a.Status == store.StatusFailed || a.Status == store.StatusProvisioning {
		return errf(409, "invalid_state", "add-on is %s", a.Status)
	}
	if err := s.Runtime.Action(ctx, a.ContainerID, action); err != nil {
		return errf(502, "runtime_error", "%v", err)
	}
	if action == "stop" {
		a.Status = store.StatusStopped
	} else {
		a.Status = store.StatusReady
	}
	_ = s.Store.Update(ctx, a)
	return nil
}

// Delete removes the container (and the volume when purge), the secrets and the record.
func (s *Service) Delete(ctx context.Context, a *store.Addon, purge bool, actor string) *Error {
	if a.ContainerID != "" {
		if err := s.Runtime.Delete(ctx, a.ContainerID, purge); err != nil {
			return errf(502, "runtime_error", "could not remove container: %v", err)
		}
	}
	var warn []string
	for _, env := range a.Envs {
		for _, k := range a.SecretKeys {
			if err := s.Secrets.Delete(ctx, a.OrgID, a.ProjectID, env, k); err != nil {
				warn = append(warn, err.Error())
			}
		}
	}
	if backups, err := s.Store.ListBackups(ctx, a.ID); err == nil && s.Storage != nil {
		for _, b := range backups {
			_ = s.Storage.Delete(ctx, a.ProjectID, b.ObjectKey)
		}
	}
	if err := s.Store.Delete(ctx, a.ID); err != nil {
		return errf(500, "internal", "delete failed")
	}
	if len(warn) > 0 {
		s.log().Warn("secret cleanup incomplete", "addon", a.ID, "errors", warn)
	}
	s.emit(ctx, "addon.deleted", actor, a.OrgID, map[string]any{"project_id": a.ProjectID, "addon_id": a.ID, "engine": a.Engine, "name": a.Name, "purged": purge})
	return nil
}

// DeleteProject is the project-delete cascade. It never stops at the first error.
func (s *Service) DeleteProject(ctx context.Context, projectID string) (removed int, failures []string) {
	list, err := s.Store.ListByProject(ctx, projectID)
	if err != nil {
		return 0, []string{err.Error()}
	}
	for i := range list {
		a := list[i]
		if a.ContainerID != "" {
			if err := s.Runtime.Delete(ctx, a.ContainerID, true); err != nil {
				failures = append(failures, fmt.Sprintf("addon %s container: %v", a.Name, err))
				continue
			}
		}
		if err := s.Store.Delete(ctx, a.ID); err != nil {
			failures = append(failures, fmt.Sprintf("addon %s record: %v", a.Name, err))
			continue
		}
		removed++
	}
	return
}

// ---------------------------------------------------------------- connection

// Connection is the connection info of an add-on.
type Connection struct {
	URL       string            `json:"url"`
	Masked    bool              `json:"masked"`
	Host      string            `json:"host"`
	Port      int               `json:"port"`
	PublicURL *string           `json:"public_url"`
	Secrets   []string          `json:"secret_keys"`
	Extras    map[string]string `json:"extras,omitempty"`
}

func (s *Service) secretValue(ctx context.Context, a *store.Addon) (map[string]string, *Error) {
	env := "production"
	if len(a.Envs) > 0 {
		env = a.Envs[0]
	}
	all, err := s.Secrets.Env(ctx, a.OrgID, a.ProjectID, env)
	if err != nil {
		return nil, errf(502, "bad_gateway", "secret service: %v", err)
	}
	return all, nil
}

// Connection returns connection info; the password is masked unless reveal is true.
func (s *Service) Connection(ctx context.Context, a *store.Addon, reveal bool) (*Connection, *Error) {
	all, e := s.secretValue(ctx, a)
	if e != nil {
		return nil, e
	}
	raw, ok := all[a.SecretRef]
	if !ok {
		return nil, errf(404, "not_found", "connection secret %s not found", a.SecretRef)
	}
	host, port := splitEndpoint(a.Endpoint)
	c := &Connection{URL: raw, Masked: !reveal, Host: host, Port: port, Secrets: a.SecretKeys}
	if !reveal {
		c.URL = MaskURL(raw)
	} else {
		for _, k := range a.SecretKeys {
			if k != a.SecretRef && all[k] != "" && !strings.HasPrefix(all[k], strings.SplitN(raw, "://", 2)[0]+"://") {
				if c.Extras == nil {
					c.Extras = map[string]string{}
				}
				c.Extras[k] = all[k]
			}
		}
	}
	return c, nil
}

// MaskURL hides the password in a URL.
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); !has {
		return raw
	}
	user := u.User.Username()
	u.User = url.UserPassword(user, "xxxxx")
	return strings.Replace(u.String(), "xxxxx", "••••••••", 1)
}

func splitEndpoint(ep string) (string, int) {
	i := strings.LastIndexByte(ep, ':')
	if i < 0 {
		return ep, 0
	}
	p, _ := strconv.Atoi(ep[i+1:])
	return ep[:i], p
}

// creds recovers the generated credentials from the stored connection URL.
func (s *Service) creds(ctx context.Context, a *store.Addon) (driver.Creds, *Error) {
	all, e := s.secretValue(ctx, a)
	if e != nil {
		return driver.Creds{}, e
	}
	c, err := driver.CredsFromURL(all[a.SecretRef])
	if err != nil || (c.Password == "") {
		return driver.Creds{}, errf(500, "internal", "cannot recover credentials")
	}
	return c, nil
}

func (s *Service) driverOf(a *store.Addon) string {
	if it, ok := catalog.Get(a.Engine); ok {
		return it.Driver
	}
	return ""
}

// ---------------------------------------------------------------- backups

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func (s *Service) execTimeout() time.Duration {
	if s.ExecTimeout > 0 {
		return s.ExecTimeout
	}
	return 30 * time.Minute
}

func (s *Service) stats(ctx context.Context, cid string, spec *driver.ExecSpec) int64 {
	if spec == nil {
		return -1
	}
	var out bytes.Buffer
	code, _, err := s.Runtime.Exec(ctx, cid, *spec, nil, &out, 2*time.Minute)
	if err != nil || code != 0 {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out.String()), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// BackupSupported reports whether a real backup can be taken for the add-on.
func (s *Service) BackupSupported(a *store.Addon) (driver.BackupKind, bool) {
	k, ok := driver.Backup(s.driverOf(a))
	return k, ok && k.Dump != nil
}

// Backup dumps the add-on inside its container and streams the dump to storage with a SHA-256.
func (s *Service) Backup(ctx context.Context, a *store.Addon, actor string) (*store.Backup, *Error) {
	kind, ok := s.BackupSupported(a)
	if !ok {
		return nil, errf(422, "not_supported", "backups are not supported for engine %s", a.Engine)
	}
	if s.Storage == nil {
		return nil, errf(503, "unavailable", "storage service not configured")
	}
	if a.ContainerID == "" || a.Status != store.StatusReady {
		return nil, errf(409, "invalid_state", "add-on must be ready (is %s)", a.Status)
	}
	c, e := s.creds(ctx, a)
	if e != nil {
		return nil, e
	}
	b := &store.Backup{AddonID: a.ID, OrgID: a.OrgID, ProjectID: a.ProjectID, Engine: a.Engine}
	if actor != "" {
		b.CreatedBy = &actor
	}
	b.ID = newID()
	b.ObjectKey = fmt.Sprintf("_backups/addon/%s/%s-%s.%s", a.ID, time.Now().UTC().Format("20060102T150405Z"), b.ID[:8], kind.Ext)
	if err := s.Store.CreateBackup(ctx, b); err != nil {
		return nil, errf(500, "internal", "could not record backup")
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.execTimeout()+time.Minute)
	defer cancel()

	var statsSpec *driver.ExecSpec
	if kind.Stats != nil {
		sp := kind.Stats(c)
		statsSpec = &sp
	}
	b.ObjectsCount = s.stats(work, a.ContainerID, statsSpec)

	pr, pw := io.Pipe()
	h := sha256.New()
	cw := &countWriter{}
	putDone := make(chan error, 1)
	go func() {
		err := s.Storage.Put(work, a.OrgID, a.ProjectID, b.ObjectKey, io.TeeReader(io.TeeReader(pr, h), cw))
		_, _ = io.Copy(io.Discard, pr) // never leave the writer blocked
		putDone <- err
	}()
	code, stderr, execErr := s.Runtime.Exec(work, a.ContainerID, kind.Dump(c), nil, pw, s.execTimeout())
	switch {
	case execErr != nil:
		pw.CloseWithError(execErr)
	case code != 0:
		pw.CloseWithError(fmt.Errorf("dump exited with code %d", code))
	default:
		pw.Close()
	}
	putErr := <-putDone

	now := time.Now().UTC()
	b.CompletedAt = &now
	var failure string
	switch {
	case errors.Is(execErr, clients.ErrExecUnsupported):
		failure = "runtime does not support exec; backups are unavailable"
	case execErr != nil:
		failure = "dump failed: " + execErr.Error()
	case code != 0:
		failure = fmt.Sprintf("dump exited with code %d: %s", code, strings.TrimSpace(stderr))
	case putErr != nil:
		failure = "upload failed: " + putErr.Error()
	case cw.n == 0:
		failure = "dump produced no data"
	}
	if failure != "" {
		b.Status, b.Error = "failed", failure
		_ = s.Storage.Delete(context.WithoutCancel(ctx), a.ProjectID, b.ObjectKey)
		_ = s.Store.UpdateBackup(work, b)
		return b, errf(502, "backup_failed", "%s", failure)
	}
	b.Status, b.SizeBytes, b.SHA256 = "completed", cw.n, hex.EncodeToString(h.Sum(nil))
	if err := s.Store.UpdateBackup(work, b); err != nil {
		return nil, errf(500, "internal", "could not record backup")
	}
	s.emit(work, "addon.backup.completed", actor, a.OrgID, map[string]any{"addon_id": a.ID, "backup_id": b.ID, "sha256": b.SHA256, "size": b.SizeBytes})
	return b, nil
}

// Restore verifies the stored artifact against its SHA-256, restores it and checks the result.
func (s *Service) Restore(ctx context.Context, a *store.Addon, backupID, actor string) (*store.Backup, *Error) {
	kind, ok := s.BackupSupported(a)
	if !ok || kind.Restore == nil {
		return nil, errf(422, "not_supported", "restore is not supported for engine %s", a.Engine)
	}
	b, err := s.Store.GetBackup(ctx, a.ID, backupID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errf(404, "not_found", "backup not found")
	}
	if err != nil {
		return nil, errf(500, "internal", "lookup failed")
	}
	if b.Status != "completed" {
		return nil, errf(409, "invalid_state", "backup is %s", b.Status)
	}
	if a.ContainerID == "" || a.Status != store.StatusReady {
		return nil, errf(409, "invalid_state", "add-on must be ready (is %s)", a.Status)
	}
	c, e := s.creds(ctx, a)
	if e != nil {
		return nil, e
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.execTimeout()+5*time.Minute)
	defer cancel()
	record := func(status, msg string) {
		now := time.Now().UTC()
		b.LastRestoreAt, b.LastRestoreStatus, b.LastRestoreError = &now, status, msg
		_ = s.Store.UpdateBackup(work, b)
	}
	fail := func(code int, c2, msg string) (*store.Backup, *Error) {
		record("failed", msg)
		return b, errf(code, c2, "%s", msg)
	}

	// 1. checksum
	rc, err := s.Storage.Get(work, a.ProjectID, b.ObjectKey)
	if err != nil {
		return fail(502, "storage_error", "cannot read backup object: "+err.Error())
	}
	h := sha256.New()
	n, err := io.Copy(h, rc)
	rc.Close()
	if err != nil {
		return fail(502, "storage_error", "reading backup failed: "+err.Error())
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != b.SHA256 || n != b.SizeBytes {
		return fail(409, "checksum_mismatch", fmt.Sprintf("backup object does not match its manifest (sha256 %s, expected %s)", got, b.SHA256))
	}
	// 2. archive integrity
	if kind.Verify != nil {
		rc, err := s.Storage.Get(work, a.ProjectID, b.ObjectKey)
		if err != nil {
			return fail(502, "storage_error", err.Error())
		}
		var out bytes.Buffer
		code, stderr, err := s.Runtime.Exec(work, a.ContainerID, kind.Verify(c), rc, &out, 10*time.Minute)
		rc.Close()
		if err != nil || code != 0 {
			return fail(422, "invalid_archive", fmt.Sprintf("archive integrity check failed (code %d): %v %s", code, err, strings.TrimSpace(stderr)))
		}
	}
	// 3. restore
	rc, err = s.Storage.Get(work, a.ProjectID, b.ObjectKey)
	if err != nil {
		return fail(502, "storage_error", err.Error())
	}
	var out bytes.Buffer
	code, stderr, err := s.Runtime.Exec(work, a.ContainerID, kind.Restore(c), rc, &out, s.execTimeout())
	rc.Close()
	if errors.Is(err, clients.ErrExecUnsupported) {
		return fail(501, "not_supported", "runtime does not support exec")
	}
	if err != nil || code != 0 {
		return fail(502, "restore_failed", fmt.Sprintf("restore failed (code %d): %v %s", code, err, strings.TrimSpace(stderr)))
	}
	// 4. verify result
	if kind.Stats != nil && b.ObjectsCount >= 0 {
		sp := kind.Stats(c)
		if got := s.stats(work, a.ContainerID, &sp); got != b.ObjectsCount {
			return fail(409, "verification_failed", fmt.Sprintf("restore finished but object count is %d (backup had %d)", got, b.ObjectsCount))
		}
	}
	record("verified", "")
	s.emit(work, "addon.backup.restored", actor, a.OrgID, map[string]any{"addon_id": a.ID, "backup_id": b.ID})
	return b, nil
}

// SQL runs one statement through psql inside a postgres add-on (used for diagnostics).
func (s *Service) SQL(ctx context.Context, a *store.Addon, sql string) (string, *Error) {
	if s.driverOf(a) != "postgres" {
		return "", errf(422, "not_supported", "SQL execution is only available for postgres")
	}
	if a.ContainerID == "" || a.Status != store.StatusReady {
		return "", errf(409, "invalid_state", "add-on must be ready (is %s)", a.Status)
	}
	c, e := s.creds(ctx, a)
	if e != nil {
		return "", e
	}
	var out bytes.Buffer
	spec := driver.ExecSpec{Cmd: []string{"psql", "-h", "127.0.0.1", "-U", c.User, "-d", c.DB, "-X", "-A", "-t", "-F", "\t", "-v", "ON_ERROR_STOP=1", "-c", sql},
		Env: map[string]string{"PGPASSWORD": c.Password}}
	code, stderr, err := s.Runtime.Exec(ctx, a.ContainerID, spec, nil, LimitWriter(&out, 4<<20), 60*time.Second)
	if err != nil {
		return "", errf(502, "runtime_error", "%v", err)
	}
	if code != 0 {
		return "", errf(422, "sql_error", "%s", strings.TrimSpace(stderr))
	}
	return out.String(), nil
}
