package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/scheduler/internal/cron"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	Redis             *redis.Client
	RuntimeURL        string
	RegistryURL       string
	DeploymentURL     string
	DomainURL         string
	PreviewBaseDomain string
	HTTP              *http.Client
	Log               *slog.Logger
	Slot              string
	HealthEvery       time.Duration
	CleanupEvery      time.Duration
	Cron              *cron.Store
	PreviewTTL        time.Duration
	ImageTTL          time.Duration
}

type Scheduler struct {
	cfg Config
}

func New(cfg Config) *Scheduler {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Slot == "" {
		cfg.Slot = "node-1"
	}
	if cfg.HealthEvery <= 0 {
		cfg.HealthEvery = 20 * time.Second
	}
	if cfg.CleanupEvery <= 0 {
		cfg.CleanupEvery = time.Hour
	}
	if cfg.PreviewTTL <= 0 {
		cfg.PreviewTTL = 72 * time.Hour
	}
	if cfg.ImageTTL <= 0 {
		cfg.ImageTTL = 168 * time.Hour
	}
	if cfg.RegistryURL == "" {
		cfg.RegistryURL = getenv("REGISTRY_URL", "http://localhost:8009")
	}
	if cfg.DeploymentURL == "" {
		cfg.DeploymentURL = getenv("DEPLOYMENT_URL", "http://localhost:8006")
	}
	if cfg.DomainURL == "" {
		cfg.DomainURL = getenv("DOMAIN_URL", "http://localhost:8012")
	}
	if cfg.PreviewBaseDomain == "" {
		cfg.PreviewBaseDomain = getenv("PREVIEW_BASE_DOMAIN", "preview.jp.localhost")
	}
	return &Scheduler{cfg: cfg}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (s *Scheduler) Run(ctx context.Context) error {
	if err := events.EnsureGroup(ctx, s.cfg.Redis, events.TopicDeploy, events.SchedulerConsumerGroup); err != nil {
		return err
	}
	if err := events.EnsureGroup(ctx, s.cfg.Redis, events.TopicCleanup, events.CleanupConsumerGroup); err != nil {
		return err
	}
	if err := events.EnsureGroup(ctx, s.cfg.Redis, events.TopicJobs, events.JobsConsumerGroup); err != nil {
		return err
	}
	s.cfg.Log.Info("scheduler started", "slot", s.cfg.Slot, "consumer_group", events.SchedulerConsumerGroup)

	go s.healthLoop(ctx)
	go s.cleanupPublisher(ctx)
	go s.cleanupConsumer(ctx)
	go s.jobsConsumer(ctx)
	go s.cronTicker(ctx)

	consumer := "scheduler-1"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msgs, err := events.ReadGroup(ctx, s.cfg.Redis, events.TopicDeploy, events.SchedulerConsumerGroup, consumer, 1, 5*time.Second)
		if err != nil {
			s.cfg.Log.Error("read group", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for _, msg := range msgs {
			if err := s.handle(ctx, msg); err != nil {
				s.cfg.Log.Error("schedule failed", "id", msg.ID, "err", err)
			}
			_ = events.Ack(ctx, s.cfg.Redis, events.TopicDeploy, events.SchedulerConsumerGroup, msg.ID)
		}
	}
}

func (s *Scheduler) handle(ctx context.Context, msg redis.XMessage) error {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return err
	}
	switch env.Type {
	case events.TypeDeployUpdated:
		return s.startFromEvent(ctx, env)
	default:
		s.cfg.Log.Debug("ignoring event", "type", env.Type)
		return nil
	}
}

func (s *Scheduler) startFromEvent(ctx context.Context, env events.Envelope) error {
	imageRef, _ := env.Payload["image_ref"].(string)
	orgID, _ := env.Payload["org_id"].(string)
	projectID, _ := env.Payload["project_id"].(string)
	deploymentID, _ := env.Payload["deployment_id"].(string)
	status, _ := env.Payload["status"].(string)
	strategy, _ := env.Payload["strategy"].(string)
	gitBranch, _ := env.Payload["git_branch"].(string)
	preview := payloadBool(env.Payload["preview"]) || isPreviewBranch(gitBranch)
	if strategy == "" {
		strategy = "rolling"
	}

	if env.Type == events.TypeDeployUpdated && status != "" && status != "ready" {
		return nil
	}
	if imageRef == "" || orgID == "" || projectID == "" {
		return fmt.Errorf("missing image_ref/org_id/project_id in %s", env.Type)
	}

	s.cfg.Log.Info("assigning slot", "slot", s.cfg.Slot, "project", projectID, "image", imageRef, "strategy", strategy, "preview", preview)
	rolling := strategy != "blue_green"
	if preview {
		rolling = false
	}
	body := map[string]any{
		"org_id":         orgID,
		"project_id":     projectID,
		"deployment_id":  deploymentID,
		"image_ref":      imageRef,
		"slot":           s.cfg.Slot,
		"restart_policy": "on-failure",
		"rolling":        rolling,
		"strategy":       strategy,
		"preview":        preview,
		"kind":           inferKind(imageRef, env.Payload),
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.RuntimeURL, "/")+"/internal/runtime/start", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("runtime start %d: %s", resp.StatusCode, string(b))
	}
	s.cfg.Log.Info("runtime started", "project", projectID, "image", imageRef, "preview", preview)
	if preview && deploymentID != "" {
		s.provisionPreviewURL(ctx, orgID, projectID, deploymentID, gitBranch)
	}
	return nil
}

