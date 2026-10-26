package cluster

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
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
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	IP            string            `json:"ip"`
	Labels        map[string]string `json:"labels"`
	Capacity      NodeCapacity      `json:"capacity"`
	Status        NodeStatus        `json:"status"`
	HeartbeatAt   time.Time         `json:"heartbeat_at"`
	Workloads     []string          `json:"workloads"`
	WorkloadCount int               `json:"workload_count"`
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

// DetectSystemCapacity inspects the host system resources (CPU cores, /proc/meminfo, /proc/loadavg).
func DetectSystemCapacity() NodeCapacity {
	cores := float64(runtime.NumCPU())
	if cores <= 0 {
		cores = 2.0
	}

	totalMB := int64(8192)
	availMB := int64(6144)

	// Read /proc/meminfo on Linux
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		var tKB, aKB int64
		for _, l := range lines {
			parts := strings.Fields(l)
			if len(parts) >= 2 {
				if parts[0] == "MemTotal:" {
					tKB, _ = strconv.ParseInt(parts[1], 10, 64)
				} else if parts[0] == "MemAvailable:" {
					aKB, _ = strconv.ParseInt(parts[1], 10, 64)
				}
			}
		}
		if tKB > 0 {
			totalMB = tKB / 1024
		}
		if aKB > 0 {
			availMB = aKB / 1024
		}
	}

	allocMem := totalMB - availMB
	if allocMem < 0 {
		allocMem = totalMB / 5
	}

	// Read /proc/loadavg
	cpuAlloc := 0.2
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if l, err := strconv.ParseFloat(fields[0], 64); err == nil && l > 0 {
				cpuAlloc = l
			}
		}
	}
	if cpuAlloc > cores {
		cpuAlloc = cores
	}

	return NodeCapacity{
		TotalCPUCores:  cores,
		AllocatedCPU:   math.Round(cpuAlloc*10) / 10,
		TotalMemoryMB:  totalMB,
		AllocatedMemMB: allocMem,
	}
}

// DetectHostNode dynamically builds a Node representation of the host machine.
func DetectHostNode(slot string) *Node {
	hostname := os.Getenv("NODE_HOSTNAME")
	if hostname == "" {
		hostname = os.Getenv("HOST_HOSTNAME")
	}
	if hostname == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			hostname = h
		} else {
			hostname = "control-plane-primary"
		}
	}

	ip := os.Getenv("NODE_IP")
	if ip == "" {
		ip = os.Getenv("PUBLIC_IP")
	}
	if ip == "" {
		ip = os.Getenv("HOST_IP")
	}
	if ip == "" {
		// Outgoing UDP probe to find host local IP
		if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
			if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && localAddr.IP != nil {
				ip = localAddr.IP.String()
			}
			conn.Close()
		}
	}
	if ip == "" {
		ip = "127.0.0.1"
	}

	cap := DetectSystemCapacity()

	return &Node{
		ID:            slot,
		Hostname:      hostname,
		IP:            ip,
		Labels:        map[string]string{"role": "control-plane", "region": "primary"},
		Capacity:      cap,
		Status:        NodeStatusReady,
		HeartbeatAt:   time.Now().UTC(),
		Workloads:     []string{},
		WorkloadCount: 0,
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

func (r *Registry) UpdateNodeCapacity(nodeID string, cap NodeCapacity) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}
	node.Capacity = cap
	node.HeartbeatAt = time.Now().UTC()
	return nil
}

func (r *Registry) ToggleDrain(nodeID string) (NodeStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeID]
	if !exists {
		return "", fmt.Errorf("node %s not found", nodeID)
	}
	if node.Status == NodeStatusDraining {
		node.Status = NodeStatusReady
	} else {
		node.Status = NodeStatusDraining
	}
	node.HeartbeatAt = time.Now().UTC()
	return node.Status, nil
}

func (r *Registry) DrainNode(nodeID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	node.Status = NodeStatusDraining
	node.HeartbeatAt = time.Now().UTC()
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
