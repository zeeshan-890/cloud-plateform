package cluster

import (
	"testing"
	"time"
)

func TestNodeRegistrationAndPlacement(t *testing.T) {
	reg := NewRegistry()

	node1 := &Node{
		ID:       "node-1",
		Hostname: "worker-1.internal",
		IP:       "10.0.0.1",
		Labels:   map[string]string{"tier": "standard"},
		Capacity: NodeCapacity{
			TotalCPUCores: 4.0,
			TotalMemoryMB: 8192,
		},
	}

	node2 := &Node{
		ID:       "node-2",
		Hostname: "worker-2.internal",
		IP:       "10.0.0.2",
		Labels:   map[string]string{"tier": "standard"},
		Capacity: NodeCapacity{
			TotalCPUCores: 8.0,
			TotalMemoryMB: 16384,
		},
	}

	if err := reg.RegisterNode(node1); err != nil {
		t.Fatalf("failed to register node1: %v", err)
	}
	if err := reg.RegisterNode(node2); err != nil {
		t.Fatalf("failed to register node2: %v", err)
	}

	// Placement should pick node2 because it has 16GB available vs 8GB
	chosen, err := reg.SelectPlacementNode(2048, 1.0, map[string]string{"tier": "standard"})
	if err != nil {
		t.Fatalf("placement failed: %v", err)
	}
	if chosen.ID != "node-2" {
		t.Errorf("expected node-2 to be selected for placement, got %s", chosen.ID)
	}

	// Verify allocated memory updated on node2
	if chosen.Capacity.AllocatedMemMB != 2048 {
		t.Errorf("expected 2048MB allocated, got %d", chosen.Capacity.AllocatedMemMB)
	}
}

func TestNodeHeartbeatAndEvacuation(t *testing.T) {
	reg := NewRegistry()

	node := &Node{
		ID:        "node-stale",
		Hostname:  "stale.internal",
		IP:        "10.0.0.99",
		Workloads: []string{"app-deploy-1", "app-deploy-2"},
		Capacity: NodeCapacity{
			TotalCPUCores:  2.0,
			TotalMemoryMB:  4096,
			AllocatedMemMB: 2048,
		},
	}

	_ = reg.RegisterNode(node)
	// Force heartbeat to 5 minutes in the past
	node.HeartbeatAt = time.Now().UTC().Add(-5 * time.Minute)

	evacuated := reg.EvacuateOfflineNodes(2 * time.Minute)
	if len(evacuated) != 2 {
		t.Fatalf("expected 2 evacuated workloads, got %d", len(evacuated))
	}
	if node.Status != NodeStatusOffline {
		t.Errorf("expected node to be offline, got %v", node.Status)
	}
}
