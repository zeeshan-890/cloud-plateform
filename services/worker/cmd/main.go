package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8008")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	if err := events.EnsureGroup(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup); err != nil {
		log.Error("ensure group", "err", err)
		os.Exit(1)
	}

	hostname, _ := os.Hostname()
	concurrency := workerConcurrency()
	w := &Worker{
		BuildURL:      cfg.BuildURL,
		DeploymentURL: cfg.DeploymentURL,
		RegistryAPI:   strings.TrimRight(cfg.RegistryURL, "/"),
		RegistryURL:   strings.TrimRight(getEnv("REGISTRY_PUSH_URL", getEnv("REGISTRY_HOST", "registry:5000")), "/"),
		Simulate:      simulateBuild(),
		HTTP:          &http.Client{Timeout: 30 * time.Second},
		Log:           log,
		Consumer:      getEnv("WORKER_NAME", firstNonEmpty(hostname, "worker-1")),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("worker listening", "port", cfg.HTTPPort, "simulate", w.Simulate, "concurrency", concurrency, "consumer", w.Consumer)
		_ = srv.ListenAndServe()
	}()

	log.Info("worker consuming", "stream", events.TopicBuild, "group", events.BuildConsumerGroup, "concurrency", concurrency)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			_ = srv.Shutdown(shutdownCtx)
			return
		default:
		}
		// Claim up to concurrency messages per poll so parallel builds can start together.
		msgs, err := events.ReadGroup(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup, w.Consumer, int64(concurrency), 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return
			}
			log.Error("read group", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, msg := range msgs {
			env, err := events.ParseEnvelope(msg)
			if err != nil {
				log.Error("parse envelope", "err", err)
				_ = events.Ack(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup, msg.ID)
				continue
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(msgID string, env events.Envelope) {
				defer wg.Done()
				defer func() { <-sem }()
				w.process(ctx, env)
				_ = events.Ack(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup, msgID)
			}(msg.ID, env)
		}
	}
}

type Worker struct {
	BuildURL      string
	DeploymentURL string
	RegistryAPI   string
	RegistryURL   string
	Simulate      bool
	HTTP          *http.Client
	Log           *slog.Logger
	Consumer      string
}

