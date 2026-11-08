package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jp-cloud/events"
	"github.com/redis/go-redis/v9"
)

func TestBuildPreviewHostname(t *testing.T) {
	tests := []struct {
		branch, pid, base string
		pr                int
		want              string
	}{
		{"preview/feature-login", "proj-uuid-1234", "preview.example.com", 0, "b-feature-login-projuuid.preview.example.com"},
		{"preview/pr-42", "12345678-abcd", "preview.example.com", 0, "pr-42-12345678.preview.example.com"},
		{"anything", "12345678-abcd", "preview.example.com", 7, "pr-7-12345678.preview.example.com"},
		{"main", "abcdef12-3456", "preview.example.com", 0, "b-main-abcdef12.preview.example.com"},
		{"evil;rm -rf/../x", "abcdef12-3456", "p.example.com", 0, "b-evil-rm-rf-x-abcdef12.p.example.com"},
	}
	for _, tt := range tests {
		if got := buildPreviewHostname(tt.branch, tt.pid, tt.base, tt.pr); got != tt.want {
			t.Errorf("got %q want %q", got, tt.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	if backoff(10*time.Second, time.Minute, 1) != 10*time.Second || backoff(10*time.Second, time.Minute, 3) != 40*time.Second ||
		backoff(10*time.Second, time.Minute, 10) != time.Minute {
		t.Fatal("backoff schedule wrong")
	}
}

type fakeRuntime struct {
	mu       sync.Mutex
	calls    []string
	desired  []map[string]any
	probe    map[string]map[string]any
	startFn  func(body map[string]any) (int, string)
	jobCalls []map[string]any
}

func (f *fakeRuntime) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.URL.Path == "/internal/runtime/desired":
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": f.desired})
		case strings.HasSuffix(r.URL.Path, "/probe"):
			id := strings.Split(r.URL.Path, "/")[4]
			_ = json.NewEncoder(w).Encode(f.probe[id])
		case strings.HasSuffix(r.URL.Path, "/restart"):
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/internal/runtime/start":
			code, resp := f.startFn(body)
			w.WriteHeader(code)
			_, _ = w.Write([]byte(resp))
		case r.URL.Path == "/internal/runtime/jobs":
			f.jobCalls = append(f.jobCalls, body)
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"job":{"id":"run-1"}}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fakeRuntime) count(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

type fakeDeployment struct {
	mu       sync.Mutex
	statuses []map[string]any
}

func (f *fakeDeployment) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.statuses = append(f.statuses, body)
		f.mu.Unlock()
		w.WriteHeader(204)
	})
}

func newSched(t *testing.T, rt *fakeRuntime, dep *fakeDeployment) *Scheduler {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	rs := httptest.NewServer(rt.handler())
	t.Cleanup(rs.Close)
	ds := httptest.NewServer(dep.handler())
	t.Cleanup(ds.Close)
	return New(Config{Redis: rdb, RuntimeURL: rs.URL, DeploymentURL: ds.URL, DomainURL: ds.URL, PreviewBaseDomain: "preview.test",
		BackoffBase: time.Nanosecond, ReadBlock: 20 * time.Millisecond, MaxRestarts: 2, UnhealthyProbes: 2, Consumer: "test"})
}

