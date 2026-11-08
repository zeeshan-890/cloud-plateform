package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

// JobRequest runs a command once in a throw-away container. It never touches the
// production instances of the project.
type JobRequest struct {
	OrgID          string            `json:"org_id"`
	ProjectID      string            `json:"project_id"`
	CronID         string            `json:"cron_id"`
	Name           string            `json:"name"`
	ImageRef       string            `json:"image_ref"` // empty: image of the current production deployment
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// ErrBusy means the job concurrency limit was reached.
var ErrBusy = errors.New("too many concurrent jobs")

const logTailBytes = 16 << 10

// RunJob validates the request, records a run and executes it asynchronously.
func (m *Manager) RunJob(ctx context.Context, req JobRequest) (*store.JobRun, error) {
	if !dockerx.ValidUUID(req.OrgID) || !dockerx.ValidUUID(req.ProjectID) {
		return nil, bad("org_id and project_id must be UUIDs")
	}
	if len(req.Command) > 64 {
		return nil, bad("command too long")
	}
	for _, a := range req.Command {
		if len(a) > 4096 || strings.ContainsRune(a, 0) {
			return nil, bad("invalid command argument")
		}
	}
	for k := range req.Env {
		if !dockerx.ValidEnvKey(k) {
			return nil, bad("invalid env name %q", k)
		}
	}
	image := req.ImageRef
	if image == "" {
		act, err := m.Repo.ListActiveProduction(ctx, req.OrgID, req.ProjectID)
		if err != nil {
			return nil, err
		}
		if len(act) == 0 {
			return nil, bad("no image_ref given and the project has no running production deployment")
		}
		image = act[0].ImageRef
	}
	if err := dockerx.ValidImageRef(image); err != nil {
		return nil, bad("%v", err)
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = m.Cfg.JobDefaultTimeout
	}
	if timeout > m.Cfg.JobMaxTimeout {
		timeout = m.Cfg.JobMaxTimeout
	}
	select {
	case m.jobSem <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	run := &store.JobRun{
		OrgID: req.OrgID, ProjectID: req.ProjectID, CronID: req.CronID, Name: truncateStr(req.Name, 120),
		ImageRef: image, Command: req.Command, Status: "running", TimeoutSeconds: int(timeout.Seconds()),
	}
	if err := m.Repo.CreateJob(ctx, run); err != nil {
		<-m.jobSem
		return nil, err
	}
	run.ContainerName = "jp-job-" + dockerx.Short8(req.ProjectID) + "-" + dockerx.Short8(run.ID)

	m.jobs.Add(1)
	go func() {
		defer m.jobs.Done()
		defer func() { <-m.jobSem }()
		jctx, cancel := context.WithTimeout(m.baseCtx, timeout+2*time.Minute)
		defer cancel()
		m.execJob(jctx, run, req, timeout)
	}()
	snapshot := *run
	return &snapshot, nil
}

func (m *Manager) execJob(ctx context.Context, run *store.JobRun, req JobRequest, timeout time.Duration) {
	finish := func(status, errMsg string, code *int, tail string) {
		run.Status, run.Error, run.ExitCode, run.LogTail = status, truncateStr(errMsg, 500), code, tail
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := m.Repo.FinishJob(fctx, run); err != nil {
			m.Log.Error("persist job run", "run", run.ID, "err", err)
		}
	}
	eng, node, perr := m.placement(ctx, req.ProjectID)
	if perr != nil {
		finish("failed", perr.Error(), nil, "")
		return
	}
	if err := m.ensureNetwork(ctx, eng, node, req.OrgID, req.ProjectID); err != nil {
		finish("failed", err.Error(), nil, "")
		return
	}
	env, err := m.Secrets.Env(ctx, req.OrgID, req.ProjectID, envProduction)
	if err != nil {
		finish("failed", "secrets: "+err.Error(), nil, "")
		return
	}
	merged := map[string]string{}
	for k, v := range req.Env {
		merged[k] = v
	}
	for k, v := range env {
		merged[k] = v
	}
	spec := dockerx.ContainerSpec{
		Name: run.ContainerName, Image: run.ImageRef, Env: envList(merged), Network: dockerx.ProjectNetwork(req.ProjectID),
		Labels: map[string]string{"jp.managed": "true", "jp.org": req.OrgID, "jp.project": req.ProjectID, "jp.deployment": "", "jp.kind": "job"},
		Memory: m.clampMemory(0), RestartPolicy: "no", Cmd: req.Command, AutoRemove: true,
	}
	id, err := eng.Create(ctx, spec)
	if err != nil {
		finish("failed", "create: "+err.Error(), nil, "")
		return
	}
	if eng.Simulated() {
		zero := 0
		finish("succeeded", "", &zero, "simulated job")
		return
	}
	waiter, err := eng.WaitRegister(ctx, id, "removed")
	if err != nil {
		_ = eng.Remove(context.WithoutCancel(ctx), id, false)
		finish("failed", "wait: "+err.Error(), nil, "")
		return
	}
	defer waiter.Close()
	if err := eng.Start(ctx, id); err != nil {
		_ = eng.Remove(context.WithoutCancel(ctx), id, false)
		finish("failed", "start: "+err.Error(), nil, "")
		return
	}

	// collect a bounded log tail while the job runs (the container disappears on exit)
	tail := newTailBuffer(logTailBytes)
	var logWG sync.WaitGroup
	logCtx, logCancel := context.WithCancel(ctx)
	logWG.Add(1)
	go func() {
		defer logWG.Done()
		_ = eng.FollowLogs(logCtx, id, dockerx.LogOptions{Tail: 1}, 8<<20, func(ev dockerx.LogEvent) bool {
			tail.WriteString(ev.Message + "\n")
			return true
		})
	}()

	type waitRes struct {
		code int
		err  error
	}
	done := make(chan waitRes, 1)
	go func() {
		c, e := waiter.Wait()
		done <- waitRes{c, e}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		logCancel()
		logWG.Wait()
		code := r.code
		switch {
		case r.err != nil:
			finish("failed", r.err.Error(), &code, tail.String())
		case code == 0:
			finish("succeeded", "", &code, tail.String())
		default:
			finish("failed", fmt.Sprintf("exit code %d", code), &code, tail.String())
		}
	case <-timer.C:
		kctx, kcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		_ = eng.Stop(kctx, id, 5*time.Second)
		_ = eng.Remove(kctx, id, false)
		kcancel()
		logCancel()
		logWG.Wait()
		finish("timeout", fmt.Sprintf("job exceeded %s", timeout), nil, tail.String())
	case <-ctx.Done():
		kctx, kcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		_ = eng.Stop(kctx, id, 5*time.Second)
		_ = eng.Remove(kctx, id, false)
		kcancel()
		logCancel()
		logWG.Wait()
		finish("failed", "runtime shutting down", nil, tail.String())
	}
}

// tailBuffer keeps the last N bytes written (bounded memory).
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) WriteString(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, s...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.buf), "?")
}
