package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jp-cloud/events"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	Redis        *redis.Client
	BuildURL     string
	DeploymentURL string
	RegistryURL  string
	RegistryHost string // e.g. localhost:5000
	BuildMode    string // simulate | docker
	WorkDir      string
	HTTP         *http.Client
	Log          *slog.Logger
}

type Runner struct {
	cfg Config
}

func New(cfg Config) *Runner {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/tmp/jp-builds"
	}
	if cfg.BuildMode == "" {
		cfg.BuildMode = "simulate"
	}
	if cfg.RegistryHost == "" {
		cfg.RegistryHost = "localhost:5000"
	}
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	if err := events.EnsureGroup(ctx, r.cfg.Redis, events.TopicBuild, events.BuildConsumerGroup); err != nil {
		return err
	}
	consumer := "worker-1"
	r.cfg.Log.Info("worker started", "mode", r.cfg.BuildMode, "concurrency", 1)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msgs, err := events.ReadGroup(ctx, r.cfg.Redis, events.TopicBuild, events.BuildConsumerGroup, consumer, 1, 5*time.Second)
		if err != nil {
			r.cfg.Log.Error("read group", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		// concurrency = 1: process sequentially
		for _, msg := range msgs {
			if err := r.handle(ctx, msg); err != nil {
				r.cfg.Log.Error("job failed", "id", msg.ID, "err", err)
			}
			_ = events.Ack(ctx, r.cfg.Redis, events.TopicBuild, events.BuildConsumerGroup, msg.ID)
		}
	}
}

func (r *Runner) handle(ctx context.Context, msg redis.XMessage) error {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return err
	}
	buildID, _ := env.Payload["build_id"].(string)
	if buildID == "" {
		return fmt.Errorf("missing build_id")
	}
	cloneURL, _ := env.Payload["clone_url"].(string)
	fullName, _ := env.Payload["full_name"].(string)
	gitSHA, _ := env.Payload["git_sha"].(string)
	gitBranch, _ := env.Payload["git_branch"].(string)
	deploymentID, _ := env.Payload["deployment_id"].(string)
	orgID, _ := env.Payload["org_id"].(string)
	projectID, _ := env.Payload["project_id"].(string)

	_ = r.patchBuild(buildID, map[string]any{
		"status": "running",
		"logs":   fmt.Sprintf("[%s] accepted job\n", time.Now().UTC().Format(time.RFC3339)),
	})

	dir := filepath.Join(r.cfg.WorkDir, buildID)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return r.fail(buildID, deploymentID, err)
	}
	defer os.RemoveAll(dir)

	logs := &strings.Builder{}
	fmt.Fprintf(logs, "work dir: %s\n", dir)

	cloned := false
	if cloneURL != "" && gitSHA != "HEAD" {
		fmt.Fprintf(logs, "cloning %s ...\n", cloneURL)
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", firstNonEmpty(gitBranch, "main"), cloneURL, dir)
		out, err := cmd.CombinedOutput()
		logs.Write(out)
		if err != nil {
			fmt.Fprintf(logs, "clone failed (%v); falling back to mock workspace\n", err)
			_ = writeMockProject(dir, fullName)
		} else {
			cloned = true
		}
	} else {
		fmt.Fprintf(logs, "no clone URL / SHA=HEAD — using mock workspace\n")
		_ = writeMockProject(dir, fullName)
	}
	_ = r.patchBuild(buildID, map[string]any{"logs": logs.String()})
	logs.Reset()

	framework := detectFramework(dir)
	fmt.Fprintf(logs, "framework detect: %s (cloned=%v)\n", framework, cloned)
	_ = r.patchBuild(buildID, map[string]any{"framework": framework, "logs": logs.String()})
	logs.Reset()

	imageRef := fmt.Sprintf("%s/jp/%s:%s", r.cfg.RegistryHost, sanitize(firstNonEmpty(fullName, projectID)), shortSHA(gitSHA, buildID))

	if r.cfg.BuildMode == "docker" && dockerAvailable(ctx) {
		fmt.Fprintf(logs, "building docker image %s\n", imageRef)
		dockerfile := ensureDockerfile(dir, framework)
		fmt.Fprintf(logs, "using Dockerfile: %s\n", dockerfile)
		cmd := exec.CommandContext(ctx, "docker", "build", "-t", imageRef, dir)
		out, err := cmd.CombinedOutput()
		logs.Write(out)
		if err != nil {
			_ = r.patchBuild(buildID, map[string]any{"logs": logs.String()})
			return r.fail(buildID, deploymentID, fmt.Errorf("docker build: %w", err))
		}
		fmt.Fprintf(logs, "pushing %s\n", imageRef)
		push := exec.CommandContext(ctx, "docker", "push", imageRef)
		pout, perr := push.CombinedOutput()
		logs.Write(pout)
		if perr != nil {
			fmt.Fprintf(logs, "push failed (%v); registering image metadata only\n", perr)
		}
	} else {
		fmt.Fprintf(logs, "BUILD_MODE=simulate (docker unavailable or disabled)\n")
		fmt.Fprintf(logs, "simulating build for framework=%s\n", framework)
		time.Sleep(500 * time.Millisecond)
		fmt.Fprintf(logs, "simulated layers: base, deps, app\n")
		fmt.Fprintf(logs, "image tag: %s\n", imageRef)
	}

	_ = r.registerImage(orgID, projectID, imageRef, framework, gitSHA)
	_ = r.patchBuild(buildID, map[string]any{
		"status":    "succeeded",
		"framework": framework,
		"image_ref": imageRef,
		"logs":      logs.String() + "build succeeded\n",
	})
	if deploymentID != "" {
		_ = r.patchDeployment(deploymentID, "ready", "success", imageRef, "")
	}
	r.cfg.Log.Info("build succeeded", "build_id", buildID, "image", imageRef, "framework", framework)
	return nil
}