func (w *Worker) process(ctx context.Context, env events.Envelope) {
	buildID, _ := env.Payload["build_id"].(string)
	if buildID == "" {
		w.Log.Error("missing build_id")
		return
	}
	orgID, _ := env.Payload["org_id"].(string)
	projectID, _ := env.Payload["project_id"].(string)
	deploymentID, _ := env.Payload["deployment_id"].(string)
	gitSHA, _ := env.Payload["git_sha"].(string)
	gitBranch, _ := env.Payload["git_branch"].(string)
	cloneURL, _ := env.Payload["clone_url"].(string)
	fullName, _ := env.Payload["full_name"].(string)

	w.Log.Info("claimed build", "build_id", buildID, "repo", fullName)
	w.patchBuild(ctx, buildID, "running", "", "", "worker claimed job\n", "")

	var logs strings.Builder
	logs.WriteString("worker claimed job\n")

	workDir, err := os.MkdirTemp("", "jp-build-*")
	if err != nil {
		w.fail(ctx, buildID, deploymentID, "temp dir: "+err.Error())
		return
	}
	defer os.RemoveAll(workDir)

	framework := "unknown"
	if w.Simulate || cloneURL == "" {
		logs.WriteString(fmt.Sprintf("simulated clone %s@%s (%s)\n", firstNonEmpty(fullName, "local"), gitSHA, gitBranch))
		framework = detectFromName(fullName)
		logs.WriteString("detected framework: " + framework + "\n")
		writeSimulatedTree(workDir, framework)
	} else {
		logs.WriteString("cloning " + cloneURL + "\n")
		w.patchBuild(ctx, buildID, "running", "", "", logs.String(), "")
		if err := gitClone(ctx, cloneURL, gitBranch, workDir); err != nil {
			logs.WriteString("clone failed: " + err.Error() + " — falling back to simulated tree\n")
			framework = detectFromName(fullName)
			writeSimulatedTree(workDir, framework)
		} else {
			framework = detectFramework(workDir)
			logs.WriteString("clone ok; framework=" + framework + "\n")
		}
	}
	w.patchBuild(ctx, buildID, "running", framework, "", logs.String(), "")

	imageRef := fmt.Sprintf("%s/%s/%s:%s",
		strings.TrimPrefix(strings.TrimPrefix(w.RegistryURL, "http://"), "https://"),
		short(orgID), short(projectID), shortSHA(gitSHA))
	if strings.HasPrefix(imageRef, "/") {
		imageRef = "registry:5000" + imageRef
	}

	logs.WriteString("building image (simulate=" + fmt.Sprintf("%v", w.Simulate) + ")\n")
	if !w.Simulate && dockerAvailable(ctx) {
		logs.WriteString("BuildKit build starting\n")
		w.patchBuild(ctx, buildID, "running", framework, "", logs.String(), "")
		if err := dockerBuild(ctx, workDir, imageRef, &logs); err != nil {
			logs.WriteString("build failed: " + err.Error() + "\n")
			w.fail(ctx, buildID, deploymentID, logs.String())
			return
		}
		logs.WriteString("pushing " + imageRef + "\n")
		if err := dockerPush(ctx, imageRef, &logs); err != nil {
			logs.WriteString("push failed (tag recorded anyway): " + err.Error() + "\n")
		}
	} else {
		logs.WriteString("simulated BuildKit steps:\n")
		for _, step := range []string{
			"[1/4] FROM alpine:3.20",
			"[2/4] COPY . /app",
			"[3/4] RUN build (" + framework + ")",
			"[4/4] tagging " + imageRef,
		} {
			logs.WriteString(step + "\n")
			time.Sleep(200 * time.Millisecond)
			w.patchBuild(ctx, buildID, "running", framework, "", logs.String(), "")
		}
		logs.WriteString("simulated push ok\n")
	}

	logs.WriteString("build succeeded\n")
	w.patchBuild(ctx, buildID, "succeeded", framework, imageRef, logs.String(), "")
	w.registerImage(ctx, orgID, projectID, imageRef, framework, gitSHA)
	if deploymentID != "" {
		w.patchDeploy(ctx, deploymentID, "ready", "success", imageRef, "")
	}
	w.Log.Info("build succeeded", "build_id", buildID, "image", imageRef, "framework", framework)
}

func (w *Worker) fail(ctx context.Context, buildID, deploymentID, msg string) {
	w.patchBuild(ctx, buildID, "failed", "", "", msg+"\n", msg)
	if deploymentID != "" {
		w.patchDeploy(ctx, deploymentID, "failed", "failure", "", msg)
	}
}

func (w *Worker) registerImage(ctx context.Context, orgID, projectID, imageRef, framework, gitSHA string) {
	if w.RegistryAPI == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"org_id": orgID, "project_id": projectID, "image_ref": imageRef,
		"framework": framework, "git_sha": gitSHA,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		w.RegistryAPI+"/internal/images", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		w.Log.Error("register image", "err", err)
		return
	}
	_ = resp.Body.Close()
}

