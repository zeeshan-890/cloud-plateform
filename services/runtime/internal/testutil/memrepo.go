package testutil

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/runtime/internal/store"
)

// MemRepo is an in-memory implementation of the runtime repository.
type MemRepo struct {
	mu        sync.Mutex
	Instances map[string]*store.Instance
	Jobs      map[string]*store.JobRun
}

// NewMemRepo returns an empty repository.
func NewMemRepo() *MemRepo {
	return &MemRepo{Instances: map[string]*store.Instance{}, Jobs: map[string]*store.JobRun{}}
}

func cp(in *store.Instance) *store.Instance { c := *in; return &c }

func (m *MemRepo) Create(_ context.Context, in *store.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	in.CreatedAt, in.UpdatedAt = time.Now(), time.Now()
	if in.Role == "" {
		in.Role = "app"
	}
	m.Instances[in.ID] = cp(in)
	return nil
}

func (m *MemRepo) Update(_ context.Context, in *store.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	in.UpdatedAt = time.Now()
	m.Instances[in.ID] = cp(in)
	return nil
}

func (m *MemRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Instances, id)
	return nil
}

func (m *MemRepo) GetByID(_ context.Context, id string) (*store.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in, ok := m.Instances[id]; ok {
		return cp(in), nil
	}
	return nil, store.ErrNotFound
}

func (m *MemRepo) GetByContainerID(_ context.Context, cid string) (*store.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.Instances {
		if in.ContainerID == cid || in.ContainerName == cid {
			return cp(in), nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *MemRepo) filter(f func(*store.Instance) bool) []store.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []store.Instance{}
	for _, in := range m.Instances {
		if f(in) {
			out = append(out, *in)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *MemRepo) FindByDeploymentID(_ context.Context, id string) ([]store.Instance, error) {
	return m.filter(func(in *store.Instance) bool { return in.DeploymentID != nil && *in.DeploymentID == id }), nil
}

func (m *MemRepo) ListActiveProduction(_ context.Context, org, proj string) ([]store.Instance, error) {
	return m.filter(func(in *store.Instance) bool {
		return in.OrgID == org && in.ProjectID == proj && in.Role == "app" && in.DesiredState == "running" && !in.IsPreview &&
			(in.Status == "running" || in.Status == "starting" || in.Status == "unhealthy")
	}), nil
}

func (m *MemRepo) ListByProject(_ context.Context, proj string) ([]store.Instance, error) {
	return m.filter(func(in *store.Instance) bool { return in.ProjectID == proj }), nil
}

func (m *MemRepo) DeleteByProject(_ context.Context, proj string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, in := range m.Instances {
		if in.ProjectID == proj {
			delete(m.Instances, id)
		}
	}
	return nil
}

func (m *MemRepo) ListDesiredRunning(_ context.Context) ([]store.Instance, error) {
	return m.filter(func(in *store.Instance) bool { return in.DesiredState == "running" && in.Status != "starting" }), nil
}

func (m *MemRepo) ListAllWithContainer(_ context.Context) ([]store.Instance, error) {
	return m.filter(func(in *store.Instance) bool { return in.ContainerID != "" }), nil
}

func (m *MemRepo) List(_ context.Context, org, proj string) ([]store.Instance, error) {
	return m.filter(func(in *store.Instance) bool { return in.OrgID == org && in.ProjectID == proj }), nil
}

func (m *MemRepo) Get(_ context.Context, org, proj, id string) (*store.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in, ok := m.Instances[id]; ok && in.OrgID == org && in.ProjectID == proj {
		return cp(in), nil
	}
	return nil, store.ErrNotFound
}

func (m *MemRepo) CreateJob(_ context.Context, j *store.JobRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	j.StartedAt = time.Now()
	c := *j
	m.Jobs[j.ID] = &c
	return nil
}

func (m *MemRepo) FinishJob(_ context.Context, j *store.JobRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	j.FinishedAt = &now
	c := *j
	m.Jobs[j.ID] = &c
	return nil
}

// JobSnapshot returns a copy of a run.
func (m *MemRepo) JobSnapshot(id string) *store.JobRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.Jobs[id]; ok {
		c := *j
		return &c
	}
	return nil
}

func (m *MemRepo) FailStaleJobs(context.Context, time.Time) (int64, error) { return 0, nil }

func (m *MemRepo) ListJobs(_ context.Context, org, proj string, _ int) ([]store.JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []store.JobRun{}
	for _, j := range m.Jobs {
		if j.OrgID == org && j.ProjectID == proj {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (m *MemRepo) GetJob(_ context.Context, org, proj, id string) (*store.JobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.Jobs[id]; ok && j.OrgID == org && j.ProjectID == proj {
		c := *j
		return &c, nil
	}
	return nil, store.ErrNotFound
}