func (r *Runner) fail(buildID, deploymentID string, err error) error {
	_ = r.patchBuild(buildID, map[string]any{
		"status": "failed",
		"error":  err.Error(),
		"logs":   "error: " + err.Error() + "\n",
	})
	if deploymentID != "" {
		_ = r.patchDeployment(deploymentID, "failed", "failure", "", err.Error())
	}
	return err
}

func (r *Runner) patchBuild(id string, body map[string]any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(r.cfg.BuildURL, "/")+"/internal/builds/"+id+"/status", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (r *Runner) patchDeployment(id, status, commitStatus, imageRef, errMsg string) error {
	if r.cfg.DeploymentURL == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{
		"status": status, "commit_status": commitStatus, "image_ref": imageRef, "error": errMsg,
	})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(r.cfg.DeploymentURL, "/")+"/internal/deployments/"+id+"/status", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (r *Runner) registerImage(orgID, projectID, imageRef, framework, gitSHA string) error {
	if r.cfg.RegistryURL == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{
		"org_id": orgID, "project_id": projectID, "image_ref": imageRef,
		"framework": framework, "git_sha": gitSHA,
	})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(r.cfg.RegistryURL, "/")+"/internal/images", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func detectFramework(dir string) string {
	checks := []struct {
		file string
		name string
	}{
		{"package.json", "nodejs"},
		{"go.mod", "go"},
		{"pyproject.toml", "python"},
		{"requirements.txt", "python"},
		{"Cargo.toml", "rust"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Dockerfile", "dockerfile"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.name
		}
	}
	return "unknown"
}

func writeMockProject(dir, fullName string) error {
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"jp-app","private":true}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+fullName+"\n\nMock workspace for jp build worker.\n"), 0o644)
	return nil
}

func ensureDockerfile(dir, framework string) string {
	path := filepath.Join(dir, "Dockerfile")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	var content string
	switch framework {
	case "go":
		content = "FROM golang:1.22-alpine\nWORKDIR /app\nCOPY . .\nRUN go build -o /out/app ./...\nCMD [\"/out/app\"]\n"
	case "python":
		content = "FROM python:3.12-slim\nWORKDIR /app\nCOPY . .\nRUN pip install -r requirements.txt || true\nCMD [\"python\", \"-m\", \"http.server\", \"8080\"]\n"
	default:
		content = "FROM node:22-alpine\nWORKDIR /app\nCOPY . .\nRUN npm install || true\nCMD [\"node\", \"-e\", \"console.log('jp')\"]\n"
	}
	_ = os.WriteFile(path, []byte(content), 0o644)
	return path
}

func dockerAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "version")
	return cmd.Run() == nil
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		return "app"
	}
	return s
}

func shortSHA(sha, fallback string) string {
	if len(sha) >= 7 && sha != "HEAD" {
		return sha[:7]
	}
	if len(fallback) >= 8 {
		return fallback[:8]
	}
	return "latest"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