func (s *Scheduler) provisionPreviewURL(ctx context.Context, orgID, projectID, deploymentID, gitBranch string) {
	host := buildPreviewHostname(gitBranch, projectID, s.cfg.PreviewBaseDomain)
	body, _ := json.Marshal(map[string]any{
		"org_id": orgID, "project_id": projectID, "deployment_id": deploymentID, "hostname": host,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.cfg.DomainURL, "/")+"/internal/domains/preview", bytes.NewReader(body))
	if err != nil {
		s.cfg.Log.Warn("preview domain provision request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		s.cfg.Log.Warn("preview domain provision failed", "err", err)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		s.cfg.Log.Warn("preview domain provision status", "status", resp.StatusCode, "body", string(raw))
		return
	}
	var out struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &out)
	previewURL := out.URL
	if previewURL == "" {
		previewURL = "http://" + host
	}
	setBody, _ := json.Marshal(map[string]any{"preview_url": previewURL})
	sreq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.cfg.DeploymentURL, "/")+"/internal/deployments/"+deploymentID+"/preview-url",
		bytes.NewReader(setBody))
	if err != nil {
		return
	}
	sreq.Header.Set("Content-Type", "application/json")
	sresp, err := s.cfg.HTTP.Do(sreq)
	if err != nil {
		s.cfg.Log.Warn("set preview_url failed", "err", err)
		return
	}
	_ = sresp.Body.Close()
	s.cfg.Log.Info("preview url provisioned", "deployment", deploymentID, "url", previewURL)
}

func payloadBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true") || t == "1"
	default:
		return false
	}
}

func isPreviewBranch(branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	return strings.HasPrefix(b, "preview/") || strings.Contains(b, "preview")
}

func buildPreviewHostname(gitBranch, projectID, baseDomain string) string {
	base := strings.TrimSpace(baseDomain)
	if base == "" {
		base = "preview.jp.localhost"
	}
	pid := strings.ToLower(strings.ReplaceAll(projectID, "-", ""))
	if len(pid) > 8 {
		pid = pid[:8]
	}
	branch := strings.ToLower(strings.TrimSpace(gitBranch))
	var label string
	if strings.HasPrefix(branch, "preview/pr-") {
		n := strings.TrimPrefix(branch, "preview/pr-")
		label = "pr-" + sanitizeHostLabel(n)
	} else {
		slug := branch
		if strings.HasPrefix(slug, "preview/") {
			slug = strings.TrimPrefix(slug, "preview/")
		}
		label = "b-" + sanitizeHostLabel(slug)
	}
	if label == "pr-" || label == "b-" || label == "b" {
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
		out = out[:40]
	}
	if out == "" {
		return "x"
	}
	return out
}

