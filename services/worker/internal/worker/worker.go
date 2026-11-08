// Package worker turns a queued build job into a verified, scanned, pushed image and
// reports the real outcome. A failed clone, build, push or blocked scan FAILS the
// deployment; there is no mock fallback in real mode.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/worker/internal/builder"
	"github.com/jp-cloud/worker/internal/dockerfile"
	"github.com/jp-cloud/worker/internal/gitx"
	"github.com/jp-cloud/worker/internal/jpyaml"
	"github.com/jp-cloud/worker/internal/redact"
	"github.com/jp-cloud/worker/internal/scan"
)

// Outcome tells the consumer what to do with the stream message.
type Outcome int

const (
	Done  Outcome = iota // final (success, failure or canceled): ack
	Retry                // infrastructure problem / shutdown: leave pending
)

// Docker abstracts the docker CLI wrapper.
type Docker interface {
	Available(ctx context.Context) error
	Build(ctx context.Context, imageRef, dockerfile, contextDir string, buildArgs map[string]string, out io.Writer) error
	Push(ctx context.Context, imageRef string, out io.Writer) error
	Remove(ctx context.Context, imageRef string)
}

// Worker holds configuration and dependencies.
type Worker struct {
	BuildURL      string
	DeploymentURL string
	RepositoryURL string
	RegistryAPI   string // registry metadata service (internal/images)
	RegistryHost  string // host used in image refs, resolvable by the HOST daemon (e.g. 127.0.0.1:5000)
	RegistryCheck string // optional http(s) base of registry:2 for push verification

	Simulate bool
	HTTP     *http.Client
	Log      *slog.Logger
	Redactor *redact.Redactor

	Git    func(ctx context.Context, o gitx.Options) (*gitx.Result, error)
	Docker Docker
	Scan   func(ctx context.Context, imageRef string) *scan.Summary

	WorkRoot        string
	MinFreeBytes    uint64
	MaxRepoBytes    int64
	CloneTimeout    time.Duration
	BuildTimeout    time.Duration
	PushTimeout     time.Duration
	AllowedGitHosts []string
	CancelPoll      time.Duration

	// AllowFileClone is a TEST-ONLY switch (never set by cmd/main.go).
	AllowFileClone bool
}

