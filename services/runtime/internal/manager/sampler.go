package manager

import (
	"context"
	"sync"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
)

// Stats is the real node + platform container usage.
type Stats struct {
	Mode             string         `json:"mode"`
	DockerVersion    string         `json:"docker_version"`
	NCPU             int            `json:"ncpu"`
	MemTotal         int64          `json:"mem_total"`
	ContainersAll    int            `json:"containers_total_host"`
	ContainersUp     int            `json:"containers_running_host"`
	Images           int            `json:"images"`
	ManagedTotal     int            `json:"containers_total"`
	ManagedRunning   int            `json:"containers_running"`
	CPUMillicores    float64        `json:"cpu_millicores_used"`
	MemUsedBytes     int64          `json:"mem_used_bytes"`
	ByKind           map[string]int `json:"managed_by_kind"`
	SampledContainer int            `json:"sampled_containers"`
}

// Stats queries Docker: daemon info, platform container counts and the live usage of the
// running tenant containers.
func (m *Manager) Stats(ctx context.Context) (*Stats, error) {
	info, err := m.Engine.Info(ctx)
	if err != nil {
		return nil, err
	}
	s := &Stats{Mode: m.Engine.Mode(), DockerVersion: info.ServerVersion, NCPU: info.NCPU, MemTotal: info.MemTotal,
		ContainersAll: info.Containers, ContainersUp: info.ContainersRunning, Images: info.Images, ByKind: map[string]int{}}
	list, err := m.Engine.List(ctx, "jp.managed=true")
	if err != nil {
		return nil, err
	}
	for _, c := range list {
		s.ManagedTotal++
		if c.State == "running" {
			s.ManagedRunning++
		}
		s.ByKind[c.Labels["jp.kind"]]++
	}
	for _, sm := range m.sampleContainers(ctx, list) {
		s.CPUMillicores += sm.CPU
		s.MemUsedBytes += sm.Mem
		s.SampledContainer++
	}
	return s, nil
}

// ContainerSample is the usage of one tenant container.
type ContainerSample struct {
	OrgID, ProjectID, Kind string
	CPU                    float64
	Mem                    int64
}

const maxSampled = 200

// sampleContainers reads usage of the running managed containers (bounded, 8 in parallel).
func (m *Manager) sampleContainers(ctx context.Context, list []dockerx.ContainerSummary) []ContainerSample {
	if m.Engine.Simulated() {
		return nil
	}
	var running []dockerx.ContainerSummary
	for _, c := range list {
		if c.State == "running" && c.Labels["jp.org"] != "" && c.Labels["jp.project"] != "" {
			running = append(running, c)
		}
	}
	if len(running) > maxSampled {
		running = running[:maxSampled]
	}
	out := make([]ContainerSample, len(running))
	ok := make([]bool, len(running))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, c := range running {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c dockerx.ContainerSummary) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			u, err := m.Engine.ContainerStats(cctx, c.ID)
			if err != nil {
				return
			}
			out[i] = ContainerSample{OrgID: c.Labels["jp.org"], ProjectID: c.Labels["jp.project"], Kind: c.Labels["jp.kind"], CPU: u.CPUMillicores, Mem: u.MemBytes}
			ok[i] = true
		}(i, c)
	}
	wg.Wait()
	res := make([]ContainerSample, 0, len(out))
	for i := range out {
		if ok[i] {
			res = append(res, out[i])
		}
	}
	return res
}

// MetricsSink receives samples (the metrics service client).
type MetricsSink interface {
	Push(ctx context.Context, samples []MetricSample) error
}

// MetricSample matches POST /internal/metrics.
type MetricSample struct {
	OrgID      string            `json:"org_id"`
	ProjectID  string            `json:"project_id"`
	Name       string            `json:"name"`
	Value      float64           `json:"value"`
	Labels     map[string]string `json:"labels,omitempty"`
	RecordedAt time.Time         `json:"recorded_at"`
}

// RunSampler pushes container CPU / memory samples to the metrics service every interval
// until ctx is cancelled.
func (m *Manager) RunSampler(ctx context.Context, interval time.Duration, sink MetricsSink) {
	if interval <= 0 || sink == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sctx, cancel := context.WithTimeout(ctx, interval)
			if err := m.SampleOnce(sctx, sink); err != nil && ctx.Err() == nil {
				m.Log.Warn("metrics sampling failed", "err", err)
			}
			cancel()
		}
	}
}

// SampleOnce collects one round of samples and pushes it.
func (m *Manager) SampleOnce(ctx context.Context, sink MetricsSink) error {
	list, err := m.Engine.List(ctx, "jp.managed=true")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var batch []MetricSample
	for _, c := range m.sampleContainers(ctx, list) {
		l := map[string]string{"kind": c.Kind}
		batch = append(batch,
			MetricSample{OrgID: c.OrgID, ProjectID: c.ProjectID, Name: "container_cpu_millicores", Value: c.CPU, Labels: l, RecordedAt: now},
			MetricSample{OrgID: c.OrgID, ProjectID: c.ProjectID, Name: "container_memory_bytes", Value: float64(c.Mem), Labels: l, RecordedAt: now})
	}
	if len(batch) == 0 {
		return nil
	}
	return sink.Push(ctx, batch)
}
