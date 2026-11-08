// Package loop is the scheduler's event / timer engine. It starts deployments on the
// runtime when a build produced an image, supervises running instances with real Docker
// inspection (via the runtime service), fires cron jobs as one-off containers and
// triggers cleanups. Every goroutine is tied to Run's context and joined before Run
// returns; every outbound call is signed, timed out and size limited.
package loop

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/scheduler/internal/cron"
	"github.com/redis/go-redis/v9"
)

// Config configures the scheduler loop.
type Config struct {
	Redis             *redis.Client
	RuntimeURL        string
	RegistryURL       string
	DeploymentURL     string
	DomainURL         string
	PreviewBaseDomain string
	HTTP              *http.Client // short calls
	DeployHTTP        *http.Client // runtime start (health gate can take minutes)
	Log               *slog.Logger
	Consumer          string
	HealthEvery       time.Duration
	CleanupEvery      time.Duration
	Cron              *cron.Store
	PreviewTTL        time.Duration
	ImageTTL          time.Duration

	MaxEventRetries int           // transient failures before an event is dead-lettered
	ClaimIdle       time.Duration // pending messages idle longer than this are reclaimed
	ClaimEvery      time.Duration
	MaxRestarts     int           // restart attempts before giving up on an instance
	BackoffBase     time.Duration // first restart delay, doubled each attempt
	BackoffMax      time.Duration
	UnhealthyProbes int // consecutive failing probes before a running instance is restarted
	ReadBlock       time.Duration
}

// Scheduler runs the loops.
type Scheduler struct {
	cfg Config

	supMu sync.Mutex
	sup   map[string]*supState // bounded: pruned to the instances the runtime still lists
}

// New applies defaults.
func New(cfg Config) *Scheduler {
	if cfg.HTTP == nil {
		cfg.HTTP = internalauth.NewClient(20 * time.Second)
	}
	if cfg.DeployHTTP == nil {
		cfg.DeployHTTP = internalauth.NewClient(12 * time.Minute)
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Consumer == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = "scheduler"
		}
		cfg.Consumer = h
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&cfg.HealthEvery, 20*time.Second)
	def(&cfg.CleanupEvery, time.Hour)
	def(&cfg.PreviewTTL, 72*time.Hour)
	def(&cfg.ImageTTL, 168*time.Hour)
	def(&cfg.ClaimIdle, 15*time.Minute)
	def(&cfg.ClaimEvery, 30*time.Second)
	def(&cfg.ReadBlock, 5*time.Second)
	def(&cfg.BackoffBase, 10*time.Second)
	def(&cfg.BackoffMax, 5*time.Minute)
	if cfg.MaxEventRetries <= 0 {
		cfg.MaxEventRetries = 3
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 5
	}
	if cfg.UnhealthyProbes <= 0 {
		cfg.UnhealthyProbes = 3
	}
	if cfg.PreviewBaseDomain == "" && !strings.EqualFold(os.Getenv("JP_ENV"), "production") {
		cfg.PreviewBaseDomain = "preview.jp.localhost"
	}
	return &Scheduler{cfg: cfg, sup: map[string]*supState{}}
}

// Run blocks until ctx is cancelled and every loop has exited.
func (s *Scheduler) Run(ctx context.Context) error {
	for _, g := range []struct{ topic, group string }{
		{events.TopicDeploy, events.SchedulerConsumerGroup},
		{events.TopicCleanup, events.CleanupConsumerGroup},
		{events.TopicJobs, events.JobsConsumerGroup},
	} {
		if err := events.EnsureGroup(ctx, s.cfg.Redis, g.topic, g.group); err != nil {
			return err
		}
	}
	s.cfg.Log.Info("scheduler started", "consumer", s.cfg.Consumer)

	var wg sync.WaitGroup
	run := func(f func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); f(ctx) }()
	}
	run(s.healthLoop)
	run(s.cleanupPublisher)
	run(s.cronTicker)
	run(func(c context.Context) {
		s.consume(c, events.TopicCleanup, events.CleanupConsumerGroup, s.handleCleanup, nil)
	})
	run(func(c context.Context) { s.consume(c, events.TopicJobs, events.JobsConsumerGroup, s.handleJob, nil) })
	run(func(c context.Context) {
		s.consume(c, events.TopicDeploy, events.SchedulerConsumerGroup, s.handleDeploy, s.deadLetterDeploy)
	})
	wg.Wait()
	return ctx.Err()
}

