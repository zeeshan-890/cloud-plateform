package manager

import (
	"context"
	"errors"
	"fmt"
	mrand "math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/store"
)

// NodeRepo is the node and placement persistence the manager needs (implemented by *store.Store).
type NodeRepo interface {
	GetNode(ctx context.Context, id string) (*store.Node, error)
	Placement(ctx context.Context, projectID string) (string, error)
	DeletePlacement(ctx context.Context, projectID string) error
}

// ErrNodeUnavailable means the node a project is placed on is disabled, deleted or cannot be reached.
var ErrNodeUnavailable = errors.New("the server this project runs on is unavailable")

type cachedEngine struct {
	eng  *dockerx.Engine
	node *store.Node
	at   time.Time
}

type engineCache struct {
	mu sync.Mutex
	m  map[string]cachedEngine
}

const engineTTL = 15 * time.Second

// SealNodeToken seals a node agent token for storage.
func SealNodeToken(key []byte, nodeName, token string) (string, error) {
	return secretbox.Seal(key, token, "runtime-node:"+nodeName)
}

// nodeEngine returns the engine for a node id ("" = this server's own Docker daemon).
func (m *Manager) nodeEngine(ctx context.Context, nodeID string) (*dockerx.Engine, *store.Node, error) {
	if nodeID == "" || m.Engine.Simulated() {
		return m.Engine, nil, nil
	}
	if m.Nodes == nil {
		return nil, nil, fmt.Errorf("%w: multi-server support is not configured", ErrNodeUnavailable)
	}
	m.engines.mu.Lock()
	if c, ok := m.engines.m[nodeID]; ok && time.Since(c.at) < engineTTL {
		m.engines.mu.Unlock()
		return c.eng, c.node, nil
	}
	m.engines.mu.Unlock()

	n, err := m.Nodes.GetNode(ctx, nodeID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: node not found", ErrNodeUnavailable)
	}
	if !n.Enabled {
		return nil, nil, fmt.Errorf("%w: node %s is disabled", ErrNodeUnavailable, n.Name)
	}
	token, err := secretbox.Open(m.NodeKey, n.TokenSealed, "runtime-node:"+n.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: node %s credentials cannot be read", ErrNodeUnavailable, n.Name)
	}
	eng := dockerx.NewRemote(n.Addr, token)
	m.engines.mu.Lock()
	if m.engines.m == nil {
		m.engines.m = map[string]cachedEngine{}
	}
	m.engines.m[nodeID] = cachedEngine{eng: eng, node: n, at: time.Now()}
	m.engines.mu.Unlock()
	return eng, n, nil
}

// engOf returns the engine an instance runs on. It never returns nil: an unreachable node yields an engine whose
// calls fail with ErrUnavailable, so supervision reports the problem instead of touching the wrong server.
func (m *Manager) engOf(ctx context.Context, in *store.Instance) *dockerx.Engine {
	eng, _, err := m.nodeEngine(ctx, in.NodeID)
	if err != nil {
		return dockerx.NewUnavailable(fmt.Errorf("%w: %v", dockerx.ErrUnavailable, err))
	}
	return eng
}

// placement resolves where NEW containers of a project go.
func (m *Manager) placement(ctx context.Context, projectID string) (*dockerx.Engine, *store.Node, error) {
	if m.Nodes == nil || m.Engine.Simulated() {
		return m.Engine, nil, nil
	}
	id, err := m.Nodes.Placement(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	return m.nodeEngine(ctx, id)
}

// NodeEngine exposes node engines to the HTTP layer (log streaming, exec).
func (m *Manager) NodeEngine(ctx context.Context, nodeID string) (*dockerx.Engine, error) {
	e, _, err := m.nodeEngine(ctx, nodeID)
	return e, err
}

// InvalidateNode drops a cached node engine (after the node was edited or deleted).
func (m *Manager) InvalidateNode(nodeID string) {
	m.engines.mu.Lock()
	delete(m.engines.m, nodeID)
	m.engines.mu.Unlock()
}

// engineRef is one server a project has (or had) containers on.
type engineRef struct {
	eng  *dockerx.Engine
	node *store.Node // nil = this server
	err  error
}

// projectEngines returns every server a project may have containers on: this one, the one it is placed on and any
// server one of its instance rows points at.
func (m *Manager) projectEngines(ctx context.Context, projectID string) []engineRef {
	out := []engineRef{{eng: m.Engine}}
	if m.Nodes == nil || m.Engine.Simulated() {
		return out
	}
	ids := map[string]bool{}
	if id, err := m.Nodes.Placement(ctx, projectID); err == nil && id != "" {
		ids[id] = true
	}
	if rows, err := m.Repo.ListByProject(ctx, projectID); err == nil {
		for _, r := range rows {
			if r.NodeID != "" {
				ids[r.NodeID] = true
			}
		}
	}
	for id := range ids {
		e, n, err := m.nodeEngine(ctx, id)
		out = append(out, engineRef{eng: e, node: n, err: err})
	}
	return out
}

// platformRe finds "linux/amd64/v3" or "linux/arm64" in the daemon's "no matching manifest" message.
var platformRe = regexp.MustCompile(`linux/(amd64|arm64|arm|386|ppc64le|s390x)`)

// friendlyImageErr turns the daemon's cryptic "no matching manifest" into something a person can act on: the image
// was built for another CPU architecture than the server it was placed on.
func friendlyImageErr(image string, err error) error {
	if err == nil || !strings.Contains(err.Error(), "no matching manifest") {
		return err
	}
	want := "this server's CPU architecture"
	if m := platformRe.FindString(err.Error()); m != "" {
		want = m
	}
	return fmt.Errorf("image %s has no build for %s, the CPU architecture of the server this project runs on. "+
		"Images are built on the control plane, so a server with a different architecture (for example an x86 server with an ARM control plane) "+
		"cannot run them. Run this project on a server with the same architecture as the control plane, or push a multi-architecture image: %w", image, want, err)
}

// Published app ports on a node live in this range (the join command opens exactly it for the control plane).
const (
	hostPortMin = 32768
	hostPortMax = 60999
)

// allocHostPort picks a host port on a node that no known instance uses. The daemon still has the last word: a
// port taken by something else makes the start fail with "already allocated" and the caller retries with another.
func (m *Manager) allocHostPort(ctx context.Context, nodeID string, tried map[int]bool) (int, error) {
	used := map[int]bool{}
	for p := range tried {
		used[p] = true
	}
	rows, err := m.Repo.ListAllWithContainer(ctx)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if r.NodeID == nodeID && r.HostPort > 0 {
			used[r.HostPort] = true
		}
	}
	for i := 0; i < 200; i++ {
		p := hostPortMin + mrand.Intn(hostPortMax-hostPortMin+1)
		if !used[p] {
			return p, nil
		}
	}
	return 0, errors.New("no free published port left on this server")
}

// portTaken reports whether a daemon error means the requested host port is already in use.
func portTaken(err error) bool {
	if err == nil {
		return false
	}
	l := strings.ToLower(err.Error())
	return strings.Contains(l, "port is already allocated") || strings.Contains(l, "address already in use") || strings.Contains(l, "bind for")
}

// nodeDown rewrites "the Docker daemon cannot be reached" into a message that names the server, so the operator
// knows which server and what to check. Errors that are not about reachability pass through unchanged.
func nodeDown(node *store.Node, err error) error {
	if node == nil || err == nil || !errors.Is(err, dockerx.ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: server %s is not answering: its node agent is stopped, or the network blocks the control plane from reaching it: %w", ErrNodeUnavailable, node.Name, err)
}