// Job is the validated payload of a build.queued event.
type Job struct {
	BuildID, OrgID, ProjectID, DeploymentID, RepoID string
	GitSHA, GitBranch, FullName                     string
	PRNumber                                        int
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	tagRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// ParseJob validates the untrusted event payload.
func ParseJob(env events.Envelope) (Job, error) {
	s := func(k string) string { v, _ := env.Payload[k].(string); return v }
	j := Job{BuildID: s("build_id"), OrgID: s("org_id"), ProjectID: s("project_id"), DeploymentID: s("deployment_id"), RepoID: s("repo_id"),
		GitSHA: s("git_sha"), GitBranch: s("git_branch"), FullName: s("full_name")}
	if f, ok := env.Payload["pr_number"].(float64); ok {
		j.PRNumber = int(f)
	}
	for name, id := range map[string]string{"build_id": j.BuildID, "org_id": j.OrgID, "project_id": j.ProjectID} {
		if !uuidRe.MatchString(id) {
			return j, fmt.Errorf("invalid %s", name)
		}
	}
	for name, id := range map[string]string{"deployment_id": j.DeploymentID, "repo_id": j.RepoID} {
		if id != "" && !uuidRe.MatchString(id) {
			return j, fmt.Errorf("invalid %s", name)
		}
	}
	if j.GitBranch != "" && !gitx.ValidBranch(j.GitBranch) {
		return j, errors.New("invalid git_branch")
	}
	if j.GitSHA != "" && j.GitSHA != "HEAD" && !gitx.ValidSHA(j.GitSHA) {
		return j, errors.New("invalid git_sha")
	}
	if j.PRNumber < 0 || j.PRNumber > 10_000_000 {
		return j, errors.New("invalid pr_number")
	}
	return j, nil
}

// buildErr is a deterministic failure of this build (reported to the user, not retried).
type buildErr struct{ msg string }

func (e *buildErr) Error() string { return e.msg }

func fail(format string, a ...any) error { return &buildErr{msg: fmt.Sprintf(format, a...)} }

type cloneInfo struct {
	CloneURL   string `json:"clone_url"`
	AuthType   string `json:"auth_type"` // none | token | deploy_key
	Token      string `json:"token"`
	PrivateKey string `json:"private_key"`
	FullName   string `json:"full_name"`
	Default    string `json:"default_branch"`
	// RootDir is the folder of a (mono)repo this project builds; "" = the repository root.
	RootDir string `json:"root_dir"`
}

type result struct {
	Image     string
	Framework string
	SHA       string
	Scan      *scan.Summary
	JPConfig  json.RawMessage
}

func (w *Worker) defaults() {
	if w.HTTP == nil {
		w.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if w.Log == nil {
		w.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if w.Redactor == nil {
		w.Redactor = redact.New()
	}
	if w.Git == nil {
		w.Git = gitx.Clone
	}
	if w.CloneTimeout <= 0 {
		w.CloneTimeout = 3 * time.Minute
	}
	if w.BuildTimeout <= 0 {
		w.BuildTimeout = 15 * time.Minute
	}
	if w.PushTimeout <= 0 {
		w.PushTimeout = 5 * time.Minute
	}
	if w.MaxRepoBytes <= 0 {
		w.MaxRepoBytes = 512 << 20
	}
	if w.MinFreeBytes == 0 {
		w.MinFreeBytes = 2 << 30
	}
	if w.CancelPoll <= 0 {
		w.CancelPoll = 5 * time.Second
	}
}

// Process runs one job end to end and reports the outcome.
func (w *Worker) Process(ctx context.Context, j Job) Outcome {
	w.defaults()
	log := w.Log.With("build_id", j.BuildID, "project_id", j.ProjectID)

	if st, err := w.buildStatus(ctx, j.BuildID); err == nil && isTerminal(st) {
		log.Info("build already finished; skipping redelivery", "status", st)
		return Done
	} else if err != nil && ctx.Err() == nil {
		log.Warn("cannot read build status", "err", err)
		return Retry
	}
	if err := w.patchBuild(ctx, j.BuildID, map[string]any{"status": "running", "logs": "worker claimed job\n"}); err != nil {
		log.Warn("cannot mark build running", "err", err)
		return Retry
	}

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var userCanceled bool
	var mu sync.Mutex
	go w.watchCancel(jobCtx, j.BuildID, func() { mu.Lock(); userCanceled = true; mu.Unlock(); cancel() })

	sink := newSink(w, j.BuildID)
	stopFlush := sink.start(jobCtx)
	res, err := w.run(jobCtx, j, sink)
	stopFlush()
	sink.Flush(context.WithoutCancel(ctx))

	mu.Lock()
	canceled := userCanceled
	mu.Unlock()
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer rcancel()
	switch {
	case err == nil:
		patch := map[string]any{"status": "succeeded", "framework": res.Framework, "image_ref": res.Image,
			"scan_status": res.Scan.Status, "scan": res.Scan}
		if e := w.patchBuild(rctx, j.BuildID, patch); e != nil {
			log.Error("cannot report success", "err", e)
			return Retry
		}
		w.registerImage(rctx, j, res)
		if j.DeploymentID != "" {
			if e := w.patchDeployment(rctx, j.DeploymentID, map[string]any{"status": "deploying", "image_ref": res.Image, "build_id": j.BuildID,
				"git_sha": res.SHA, "jp_config": res.JPConfig, "framework": res.Framework}); e != nil {
				log.Error("cannot report deployment", "err", e)
				return Retry
			}
		}
		log.Info("build succeeded", "image", res.Image, "framework", res.Framework, "scan", res.Scan.Status)
		return Done
	case canceled:
		log.Info("build canceled by user")
		return Done
	case ctx.Err() != nil:
		log.Warn("worker shutting down mid-build; job stays pending for another worker")
		return Retry
	}
	var be *buildErr
	msg := "internal error: " + err.Error()
	if errors.As(err, &be) {
		msg = be.msg
	}
	msg = truncate(w.Redactor.String(msg), 500)
	log.Warn("build failed", "err", msg)
	if e := w.patchBuild(rctx, j.BuildID, map[string]any{"status": "failed", "error": msg, "logs": "BUILD FAILED: " + msg + "\n"}); e != nil {
		return Retry
	}
	if j.DeploymentID != "" {
		if e := w.patchDeployment(rctx, j.DeploymentID, map[string]any{"status": "failed", "error": msg, "build_id": j.BuildID}); e != nil {
			return Retry
		}
	}
	return Done
}

func isTerminal(s string) bool { return s == "succeeded" || s == "failed" || s == "canceled" }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func short(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// run is the pipeline; every returned *buildErr is a user-visible failure.
func (w *Worker) run(ctx context.Context, j Job, out *sink) (*result, error) {
	logf := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

	if w.Simulate {
		logf("[SIMULATED BUILD] BUILD_MODE=simulate: nothing is cloned, built or pushed")
		return &result{Image: fmt.Sprintf("%s/%s/%s:sim-%s", w.RegistryHost, short(j.OrgID), short(j.ProjectID), short(j.BuildID)),
			Framework: "simulated", SHA: j.GitSHA, Scan: &scan.Summary{Scanner: "none", Status: "simulated", Counts: map[string]int{}, Findings: []scan.Finding{}},
			JPConfig: json.RawMessage(`{}`)}, nil
	}
	if w.Docker == nil {
		return nil, errors.New("docker is not configured")
	}
	if err := w.Docker.Available(ctx); err != nil {
		return nil, fmt.Errorf("docker daemon unavailable: %w", err)
	}
	if j.RepoID == "" {
		return nil, fail("no repository is connected to this project")
	}

	// workspace + disk quota
	if err := os.MkdirAll(w.WorkRoot, 0o700); err != nil {
		return nil, err
	}
	if free, err := builder.FreeBytes(w.WorkRoot); err == nil && free < w.MinFreeBytes {
		return nil, fail("build host is low on disk space (%d MiB free); try again later", free>>20)
	}
	dir, err := os.MkdirTemp(w.WorkRoot, "build-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	ci, err := w.cloneInfo(ctx, j)
	if err != nil {
		return nil, fail("cannot load repository settings: %v", err)
	}
	w.Redactor.Add(ci.Token, ci.PrivateKey)
	branch := j.GitBranch
	if branch == "" && j.PRNumber == 0 {
		branch = ci.Default
	}
	logf("cloning %s (%s)", ci.FullName, firstNonEmpty(branch, fmt.Sprintf("PR #%d", j.PRNumber)))
	gctx, gcancel := context.WithTimeout(ctx, w.CloneTimeout)
	cres, err := w.Git(gctx, gitx.Options{
		CloneURL: ci.CloneURL, Branch: branch, SHA: j.GitSHA, PRNumber: j.PRNumber, Dir: dir,
		Token: ci.Token, SSHKey: ci.PrivateKey, Timeout: w.CloneTimeout, MaxRepoBytes: w.MaxRepoBytes,
		Out: w.Redactor.Writer(out), AllowedHosts: w.AllowedGitHosts, AllowFile: w.AllowFileClone,
	})
	gcancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail("clone failed: %s", w.Redactor.String(err.Error()))
	}
	logf("checked out %s", cres.SHA)

	// Monorepo: everything below (jp.yaml, Dockerfile detection, build context, relative paths) is relative to
	// the project's root directory, so two projects of one repository build two different folders.
	appDir := dir
	if ci.RootDir != "" {
		if appDir, err = w.resolveRootDir(dir, ci.RootDir); err != nil {
			return nil, err
		}
		logf("root directory: %s", ci.RootDir)
	}
	cfg, err := jpyaml.Load(appDir)
	if err != nil {
		return nil, fail("%v", err)
	}
	buildCtx := appDir
	if cfg.Context != "" {
		if buildCtx, err = jpyaml.ResolveInside(appDir, cfg.Context); err != nil {
			return nil, fail("build.context: %v", err)
		}
	}
	dockerfilePath, framework, err := w.prepareDockerfile(appDir, buildCtx, cfg, logf)
	if err != nil {
		return nil, err
	}

	tag := cres.SHA
	if len(tag) > 12 {
		tag = tag[:12]
	}
	if j.PRNumber > 0 {
		tag = fmt.Sprintf("pr%d-%s", j.PRNumber, tag)
	}
	if !tagRe.MatchString(tag) {
		return nil, fail("invalid image tag")
	}
	imageRef := fmt.Sprintf("%s/%s/%s:%s", w.RegistryHost, short(j.OrgID), short(j.ProjectID), tag)

	logf("building image %s", imageRef)
	bctx, bcancel := context.WithTimeout(ctx, w.BuildTimeout)
	buildOut := w.Redactor.Writer(out)
	err = w.Docker.Build(bctx, imageRef, dockerfilePath, buildCtx, cfg.BuildEnv, buildOut)
	_ = buildOut.Close()
	bcancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail("image build failed: %s", w.Redactor.String(err.Error()))
	}

	logf("pushing %s", imageRef)
	pctx, pcancel := context.WithTimeout(ctx, w.PushTimeout)
	pushOut := w.Redactor.Writer(out)
	err = w.Docker.Push(pctx, imageRef, pushOut)
	_ = pushOut.Close()
	pcancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail("image push failed (the image is not in the registry): %s", w.Redactor.String(err.Error()))
	}
	if err := w.verifyPushed(ctx, imageRef); err != nil {
		return nil, fail("pushed image not found in registry: %v", err)
	}

	sum := &scan.Summary{Scanner: "trivy", Status: scan.StatusDisabled, Counts: map[string]int{}, Findings: []scan.Finding{}}
	if w.Scan != nil {
		logf("scanning image for HIGH/CRITICAL vulnerabilities")
		sum = w.Scan(ctx, imageRef)
		logf("scan: %s (critical=%d high=%d)%s", sum.Status, sum.Critical, sum.High, map[bool]string{true: " - " + sum.Reason, false: ""}[sum.Reason != ""])
	}
	if cfg.BlockOnCritical && (sum.Critical > 0 || sum.Status == scan.StatusUnavailable) {
		why := fmt.Sprintf("%d critical vulnerabilities found", sum.Critical)
		if sum.Status == scan.StatusUnavailable {
			why = "the security scan could not run (" + sum.Reason + ")"
		}
		return nil, fail("deployment blocked by security.block_on_critical: %s", why)
	}
	return &result{Image: imageRef, Framework: framework, SHA: cres.SHA, Scan: sum, JPConfig: cfg.JSON}, nil
}

// resolveRootDir returns the absolute, symlink-resolved project folder inside the checkout, or a user-facing
// failure naming the folder (a deleted/renamed folder is the usual cause).
func (w *Worker) resolveRootDir(checkout, rel string) (string, error) {
	if err := jpyaml.SafeRelPath(rel); err != nil {
		return "", fail("root directory: %v", err)
	}
	fi, err := os.Stat(filepath.Join(checkout, filepath.FromSlash(rel)))
	switch {
	case err != nil:
		return "", fail("root directory %q does not exist in this commit: check Project > Repository > Deployment settings", rel)
	case !fi.IsDir():
		return "", fail("root directory %q is a file, not a folder", rel)
	}
	abs, err := jpyaml.ResolveInside(checkout, rel)
	if err != nil {
		return "", fail("root directory: %v", err)
	}
	return abs, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// prepareDockerfile honours jp.yaml (build.dockerfile), then a Dockerfile in the build
// context, and only then generates one.
func (w *Worker) prepareDockerfile(root, ctxDir string, cfg *jpyaml.Config, logf func(string, ...any)) (path, framework string, err error) {
	framework = "docker"
	if cfg.Dockerfile != "" {
		p, err := jpyaml.ResolveInside(root, cfg.Dockerfile)
		if err != nil {
			return "", "", fail("build.dockerfile: %v", err)
		}
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			return "", "", fail("build.dockerfile %q not found", cfg.Dockerfile)
		}
		logf("using Dockerfile %s (jp.yaml)", cfg.Dockerfile)
		return p, framework, nil
	}
	if fi, err := os.Stat(filepath.Join(ctxDir, "Dockerfile")); err == nil && fi.Mode().IsRegular() {
		logf("using Dockerfile from repository")
		return filepath.Join(ctxDir, "Dockerfile"), framework, nil
	}
	plan, err := dockerfile.Detect(ctxDir, dockerfile.Options{Runtime: cfg.Runtime, Port: cfg.Port, BuildCommand: cfg.BuildCommand})
	if err != nil {
		return "", "", fail("%v", err)
	}
	if err := plan.Write(ctxDir); err != nil {
		return "", "", err
	}
	logf("no Dockerfile in repository; generated one for %s (port %d)", plan.Framework, plan.Port)
	return filepath.Join(ctxDir, "Dockerfile.jp"), plan.Framework, nil
}

// ---------------------------------------------------------------- service calls

func (w *Worker) do(ctx context.Context, method, u string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if out != nil && resp.StatusCode < 300 {
		_ = json.Unmarshal(b, out)
	}
	return resp.StatusCode, nil
}

func (w *Worker) buildStatus(ctx context.Context, id string) (string, error) {
	var out struct {
		Build struct {
			Status string `json:"status"`
		} `json:"build"`
	}
	code, err := w.do(ctx, http.MethodGet, strings.TrimRight(w.BuildURL, "/")+"/internal/builds/"+url.PathEscape(id), nil, &out)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("build service status %d", code)
	}
	return out.Build.Status, nil
}

func (w *Worker) patchBuild(ctx context.Context, id string, body map[string]any) error {
	code, err := w.do(ctx, http.MethodPost, strings.TrimRight(w.BuildURL, "/")+"/internal/builds/"+url.PathEscape(id)+"/status", body, nil)
	if err != nil {
		return err
	}
	if code >= 300 && code != http.StatusConflict {
		return fmt.Errorf("build service status %d", code)
	}
	return nil
}

func (w *Worker) patchDeployment(ctx context.Context, id string, body map[string]any) error {
	code, err := w.do(ctx, http.MethodPost, strings.TrimRight(w.DeploymentURL, "/")+"/internal/deployments/"+url.PathEscape(id)+"/status", body, nil)
	if err != nil {
		return err
	}
	if code >= 300 && code != http.StatusConflict { // 409: deployment already finished/canceled
		return fmt.Errorf("deployment service status %d", code)
	}
	return nil
}

func (w *Worker) cloneInfo(ctx context.Context, j Job) (*cloneInfo, error) {
	u := fmt.Sprintf("%s/internal/repos/%s/clone-info?org_id=%s&project_id=%s", strings.TrimRight(w.RepositoryURL, "/"),
		url.PathEscape(j.RepoID), url.QueryEscape(j.OrgID), url.QueryEscape(j.ProjectID))
	var ci cloneInfo
	code, err := w.do(ctx, http.MethodGet, u, nil, &ci)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("repository service status %d", code)
	}
	if ci.CloneURL == "" {
		return nil, errors.New("repository has no clone url")
	}
	return &ci, nil
}

