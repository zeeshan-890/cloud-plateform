package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type svcSample struct {
	Name string
	Mem  float64 // bytes
	FDs  int     // -1 when unavailable
}

func parseBytes(s string) float64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suf string
		mul float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(s, u.suf), 64)
			return f * u.mul
		}
	}
	return 0
}

// platformContainers returns the names of the jp-* platform containers (tenant containers carry jp.managed=true).
func platformContainers(t *testing.T, e *Env) []string {
	out := e.Run(t, `docker ps --format '{{.Names}}|{{.Label "jp.managed"}}'`)
	var names []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "|")
		if len(f) == 2 && strings.HasPrefix(f[0], "jp-") && f[1] != "true" {
			names = append(names, f[0])
		}
	}
	return names
}

func sample(t *testing.T, e *Env, only []string) map[string]svcSample {
	keep := map[string]bool{}
	for _, n := range only {
		keep[n] = true
	}
	out := e.Run(t, `docker stats --no-stream --format '{{.Name}}|{{.MemUsage}}'`)
	res := map[string]svcSample{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "|")
		if len(f) != 2 || !keep[f[0]] {
			continue
		}
		mem := parseBytes(strings.TrimSpace(strings.Split(f[1], "/")[0]))
		res[f[0]] = svcSample{Name: f[0], Mem: mem, FDs: -1}
	}
	for n, s := range res {
		o, err := e.TryRun("docker exec " + n + " sh -c 'ls /proc/1/fd | wc -l'")
		if err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(o)); err == nil {
				s.FDs = v
				res[n] = s
			}
		}
	}
	return res
}

func TestResources_NoLeaksNoRestartsUnderLoad(t *testing.T) {
	e := NeedSSH(t)
	admin := AdminClient(t)
	org := NewOrg(t, admin)

	platform := platformContainers(t, e)
	if len(platform) < 5 {
		t.Skipf("only %d jp-* platform containers found: %v", len(platform), platform)
	}

	// --- health preconditions
	if bad := e.Run(t, `docker ps --filter health=unhealthy --format '{{.Names}}'`); bad != "" {
		t.Errorf("unhealthy containers before the load phase: %s", bad)
	}
	restarts := func() map[string]int {
		m := map[string]int{}
		for _, n := range platform {
			o := e.Run(t, "docker inspect -f '{{.RestartCount}}' "+n)
			v, _ := strconv.Atoi(strings.TrimSpace(o))
			m[n] = v
		}
		return m
	}
	r0 := restarts()
	for n, v := range r0 {
		if v != 0 {
			t.Errorf("%s has already restarted %d time(s)", n, v)
		}
	}

	// --- warm up, then baseline
	warm := func(n int) {
		for i := 0; i < n; i++ {
			admin.Do("GET", "/auth/me", nil)
			admin.Do("GET", "/orgs", nil)
		}
	}
	warm(100)
	time.Sleep(5 * time.Second)
	base := sample(t, e, platform)

	// --- load: 500 authenticated calls across services + 10 project create/delete cycles
	p0 := NewProject(t, admin, org)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	paths := []string{"/auth/me", "/orgs", "/orgs/" + org.ID, "/orgs/" + org.ID + "/projects", p0.Path(""), p0.Path("/domains"),
		p0.Path("/environments/production/secrets"), p0.Path("/runtime/instances"), p0.Path("/deployments"), p0.Path("/addons"),
		p0.Path("/storage/objects"), "/orgs/" + org.ID + "/users", "/system/cluster", "/system/security"}
	per := 100
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				path := paths[(i+w)%len(paths)]
				r := admin.Do("GET", path, nil)
				if r.Status >= 500 || r.Status == 0 {
					mu.Lock()
					failures++
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	if failures > 0 {
		t.Errorf("%d of %d load requests failed with 5xx/transport errors", failures, 5*per)
	}
	admin.Timeout = 5 * time.Minute
	for i := 0; i < 10; i++ {
		r := admin.Must(t, "POST", "/orgs/"+org.ID+"/projects", map[string]string{"name": fmt.Sprintf("leak %d", i), "slug": RandName("lk")}, 201).Map(t)
		id := Str(r, "project", "id")
		admin.Must(t, "POST", "/orgs/"+org.ID+"/projects/"+id+"/environments/production/secrets", map[string]string{"name": "LEAK_TEST", "value": "x"}, 201)
		admin.Must(t, "DELETE", "/orgs/"+org.ID+"/projects/"+id, nil, 204)
	}
	admin.Timeout = 60 * time.Second

	// --- settle and re-measure
	time.Sleep(30 * time.Second)
	after := sample(t, e, platform)
	for name, b := range base {
		a, ok := after[name]
		if !ok {
			t.Errorf("%s disappeared during the load phase", name)
			continue
		}
		limit := b.Mem*1.30 + 10*(1<<20)
		t.Logf("%-28s mem %6.1f -> %6.1f MiB   fds %3d -> %3d", name, b.Mem/(1<<20), a.Mem/(1<<20), b.FDs, a.FDs)
		if a.Mem > limit {
			t.Errorf("%s memory grew from %.1f to %.1f MiB (limit %.1f)", name, b.Mem/(1<<20), a.Mem/(1<<20), limit/(1<<20))
		}
		if b.FDs >= 0 && a.FDs >= 0 && a.FDs-b.FDs >= 50 {
			t.Errorf("%s open fds grew from %d to %d", name, b.FDs, a.FDs)
		}
	}
	// --- no restarts, nothing unhealthy
	for n, v := range restarts() {
		if v != r0[n] {
			t.Errorf("%s restarted during the run (RestartCount %d -> %d)", n, r0[n], v)
		}
	}
	if bad := e.Run(t, `docker ps --filter health=unhealthy --format '{{.Names}}'`); bad != "" {
		t.Errorf("unhealthy containers after the load phase: %s", bad)
	}
	if exited := e.Run(t, `docker ps -a --filter status=exited --filter status=dead --format '{{.Names}}' | grep '^jp-' | grep -v -E 'init|migrate|setup' || true`); exited != "" {
		t.Errorf("platform containers not running: %s", exited)
	}
	// no tenant leftovers from the 10 create/delete cycles
	if left := e.Run(t, `docker network ls --format '{{.Name}}' | grep -c '^jp_p_' || true`); left != "" {
		t.Logf("jp_p_* networks present now: %s", left)
	}
}