func payload(status string, extra map[string]any) map[string]any {
	p := map[string]any{"deployment_id": "d1", "org_id": "o1", "project_id": "p1", "image_ref": "reg/app:1", "status": status, "strategy": "blue_green",
		"spec": map[string]any{"port": 8080}}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func TestDeployEventSuccessAndFailureReportTruthfully(t *testing.T) {
	rt, dep := &fakeRuntime{}, &fakeDeployment{}
	rt.startFn = func(b map[string]any) (int, string) {
		if b["strategy"] != "blue_green" || b["spec"].(map[string]any)["port"].(float64) != 8080 {
			t.Errorf("start body = %v", b)
		}
		return 201, `{"instance":{}}`
	}
	s := newSched(t, rt, dep)
	if err := s.startFromEvent(context.Background(), payload("deploying", nil)); err != nil {
		t.Fatal(err)
	}
	if got := dep.statuses[0]["status"]; got != "ready" {
		t.Fatalf("statuses = %v", dep.statuses)
	}

	// health gate failure (422) => deployment FAILED with the reason, no retry error
	rt.startFn = func(map[string]any) (int, string) {
		return 422, `{"error":{"code":"deploy_failed","message":"deploy failed at health_gate: timed out"},"stage":"health_gate"}`
	}
	if err := s.startFromEvent(context.Background(), payload("deploying", nil)); err != nil {
		t.Fatal(err)
	}
	last := dep.statuses[len(dep.statuses)-1]
	if last["status"] != "failed" || !strings.Contains(last["error"].(string), "health_gate") {
		t.Fatalf("last = %v", last)
	}

	// runtime down (5xx) => transient error, nothing reported yet
	n := len(dep.statuses)
	rt.startFn = func(map[string]any) (int, string) { return 503, "" }
	if err := s.startFromEvent(context.Background(), payload("deploying", nil)); err == nil {
		t.Fatal("5xx must be a retryable error")
	}
	if len(dep.statuses) != n {
		t.Fatal("must not report on transient failure")
	}
	if err := s.startFromEvent(context.Background(), map[string]any{"status": "deploying"}); err == nil {
		t.Fatal("incomplete event must be rejected")
	}
}

func TestStreamRetryAndDeadLetter(t *testing.T) {
	rt, dep := &fakeRuntime{}, &fakeDeployment{}
	rt.startFn = func(map[string]any) (int, string) { return 503, "" }
	s := newSched(t, rt, dep)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = events.EnsureGroup(ctx, s.cfg.Redis, events.TopicDeploy, events.SchedulerConsumerGroup)
	// non-deploying events (billing shares the stream) are acked without side effects
	_, _ = events.PublishJSON(ctx, s.cfg.Redis, events.TopicDeploy, events.New(events.TopicDeploy, events.TypeDeployUpdated, "", "o1", payload("ready", nil)))
	_, _ = events.PublishJSON(ctx, s.cfg.Redis, events.TopicDeploy, events.New(events.TopicDeploy, events.TypeDeployUpdated, "", "o1", payload("deploying", nil)))
	s.cfg.ClaimIdle = time.Millisecond
	s.cfg.ClaimEvery = time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.consume(ctx, events.TopicDeploy, events.SchedulerConsumerGroup, s.handleDeploy, s.deadLetterDeploy)
	}()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		dep.mu.Lock()
		n := len(dep.statuses)
		dep.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	dep.mu.Lock()
	defer dep.mu.Unlock()
	if len(dep.statuses) != 1 || dep.statuses[0]["status"] != "failed" || !strings.Contains(dep.statuses[0]["error"].(string), "gave up") {
		t.Fatalf("dead letter did not fail the deployment: %v", dep.statuses)
	}
	if rt.count("/internal/runtime/start") != s.cfg.MaxEventRetries {
		t.Fatalf("start attempts = %d, want %d", rt.count("/internal/runtime/start"), s.cfg.MaxEventRetries)
	}
	pending, _ := s.cfg.Redis.XPending(context.Background(), events.TopicDeploy, events.SchedulerConsumerGroup).Result()
	if pending.Count != 0 {
		t.Fatalf("pending = %d, message must be acked after dead-letter", pending.Count)
	}
}