func (w *Worker) registerImage(ctx context.Context, j Job, r *result) {
	if w.RegistryAPI == "" {
		return
	}
	code, err := w.do(ctx, http.MethodPost, strings.TrimRight(w.RegistryAPI, "/")+"/internal/images", map[string]any{
		"org_id": j.OrgID, "project_id": j.ProjectID, "image_ref": r.Image, "framework": r.Framework, "git_sha": r.SHA}, nil)
	if err != nil || code >= 300 {
		w.Log.Warn("register image failed", "err", err, "status", code)
	}
}

// verifyPushed checks that the manifest exists in registry:2 when REGISTRY_CHECK_URL is set.
func (w *Worker) verifyPushed(ctx context.Context, imageRef string) error {
	if w.RegistryCheck == "" {
		return nil
	}
	rest := strings.TrimPrefix(imageRef, w.RegistryHost+"/")
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return errors.New("bad image ref")
	}
	parts := strings.Split(rest[:i], "/")
	for k := range parts {
		parts[k] = url.PathEscape(parts[k])
	}
	u := strings.TrimRight(w.RegistryCheck, "/") + "/v2/" + strings.Join(parts, "/") + "/manifests/" + url.PathEscape(rest[i+1:])
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry answered %d", resp.StatusCode)
	}
	return nil
}