// ---------------------------------------------------------------- generic consumer

// permanent marks an error that must not be retried.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

func perm(format string, a ...any) error { return permanent{fmt.Errorf(format, a...)} }

// consume reads a stream with a consumer group. Failed messages stay pending and are
// retried (also after a crash, via XAUTOCLAIM) until MaxEventRetries, then dead-lettered.
func (s *Scheduler) consume(ctx context.Context, topic, group string, handle func(context.Context, redis.XMessage) error, dead func(context.Context, redis.XMessage, error)) {
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		var msgs []redis.XMessage
		if time.Since(lastClaim) >= s.cfg.ClaimEvery {
			lastClaim = time.Now()
			claimed, _, err := s.cfg.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: topic, Group: group, Consumer: s.cfg.Consumer, MinIdle: s.cfg.ClaimIdle, Start: "0-0", Count: 10,
			}).Result()
			if err == nil {
				msgs = append(msgs, claimed...)
			} else if ctx.Err() == nil {
				s.cfg.Log.Warn("xautoclaim", "topic", topic, "err", err)
			}
		}
		fresh, err := events.ReadGroup(ctx, s.cfg.Redis, topic, group, s.cfg.Consumer, 1, s.cfg.ReadBlock)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.cfg.Log.Error("read group", "topic", topic, "err", err)
			sleepCtx(ctx, 2*time.Second)
			continue
		}
		msgs = append(msgs, fresh...)
		for _, msg := range msgs {
			if ctx.Err() != nil {
				return
			}
			err := handle(ctx, msg)
			var p permanent
			switch {
			case err == nil:
				_ = events.Ack(ctx, s.cfg.Redis, topic, group, msg.ID)
			case errors.As(err, &p):
				s.cfg.Log.Warn("event failed permanently", "topic", topic, "id", msg.ID, "err", err)
				if dead != nil {
					dead(ctx, msg, err)
				}
				_ = events.Ack(ctx, s.cfg.Redis, topic, group, msg.ID)
			default:
				n, _ := s.cfg.Redis.Incr(ctx, "jp:sched:retry:"+topic+":"+msg.ID).Result()
				_ = s.cfg.Redis.Expire(ctx, "jp:sched:retry:"+topic+":"+msg.ID, 6*time.Hour).Err()
				if int(n) >= s.cfg.MaxEventRetries {
					s.cfg.Log.Error("event dead-lettered", "topic", topic, "id", msg.ID, "attempts", n, "err", err)
					if dead != nil {
						dead(ctx, msg, err)
					}
					_ = events.Ack(ctx, s.cfg.Redis, topic, group, msg.ID)
				} else {
					s.cfg.Log.Warn("event failed, will retry", "topic", topic, "id", msg.ID, "attempt", n, "err", err)
					// leave pending: reclaimed after ClaimIdle; shorten by re-adding to our own PEL
				}
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// ---------------------------------------------------------------- outbound helpers

type httpResult struct {
	Status int
	Body   []byte
}

func (s *Scheduler) do(ctx context.Context, hc *http.Client, method, u string, body any) (*httpResult, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return &httpResult{Status: resp.StatusCode, Body: b}, nil
}

func join(base string, parts ...string) string {
	u := strings.TrimRight(base, "/")
	for _, p := range parts {
		u += "/" + url.PathEscape(p)
	}
	return u
}

// ---------------------------------------------------------------- deploy events

func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }

func payloadBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true") || t == "1"
	}
	return false
}