func TestSupervisionRestartsWithBackoffBoundsAndDoesNotDuplicate(t *testing.T) {
	rt, dep := &fakeRuntime{}, &fakeDeployment{}
	rt.desired = []map[string]any{{"id": "i1", "restart_policy": "on-failure"}, {"id": "i2", "restart_policy": "no"}, {"id": "i3", "restart_policy": "on-failure"}}
	rt.probe = map[string]map[string]any{
		"i1": {"exists": true, "running": false, "healthy": false, "exit_code": 1},
		"i2": {"exists": true, "running": false, "healthy": false, "exit_code": 1},
		"i3": {"exists": true, "running": true, "healthy": true},
	}
	s := newSched(t, rt, dep)
	for i := 0; i < 6; i++ {
		s.checkHealth(context.Background())
	}
	if n := rt.count("/instances/i1/restart"); n != 2 { // MaxRestarts
		t.Fatalf("i1 restarts = %d, want 2 (bounded)", n)
	}
	if rt.count("/instances/i2/restart") != 0 {
		t.Fatal("restart policy 'no' must never restart")
	}
	if rt.count("/instances/i3/restart") != 0 {
		t.Fatal("healthy instance restarted")
	}
	if rt.count("/internal/runtime/start") != 0 {
		t.Fatal("supervision must never go through the deploy path (duplicate instances)")
	}
	if s.Tracked() != 3 {
		t.Fatalf("tracked = %d", s.Tracked())
	}
	rt.mu.Lock()
	rt.desired = rt.desired[:1]
	rt.mu.Unlock()
	s.checkHealth(context.Background())
	if s.Tracked() != 1 {
		t.Fatalf("state map must be pruned, tracked = %d", s.Tracked())
	}
}

func TestUnhealthyRunningNeedsConsecutiveFailures(t *testing.T) {
	rt, dep := &fakeRuntime{}, &fakeDeployment{}
	rt.desired = []map[string]any{{"id": "i1", "restart_policy": "always"}}
	rt.probe = map[string]map[string]any{"i1": {"exists": true, "running": true, "healthy": false}}
	s := newSched(t, rt, dep)
	s.checkHealth(context.Background())
	if rt.count("/restart") != 0 {
		t.Fatal("a single failed probe must not restart")
	}
	s.checkHealth(context.Background())
	if rt.count("/restart") != 1 {
		t.Fatalf("restarts = %d", rt.count("/restart"))
	}
	// container gone: no restart (cannot recreate), state already marked by the runtime
	rt.mu.Lock()
	rt.probe["i1"] = map[string]any{"exists": false}
	rt.mu.Unlock()
	for i := 0; i < 4; i++ {
		s.checkHealth(context.Background())
	}
	if rt.count("/restart") != 1 {
		t.Fatal("missing container must not be restarted")
	}
}

func TestCronJobsUseJobEndpointNeverStart(t *testing.T) {
	rt, dep := &fakeRuntime{}, &fakeDeployment{}
	s := newSched(t, rt, dep)
	env := events.New(events.TopicJobs, events.TypeCronTriggered, "scheduler", "o1", map[string]any{
		"cron_id": "c1", "org_id": "o1", "project_id": "p1", "name": "nightly", "image_ref": "", "command": []string{"node", "job.js"}, "timeout_seconds": 60})
	raw, _ := json.Marshal(env)
	msg := redis.XMessage{ID: "1-0", Values: map[string]any{"payload": string(raw)}}
	if err := s.handleJob(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if rt.count("/internal/runtime/start") != 0 || rt.count("/internal/runtime/jobs") != 1 {
		t.Fatalf("calls = %v", rt.calls)
	}
	if got := rt.jobCalls[0]["command"].([]any); len(got) != 2 || got[0] != "node" {
		t.Fatalf("job body = %v", rt.jobCalls[0])
	}
}

func TestSanitizeHostLabel(t *testing.T) {
	for in, want := range map[string]string{"feature_branch": "feature-branch", "UPPER-case-123": "upper-case-123", "--multiple---hyphens--": "multiple-hyphens", "": "x"} {
		if got := sanitizeHostLabel(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
}

func TestRunStopsAllGoroutines(t *testing.T) {
	rt, dep := &fakeRuntime{}, &fakeDeployment{}
	s := newSched(t, rt, dep)
	s.cfg.HealthEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel (goroutine leak)")
	}
}