// watchCancel polls the build record and calls onCancel when the user canceled it.
func (w *Worker) watchCancel(ctx context.Context, buildID string, onCancel func()) {
	t := time.NewTicker(w.CancelPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if st, err := w.buildStatus(ctx, buildID); err == nil && st == "canceled" {
				onCancel()
				return
			}
		}
	}
}

// ---------------------------------------------------------------- log sink

const maxLogTotal = 2 << 20

// sink collects redacted log output and flushes it to the build service in chunks.
type sink struct {
	w     *Worker
	id    string
	mu    sync.Mutex
	buf   bytes.Buffer
	total int
}

func newSink(w *Worker, id string) *sink { return &sink{w: w, id: id} }

// Write implements io.Writer (already redacted by the callers' writers; redacted again here).
func (s *sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total >= maxLogTotal {
		return len(p), nil
	}
	chunk := s.w.Redactor.String(string(p))
	if s.total+len(chunk) > maxLogTotal {
		chunk = chunk[:maxLogTotal-s.total] + "\n[log truncated]\n"
	}
	s.total += len(chunk)
	s.buf.WriteString(chunk)
	return len(p), nil
}

// Flush sends buffered output.
func (s *sink) Flush(ctx context.Context) {
	s.mu.Lock()
	data := s.buf.String()
	s.buf.Reset()
	s.mu.Unlock()
	if data == "" {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.w.patchBuild(fctx, s.id, map[string]any{"logs": data}); err != nil {
		s.w.Log.Debug("log flush failed", "err", err)
	}
}

// start flushes every 2 seconds until ctx ends; the returned func stops and joins it.
func (s *sink) start(ctx context.Context) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.Flush(ctx)
			}
		}
	}()
	return func() { close(done); <-stopped }
}