func (s *Scheduler) handleDeploy(ctx context.Context, msg redis.XMessage) error {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return perm("unparseable envelope: %v", err)
	}
	if env.Type != events.TypeDeployUpdated || str(env.Payload, "status") != "deploying" {
		return nil // billing / notification events share this stream
	}
	return s.startFromEvent(ctx, env.Payload)
}

func (s *Scheduler) startFromEvent(ctx context.Context, p map[string]any) error {
	orgID, projectID, deploymentID := str(p, "org_id"), str(p, "project_id"), str(p, "deployment_id")
	imageRef := str(p, "image_ref")
	if imageRef == "" || orgID == "" || projectID == "" || deploymentID == "" {
		return perm("deploy event missing image_ref/org_id/project_id/deployment_id")
	}
	preview := payloadBool(p["preview"])
	strategy := str(p, "strategy")
	if strategy == "" {
		strategy = "rolling"
	}
	spec, _ := p["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	s.cfg.Log.Info("starting deployment", "deployment", deploymentID, "project", projectID, "image", imageRef, "strategy", strategy, "preview", preview)

	res, err := s.do(ctx, s.cfg.DeployHTTP, http.MethodPost, join(s.cfg.RuntimeURL, "internal", "runtime", "start"), map[string]any{
		"org_id": orgID, "project_id": projectID, "deployment_id": deploymentID, "image_ref": imageRef,
		"strategy": strategy, "preview": preview, "no_secrets": payloadBool(p["fork"]), "spec": spec,
	})
	if err != nil {
		return fmt.Errorf("runtime unreachable: %w", err) // transient
	}
	switch {
	case res.Status == http.StatusCreated || res.Status == http.StatusOK:
	case res.Status >= 500 && res.Status != http.StatusUnprocessableEntity, res.Status == http.StatusTooManyRequests:
		return fmt.Errorf("runtime start status %d", res.Status)
	default: // 4xx / 422: the runtime refused or the new version failed its health gate
		msg := runtimeError(res.Body)
		s.report(ctx, deploymentID, "failed", msg, "")
		return nil
	}

	previewURL := ""
	if preview {
		previewURL = s.provisionPreviewURL(ctx, orgID, projectID, deploymentID, str(p, "git_branch"), intOf(p["pr_number"]))
	}
	if err := s.reportErr(ctx, deploymentID, "ready", "", previewURL); err != nil {
		return err // transient; runtime deploy is idempotent so the retry is safe
	}
	return nil
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func runtimeError(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Stage string `json:"stage"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return truncate(e.Error.Message, 500)
	}
	return truncate(strings.TrimSpace(string(body)), 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// deadLetterDeploy tells the deployment service that the rollout will never happen.
func (s *Scheduler) deadLetterDeploy(ctx context.Context, msg redis.XMessage, cause error) {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return
	}
	if id := str(env.Payload, "deployment_id"); id != "" {
		s.report(ctx, id, "failed", "scheduler gave up: "+truncate(cause.Error(), 300), "")
	}
}

func (s *Scheduler) report(ctx context.Context, deploymentID, status, errMsg, previewURL string) {
	if err := s.reportErr(ctx, deploymentID, status, errMsg, previewURL); err != nil {
		s.cfg.Log.Error("report deployment status", "deployment", deploymentID, "status", status, "err", err)
	}
}

func (s *Scheduler) reportErr(ctx context.Context, deploymentID, status, errMsg, previewURL string) error {
	body := map[string]any{"status": status, "error": errMsg}
	if previewURL != "" {
		body["preview_url"] = previewURL
	}
	res, err := s.do(ctx, s.cfg.HTTP, http.MethodPost, join(s.cfg.DeploymentURL, "internal", "deployments", deploymentID, "status"), body)
	if err != nil {
		return err
	}
	if res.Status >= 300 && res.Status != http.StatusConflict { // 409 = already in a terminal state
		return fmt.Errorf("deployment service status %d", res.Status)
	}
	return nil
}

func (s *Scheduler) provisionPreviewURL(ctx context.Context, orgID, projectID, deploymentID, gitBranch string, pr int) string {
	if strings.TrimSpace(s.cfg.PreviewBaseDomain) == "" {
		s.cfg.Log.Warn("PREVIEW_BASE_DOMAIN not set; preview has no public URL", "deployment", deploymentID)
		return ""
	}
	host := buildPreviewHostname(gitBranch, projectID, s.cfg.PreviewBaseDomain, pr)
	res, err := s.do(ctx, s.cfg.HTTP, http.MethodPost, join(s.cfg.DomainURL, "internal", "domains", "preview"), map[string]any{
		"org_id": orgID, "project_id": projectID, "deployment_id": deploymentID, "hostname": host,
	})
	if err != nil || res.Status >= 300 {
		s.cfg.Log.Warn("preview domain provisioning failed", "deployment", deploymentID, "err", err)
		return ""
	}
	var out struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(res.Body, &out)
	if out.URL != "" {
		return out.URL
	}
	return "http://" + host
}

func buildPreviewHostname(gitBranch, projectID, baseDomain string, pr int) string {
	base := strings.TrimSpace(baseDomain)
	pid := strings.ToLower(strings.ReplaceAll(projectID, "-", ""))
	if len(pid) > 8 {
		pid = pid[:8]
	}
	branch := strings.ToLower(strings.TrimSpace(gitBranch))
	var label string
	switch {
	case pr > 0:
		label = "pr-" + strconv.Itoa(pr)
	case strings.HasPrefix(branch, "preview/pr-"):
		label = "pr-" + sanitizeHostLabel(strings.TrimPrefix(branch, "preview/pr-"))
	default:
		label = "b-" + sanitizeHostLabel(strings.TrimPrefix(branch, "preview/"))
	}
	if label == "pr-" || label == "b-" {
		label = "b-preview"
	}
	return fmt.Sprintf("%s-%s.%s", label, pid, base)
}

func sanitizeHostLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		return "x"
	}
	return out
}

// ---------------------------------------------------------------- cleanup

func (s *Scheduler) cleanupPublisher(ctx context.Context) {
	t := time.NewTicker(s.cfg.CleanupEvery)
	defer t.Stop()
	s.publishCleanup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.publishCleanup(ctx)
		}
	}
}

func (s *Scheduler) publishCleanup(ctx context.Context) {
	for _, typ := range []string{events.TypeCleanupOrphanImages, events.TypeCleanupPreview} {
		// one cleanup of each kind per interval even with several scheduler replicas
		key := fmt.Sprintf("jp:sched:cleanup:%s:%d", typ, time.Now().Unix()/int64(s.cfg.CleanupEvery.Seconds()))
		if ok, err := s.cfg.Redis.SetNX(ctx, key, "1", s.cfg.CleanupEvery*2).Result(); err != nil || !ok {
			continue
		}
		env := events.New(events.TopicCleanup, typ, "scheduler", "", map[string]any{
			"preview_ttl_hours": s.cfg.PreviewTTL.Hours(), "image_ttl_hours": s.cfg.ImageTTL.Hours(),
		})
		if _, err := events.PublishJSON(ctx, s.cfg.Redis, events.TopicCleanup, env); err != nil {
			s.cfg.Log.Warn("cleanup publish failed", "type", typ, "err", err)
		}
	}
}

func (s *Scheduler) handleCleanup(ctx context.Context, msg redis.XMessage) error {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return perm("unparseable envelope")
	}
	switch env.Type {
	case events.TypeCleanupOrphanImages:
		return s.postCleanup(ctx, join(s.cfg.RegistryURL, "internal", "cleanup", "orphaned-images"), s.cfg.ImageTTL)
	case events.TypeCleanupPreview:
		return s.postCleanup(ctx, join(s.cfg.DeploymentURL, "internal", "cleanup", "preview-deploys"), s.cfg.PreviewTTL)
	}
	return nil
}

func (s *Scheduler) postCleanup(ctx context.Context, u string, ttl time.Duration) error {
	res, err := s.do(ctx, s.cfg.HTTP, http.MethodPost, u, map[string]any{"older_than_hours": ttl.Hours()})
	if err != nil {
		return err
	}
	if res.Status >= 500 {
		return fmt.Errorf("cleanup status %d", res.Status)
	}
	s.cfg.Log.Info("cleanup", "url", u, "status", res.Status, "body", truncate(string(res.Body), 300))
	return nil
}

// ---------------------------------------------------------------- cron

func (s *Scheduler) cronTicker(ctx context.Context) {
	if s.cfg.Cron == nil {
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.fireDue(ctx, now.UTC())
		}
	}
}

func (s *Scheduler) fireDue(ctx context.Context, now time.Time) {
	due, err := s.cfg.Cron.Due(ctx, now)
	if err != nil {
		s.cfg.Log.Warn("cron due", "err", err)
		return
	}
	for i := range due {
		sch := due[i]
		if ok, err := s.cfg.Cron.Claim(ctx, &sch); err != nil || !ok {
			continue // another replica fired this activation
		}
		env := events.New(events.TopicJobs, events.TypeCronTriggered, "scheduler", sch.OrgID, map[string]any{
			"cron_id": sch.ID, "org_id": sch.OrgID, "project_id": sch.ProjectID, "name": sch.Name,
			"image_ref": sch.ImageRef, "command": sch.Command, "timeout_seconds": sch.TimeoutSeconds,
		})
		if _, err := events.PublishJSON(ctx, s.cfg.Redis, events.TopicJobs, env); err != nil {
			s.cfg.Log.Warn("cron publish", "cron", sch.ID, "err", err)
			continue
		}
		_ = s.cfg.Cron.MarkRun(ctx, &sch, now, "")
	}
}

func (s *Scheduler) handleJob(ctx context.Context, msg redis.XMessage) error {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return perm("unparseable envelope")
	}
	if env.Type != events.TypeCronTriggered && env.Type != events.TypeJobQueued {
		return nil
	}
	var cmd []string
	if raw, ok := env.Payload["command"].([]any); ok {
		for _, a := range raw {
			if str, ok := a.(string); ok {
				cmd = append(cmd, str)
			}
		}
	}
	// A cron job is ALWAYS a one-off container; it never goes through the deploy path
	// and therefore can never stop or replace a production instance.
	res, err := s.do(ctx, s.cfg.HTTP, http.MethodPost, join(s.cfg.RuntimeURL, "internal", "runtime", "jobs"), map[string]any{
		"org_id": str(env.Payload, "org_id"), "project_id": str(env.Payload, "project_id"), "cron_id": str(env.Payload, "cron_id"),
		"name": str(env.Payload, "name"), "image_ref": str(env.Payload, "image_ref"), "command": cmd,
		"timeout_seconds": intOf(env.Payload["timeout_seconds"]),
	})
	if err != nil {
		return err
	}
	switch {
	case res.Status == http.StatusAccepted:
		var out struct {
			Job struct {
				ID string `json:"id"`
			} `json:"job"`
		}
		_ = json.Unmarshal(res.Body, &out)
		if id := str(env.Payload, "cron_id"); id != "" && s.cfg.Cron != nil && out.Job.ID != "" {
			if sch, err := s.cfg.Cron.Get(ctx, id); err == nil {
				_ = s.cfg.Cron.MarkRun(ctx, sch, time.Now().UTC(), out.Job.ID)
			}
		}
		return nil
	case res.Status == http.StatusTooManyRequests || res.Status >= 500:
		return fmt.Errorf("runtime job status %d", res.Status)
	default:
		return perm("runtime refused job: %s", runtimeError(res.Body))
	}
}
