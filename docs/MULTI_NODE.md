# JP Multi-Node & Cluster Infrastructure

> **Architecture, Node Agent, Capacity Scheduling, and Workload Recovery for Multi-Server JP Clusters.**

---

## 1. Overview

While single-server JP installations rely on local Docker Compose orchestration, clustered deployments expand to multiple physical or virtual machines using a lightweight **Node Agent** and centralized control plane.

```
                    ┌─────────────────────────┐
   Clients ────────►│    Traefik / Ingress    │
                    └────────────┬────────────┘
                                 │
         ┌───────────────────────┼───────────────────────┐
         ▼                       ▼                       ▼
   Control Plane             Data Plane              Build Farm
  (identity, org,         (Managed Nodes          (Worker Nodes
   deploy, billing...)     running node-agent)     claiming jp.build)
```

---

## 2. Node Agent Architecture

The node agent (`services/node-agent`) is a minimal daemon installed on each managed server.

### Core Responsibilities:
1. **Authenticated Enrollment:** Enrolls with the control plane using a revocable join token.
2. **Heartbeat & Telemetry:** Reports node CPU, RAM, disk capacity, and running container health every 15 seconds.
3. **Reconciliation Loop:** Receives desired state container definitions from `scheduler` and applies them using local container runtime primitives.
4. **Graceful Draining:** Evacuates workloads before node maintenance or shutdown.

---

## 3. Workload Scheduling & Placement Policies

The centralized scheduler evaluates:
- **Available Node Capacity:** CPU and RAM reservations vs available hardware.
- **Affinity & Labels:** Region constraints declared in `jp.yaml` (`deploy.region`).
- **Storage Locality:** Ensures stateful workloads requiring local persistent volumes remain pinned to the appropriate node.

---

## 4. Node Failure & Automatic Recovery

- If a node misses 3 consecutive heartbeats (45 seconds), it is marked `degraded`.
- If missing for > 90 seconds, it is marked `offline`.
- Stateless workloads are automatically rescheduled onto healthy nodes with available capacity.
- Traefik dynamic routing updates to point traffic away from failed nodes.