func (w *Worker) patchBuild(ctx context.Context, id, status, framework, imageRef, logs, errMsg string) {
	body, _ := json.Marshal(map[string]any{
		"status": status, "framework": framework, "image_ref": imageRef, "logs": logs, "error": errMsg,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(w.BuildURL, "/")+"/internal/builds/"+id+"/status", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		w.Log.Error("patch build", "err", err)
		return
	}
	_ = resp.Body.Close()
}

func (w *Worker) patchDeploy(ctx context.Context, id, status, commitStatus, imageRef, errMsg string) {
	body, _ := json.Marshal(map[string]any{
		"status": status, "commit_status": commitStatus, "image_ref": imageRef, "error": errMsg,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(w.DeploymentURL, "/")+"/internal/deployments/"+id+"/status", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		w.Log.Error("patch deploy", "err", err)
		return
	}
	_ = resp.Body.Close()
}

func gitClone(ctx context.Context, url, branch, dir string) error {
	args := []string{"clone", "--depth", "1"}
	if branch != "" && branch != "HEAD" {
		args = append(args, "--branch", branch)
	}
	args = append(args, url, dir)
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

func detectFramework(dir string) string {
	checks := []struct {
		file string
		name string
	}{
		{"next.config.js", "next"},
		{"next.config.mjs", "next"},
		{"next.config.ts", "next"},
		{"vite.config.ts", "react"},
		{"vite.config.js", "vue"},
		{"nuxt.config.ts", "vue"},
		{"go.mod", "go"},
		{"requirements.txt", "fastapi"},
		{"pyproject.toml", "fastapi"},
		{"package.json", "node"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			if c.name == "node" {
				return detectFromPackageJSON(filepath.Join(dir, "package.json"))
			}
			return c.name
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(dir, "app.py")); err == nil {
		return "fastapi"
	}
	return "unknown"
}

func detectFromPackageJSON(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "node"
	}
	s := string(b)
	switch {
	case strings.Contains(s, "\"next\""):
		return "next"
	case strings.Contains(s, "\"react\""):
		return "react"
	case strings.Contains(s, "\"vue\""):
		return "vue"
	case strings.Contains(s, "\"express\""):
		return "express"
	default:
		return "node"
	}
}

func detectFromName(fullName string) string {
	n := strings.ToLower(fullName)
	switch {
	case strings.Contains(n, "next"):
		return "next"
	case strings.Contains(n, "react") || strings.Contains(n, "dashboard") || strings.Contains(n, "web"):
		return "react"
	case strings.Contains(n, "vue"):
		return "vue"
	case strings.Contains(n, "express"):
		return "express"
	case strings.Contains(n, "fastapi") || strings.Contains(n, "python"):
		return "fastapi"
	case strings.Contains(n, "go") || strings.Contains(n, "api-go"):
		return "go"
	default:
		return "node"
	}
}

func writeSimulatedTree(dir, framework string) {
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:3.20\nCMD [\"sleep\",\"infinity\"]\n"), 0o644)
	switch framework {
	case "next", "react", "vue", "express", "node":
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"app","dependencies":{"`+frameworkDep(framework)+`":"*"}}`), 0o644)
	case "go":
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module app\n\ngo 1.22\n"), 0o644)
	case "fastapi":
		_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("fastapi\nuvicorn\n"), 0o644)
	}
}

func frameworkDep(f string) string {
	switch f {
	case "next":
		return "next"
	case "react":
		return "react"
	case "vue":
		return "vue"
	case "express":
		return "express"
	default:
		return "node"
	}
}

func dockerAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	return cmd.Run() == nil
}

func dockerBuild(ctx context.Context, dir, imageRef string, logs *strings.Builder) error {
	cmd := exec.CommandContext(ctx, "docker", "buildx", "build", "-t", imageRef, "--load", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	logs.Write(out)
	return err
}

func dockerPush(ctx context.Context, imageRef string, logs *strings.Builder) error {
	cmd := exec.CommandContext(ctx, "docker", "push", imageRef)
	out, err := cmd.CombinedOutput()
	logs.Write(out)
	return err
}

func short(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "unknown"
	}
	return id
}

func shortSHA(sha string) string {
	if sha == "" || sha == "HEAD" {
		return "latest"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func simulateBuild() bool {
	mode := strings.ToLower(getEnv("BUILD_MODE", ""))
	if mode == "docker" || mode == "buildkit" || mode == "real" {
		return false
	}
	if mode == "simulate" {
		return true
	}
	return config.GetEnvBool("SIMULATE_BUILD", true)
}

func workerConcurrency() int {
	n, err := strconv.Atoi(getEnv("WORKER_CONCURRENCY", "1"))
	if err != nil || n < 1 {
		return 1
	}
	if n > 16 {
		return 16
	}
	return n
}
