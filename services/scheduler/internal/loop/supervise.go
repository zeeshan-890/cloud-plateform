package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// supState is the per-instance restart bookkeeping. The map holding it is pruned to the
// instances the runtime still lists, so it cannot grow without bound.
type supState struct {
	failures int // consecutive failing probes
	restarts int
	nextTry  time.Time
	gaveUp   bool
	healthy  int
}

type desiredInstance struct {
	ID            string `json:"id"`
	RestartPolicy string `json:"restart_policy"`
	Role          string `json:"role"`
	Status        string `json:"status"`
}

type probeResult struct {
	Exists   bool `json:"exists"`
	Running  bool `json:"running"`
	Healthy  bool `json:"healthy"`
	ExitCode int  `json:"exit_code"`
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

func backoff(base, max time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// checkHealth asks the runtime (which owns Docker) to inspect + HTTP-probe every instance
// that should be running, then restarts the ones that are down: same container, exponential
// backoff, bounded retries. It never creates a second instance.
func (s *Scheduler) checkHealth(ctx context.Context) {
	res, err := s.do(ctx, s.cfg.HTTP, http.MethodGet, join(s.cfg.RuntimeURL, "internal", "runtime", "desired"), nil)
	if err != nil || res.Status != http.StatusOK {
		s.cfg.Log.Warn("health: cannot list instances", "err", err)
		return
	}
	var list struct {
		Instances []desiredInstance `json:"instances"`
	}
	if json.Unmarshal(res.Body, &list) != nil {
		return
	}
	seen := make(map[string]struct{}, len(list.Instances))
	for _, in := range list.Instances {
		if ctx.Err() != nil {
			return
		}
		seen[in.ID] = struct{}{}
		s.superviseOne(ctx, in)
	}
	s.supMu.Lock()
	for id := range s.sup {
		if _, ok := seen[id]; !ok {
			delete(s.sup, id)
		}
	}
	s.supMu.Unlock()
}

func (s *Scheduler) superviseOne(ctx context.Context, in desiredInstance) {
	pr, err := s.do(ctx, s.cfg.HTTP, http.MethodPost, join(s.cfg.RuntimeURL, "internal", "runtime", "instances", in.ID, "probe"), nil)
	if err != nil || pr.Status != http.StatusOK {
		return // runtime/docker unreachable: do not act on guesses
	}
	var r probeResult
	if json.Unmarshal(pr.Body, &r) != nil {
		return
	}
	s.supMu.Lock()
	st := s.sup[in.ID]
	if st == nil {
		st = &supState{}
		s.sup[in.ID] = st
	}
	if r.Healthy {
		st.failures = 0
		st.healthy++
		if st.healthy >= 3 { // stable again: forget past restarts
			st.restarts, st.gaveUp = 0, false
		}
		s.supMu.Unlock()
		return
	}
	st.healthy = 0
	st.failures++
	now := time.Now()
	missing := !r.Exists
	needs := !r.Running || st.failures >= s.cfg.UnhealthyProbes
	policy := in.RestartPolicy
	allowed := policy == "always" || (policy != "no" && (r.Running || r.ExitCode != 0 || policy == "on-failure"))
	switch {
	case missing || !needs || !allowed:
		s.supMu.Unlock()
		return
	case st.restarts >= s.cfg.MaxRestarts:
		if !st.gaveUp {
			st.gaveUp = true
			s.cfg.Log.Error("restart limit reached, giving up", "instance", in.ID, "restarts", st.restarts)
		}
		s.supMu.Unlock()
		return
	case now.Before(st.nextTry):
		s.supMu.Unlock()
		return
	}
	st.restarts++
	st.nextTry = now.Add(backoff(s.cfg.BackoffBase, s.cfg.BackoffMax, st.restarts))
	attempt := st.restarts
	s.supMu.Unlock()

	s.cfg.Log.Info("restarting instance", "instance", in.ID, "attempt", attempt, "exit_code", r.ExitCode)
	rr, err := s.do(ctx, s.cfg.DeployHTTP, http.MethodPost, join(s.cfg.RuntimeURL, "internal", "runtime", "instances", in.ID, "restart"), nil)
	if err != nil || rr.Status >= 300 {
		s.cfg.Log.Warn("restart failed", "instance", in.ID, "err", err)
	}
}

// Tracked returns how many instances have supervision state (tests / status).
func (s *Scheduler) Tracked() int {
	s.supMu.Lock()
	defer s.supMu.Unlock()
	return len(s.sup)
}
