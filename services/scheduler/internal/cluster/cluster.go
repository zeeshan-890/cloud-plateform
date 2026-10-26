package cluster

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type NodeStatus string

const (
	NodeStatusReady    NodeStatus = "ready"
	NodeStatusDraining NodeStatus = "draining"
	NodeStatusOffline  NodeStatus = "offline"
)

type NodeCapacity struct {
	TotalCPUCores  float64 `json:"total_cpu_cores"`
	AllocatedCPU   float64 `json:"allocated_cpu"`
	TotalMemoryMB  int64   `json:"total_memory_mb"`
	AllocatedMemMB int64   `json:"allocated_memory_mb"`
}

type Node struct {
	ID          string            `json:"id"`
	Hostname    string            `json:"hostname"`
	IP          string            `json:"ip"`
	Labels      map[string]string `json:"labels"`
	Capacity    NodeCapacity      `json:"capacity"`
	Status      NodeStatus        `json:"status"`
	HeartbeatAt time.Time         `json:"heartbeat_at"`
	Workloads   []string          `json:"workloads"`
}

type Registry struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

func NewRegistry() *Registry {
	return &Registry{
		nodes: make(map[string]*Node),
	}
}

func (r *Registry) RegisterNode(n *Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n.ID == "" {
		return errors.New("node ID is required")
	}
	if n.Labels == nil {
		n.Labels = make(map[string]string)
	}
	if n.Status == "" {
		n.Status = NodeStatusReady
	}
	n.HeartbeatAt = time.Now().UTC()
	r.nodes[n.ID] = n
	return nil
}

func (r *Registry) Heartbeat(nodeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}
	node.HeartbeatAt = time.Now().UTC()
	if node.Status == NodeStatusOffline {
		node.Status = NodeStatusReady
	}
	return nil
}

func (r *Registry) DrainNode(nodeID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	node.Status = NodeStatusDraining
	workloads := append([]string(nil), node.Workloads...)
	return workloads, nil
}

// EvacuateOfflineNodes checks heartbeats and marks overdue nodes as offline.
func (r *Registry) EvacuateOfflineNodes(timeout time.Duration) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var evacuatedWorkloads []string
	cutoff := time.Now().UTC().Add(-timeout)

	for _, node := range r.nodes {
		if node.Status != NodeStatusOffline && node.HeartbeatAt.Before(cutoff) {
			node.Status = NodeStatusOffline
			evacuatedWorkloads = append(evacuatedWorkloads, node.Workloads...)
			node.Workloads = nil
			node.Capacity.AllocatedCPU = 0
			node.Capacity.AllocatedMemMB = 0
		}
	}
	return evacuatedWorkloads
}

// SelectPlacementNode finds the ready node with the most available memory and CPU.
func (r *Registry) SelectPlacementNode(reqMemMB int64, reqCPU float64, requiredLabels map[string]string) (*Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var bestNode *Node
	var maxAvailableMem int64 = -1

	for _, node := range r.nodes {
		if node.Status != NodeStatusReady {
			continue
		}

		// Check labels
		match := true
		for k, v := range requiredLabels {
			if node.Labels[k] != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}

		availMem := node.Capacity.TotalMemoryMB - node.Capacity.AllocatedMemMB
		availCPU := node.Capacity.TotalCPUCores - node.Capacity.AllocatedCPU

		if availMem >= reqMemMB && availCPU >= reqCPU {
			if availMem > maxAvailableMem {
				maxAvailableMem = availMem
				bestNode = node
			}
		}
	}

	if bestNode == nil {
		return nil, errors.New("no suitable node with available capacity")
	}

	// Allocate capacity
	bestNode.Capacity.AllocatedMemMB += reqMemMB
	bestNode.Capacity.AllocatedCPU += reqCPU
	return bestNode, nil
}

func (r *Registry) ListNodes() []*Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	return out
}
