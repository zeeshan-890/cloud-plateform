package dockerx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// UsageSample is the resource usage of one running container.
type UsageSample struct {
	CPUMillicores float64
	MemBytes      int64
}

// ContainerStats takes a usage sample (CPU needs two readings, so the daemon blocks
// about a second).
func (e *Engine) ContainerStats(ctx context.Context, id string) (*UsageSample, error) {
	seg, err := segment(id)
	if err != nil {
		return nil, err
	}
	if e.Simulated() {
		return &UsageSample{}, nil
	}
	q := url.Values{}
	q.Set("stream", "false")
	b, _, err := e.call(ctx, http.MethodGet, "/containers/"+seg+"/stats", q, nil)
	if err != nil {
		return nil, err
	}
	var st struct {
		CPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		Mem struct {
			Usage uint64 `json:"usage"`
			Stats struct {
				InactiveFile uint64 `json:"inactive_file"`
				Cache        uint64 `json:"cache"`
			} `json:"stats"`
		} `json:"memory_stats"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	out := &UsageSample{}
	mem := st.Mem.Usage
	if sub := st.Mem.Stats.InactiveFile; sub > 0 && sub < mem {
		mem -= sub
	} else if c := st.Mem.Stats.Cache; c > 0 && c < mem {
		mem -= c
	}
	out.MemBytes = int64(mem)
	cpuDelta := float64(st.CPU.Usage.Total) - float64(st.PreCPU.Usage.Total)
	sysDelta := float64(st.CPU.System) - float64(st.PreCPU.System)
	if cpuDelta > 0 && sysDelta > 0 {
		n := st.CPU.Online
		if n <= 0 {
			n = 1
		}
		out.CPUMillicores = cpuDelta / sysDelta * float64(n) * 1000
	}
	return out, nil
}