func (s *Scheduler) healthLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.HealthEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkHealth(ctx)
		}
	}
}

func (s *Scheduler) checkHealth(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cfg.RuntimeURL, "/")+"/internal/runtime/desired", nil)
	if err != nil {
		return
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		s.cfg.Log.Warn("health list failed", "err", err)
		return
	}
	defer resp.Body.Close()
	var payload struct {
		Instances []struct {
			ID            string  `json:"id"`
			Status        string  `json:"status"`
			DesiredState  string  `json:"desired_state"`
			ContainerID   string  `json:"container_id"`
			RestartPolicy string  `json:"restart_policy"`
			ImageRef      string  `json:"image_ref"`
			OrgID         string  `json:"org_id"`
			ProjectID     string  `json:"project_id"`
			DeploymentID  *string `json:"deployment_id"`
			Mode          string  `json:"mode"`
		} `json:"instances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return
	}
	for _, in := range payload.Instances {
		healthy := in.Status == "running"
		health := "healthy"
		status := in.Status
		errMsg := ""
		if !healthy && in.DesiredState == "running" {
			health = "unhealthy"
			if in.RestartPolicy == "on-failure" || in.RestartPolicy == "always" {
				s.cfg.Log.Info("restart policy triggered", "instance", in.ID, "policy", in.RestartPolicy)
				depID := ""
				if in.DeploymentID != nil {
					depID = *in.DeploymentID
				}
				body := map[string]any{
					"org_id": in.OrgID, "project_id": in.ProjectID, "deployment_id": depID,
					"image_ref": in.ImageRef, "slot": s.cfg.Slot, "restart_policy": in.RestartPolicy, "rolling": true,
				}
				raw, _ := json.Marshal(body)
				rreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.RuntimeURL, "/")+"/internal/runtime/start", bytes.NewReader(raw))
				if rreq != nil {
					rreq.Header.Set("Content-Type", "application/json")
					rresp, rerr := s.cfg.HTTP.Do(rreq)
					if rerr == nil {
						rresp.Body.Close()
						status = "running"
						health = "healthy"
					} else {
						errMsg = rerr.Error()
						status = "failed"
					}
				}
			}
		}
		patch, _ := json.Marshal(map[string]any{"health_status": health, "status": status, "error": errMsg})
		preq, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.RuntimeURL, "/")+"/internal/runtime/instances/"+in.ID+"/health", bytes.NewReader(patch))
		if preq != nil {
			preq.Header.Set("Content-Type", "application/json")
			presp, perr := s.cfg.HTTP.Do(preq)
			if perr == nil {
				presp.Body.Close()
			}
		}
	}
}

func (s *Scheduler) cleanupPublisher(ctx context.Context) {
	t := time.NewTicker(s.cfg.CleanupEvery)
	defer t.Stop()
	// Fire once shortly after start
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
		env := events.New(events.TopicCleanup, typ, "scheduler", "", map[string]any{
			"preview_ttl_hours": s.cfg.PreviewTTL.Hours(),
			"image_ttl_hours":   s.cfg.ImageTTL.Hours(),
		})
		if _, err := events.PublishJSON(ctx, s.cfg.Redis, events.TopicCleanup, env); err != nil {
			s.cfg.Log.Warn("cleanup publish failed", "type", typ, "err", err)
		}
	}
}

func (s *Scheduler) cleanupConsumer(ctx context.Context) {
	consumer := "cleanup-1"
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := events.ReadGroup(ctx, s.cfg.Redis, events.TopicCleanup, events.CleanupConsumerGroup, consumer, 1, 5*time.Second)
		if err != nil {
			s.cfg.Log.Error("cleanup read", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for _, msg := range msgs {
			env, err := events.ParseEnvelope(msg)
			if err == nil {
				switch env.Type {
				case events.TypeCleanupOrphanImages:
					s.runOrphanImageCleanup(ctx)
				case events.TypeCleanupPreview:
					s.runPreviewCleanup(ctx)
				}
			}
			_ = events.Ack(ctx, s.cfg.Redis, events.TopicCleanup, events.CleanupConsumerGroup, msg.ID)
		}
	}
}

func (s *Scheduler) runOrphanImageCleanup(ctx context.Context) {
	url := strings.TrimRight(s.cfg.RegistryURL, "/") + "/internal/cleanup/orphaned-images"
	body, _ := json.Marshal(map[string]any{"older_than_hours": s.cfg.ImageTTL.Hours()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		s.cfg.Log.Warn("orphan image cleanup failed", "err", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	s.cfg.Log.Info("orphan image cleanup", "status", resp.StatusCode, "body", string(b))
}

func (s *Scheduler) runPreviewCleanup(ctx context.Context) {
	url := strings.TrimRight(s.cfg.DeploymentURL, "/") + "/internal/cleanup/preview-deploys"
	body, _ := json.Marshal(map[string]any{"older_than_hours": s.cfg.PreviewTTL.Hours()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		s.cfg.Log.Warn("preview cleanup failed", "err", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	s.cfg.Log.Info("preview deploy cleanup", "status", resp.StatusCode, "body", string(b))
}

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
			due, err := s.cfg.Cron.Due(ctx, now.UTC())
			if err != nil {
				continue
			}
			for i := range due {
				sch := due[i]
				env := events.New(events.TopicJobs, events.TypeCronTriggered, "scheduler", sch.OrgID, map[string]any{
					"cron_id": sch.ID, "org_id": sch.OrgID, "project_id": sch.ProjectID,
					"name": sch.Name, "image_ref": sch.ImageRef,
				})
				_, _ = events.PublishJSON(ctx, s.cfg.Redis, events.TopicJobs, env)
				_ = s.cfg.Cron.MarkRun(ctx, &sch, now.UTC())
			}
		}
	}
}

func (s *Scheduler) jobsConsumer(ctx context.Context) {
	consumer := "jobs-1"
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := events.ReadGroup(ctx, s.cfg.Redis, events.TopicJobs, events.JobsConsumerGroup, consumer, 1, 5*time.Second)
		if err != nil {
			s.cfg.Log.Error("jobs read", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for _, msg := range msgs {
			env, err := events.ParseEnvelope(msg)
			if err == nil && (env.Type == events.TypeCronTriggered || env.Type == events.TypeJobQueued) {
				_ = s.startCronJob(ctx, env)
			}
			_ = events.Ack(ctx, s.cfg.Redis, events.TopicJobs, events.JobsConsumerGroup, msg.ID)
		}
	}
}

func (s *Scheduler) startCronJob(ctx context.Context, env events.Envelope) error {
	imageRef, _ := env.Payload["image_ref"].(string)
	orgID, _ := env.Payload["org_id"].(string)
	projectID, _ := env.Payload["project_id"].(string)
	if imageRef == "" {
		s.cfg.Log.Info("cron triggered without image_ref (no-op)", "name", env.Payload["name"])
		return nil
	}
	body := map[string]any{
		"org_id": orgID, "project_id": projectID, "image_ref": imageRef,
		"slot": s.cfg.Slot, "restart_policy": "never", "rolling": false, "kind": "container",
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.RuntimeURL, "/")+"/internal/runtime/start", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	s.cfg.Log.Info("cron runtime job started", "project", projectID, "image", imageRef)
	return nil
}

func inferKind(imageRef string, payload map[string]any) string {
	if k, ok := payload["kind"].(string); ok && k != "" {
		return k
	}
	if f, ok := payload["framework"].(string); ok {
		switch strings.ToLower(f) {
		case "nodejs", "node":
			return "node"
		case "static":
			return "static"
		}
	}
	l := strings.ToLower(imageRef)
	if strings.Contains(l, "node") {
		return "node"
	}
	return "container"
}
