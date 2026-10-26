"use client";

import { useState } from "react";
import { PageHeader, Alert } from "@/components/ui/page";
import { Button } from "@/components/ui/button";

interface ClusterNode {
  id: string;
  hostname: string;
  ip: string;
  role: string;
  status: "ready" | "draining" | "offline";
  cpuCores: number;
  cpuAllocated: number;
  memoryMB: number;
  memoryAllocated: number;
  workloadCount: number;
  lastHeartbeat: string;
}

const initialNodes: ClusterNode[] = [
  {
    id: "node-1",
    hostname: "control-plane-primary",
    ip: "127.0.0.1",
    role: "Control Plane + Worker",
    status: "ready",
    cpuCores: 4.0,
    cpuAllocated: 0.8,
    memoryMB: 8192,
    memoryAllocated: 1536,
    workloadCount: 4,
    lastHeartbeat: "Just now",
  },
];

export default function ClusterPage() {
  const [nodes, setNodes] = useState<ClusterNode[]>(initialNodes);
  const [message, setMessage] = useState("");
  const [showEnrollModal, setShowEnrollModal] = useState(false);
  const [enrollToken, setEnrollToken] = useState("jp_node_tok_8f93a0c24e81b67f");

  const totalCores = nodes.reduce((acc, n) => acc + n.cpuCores, 0);
  const totalMemoryGB = (nodes.reduce((acc, n) => acc + n.memoryMB, 0) / 1024).toFixed(1);
  const activeNodesCount = nodes.filter((n) => n.status === "ready").length;

  const handleDrain = (nodeId: string) => {
    setNodes((prev) =>
      prev.map((n) => (n.id === nodeId ? { ...n, status: n.status === "ready" ? "draining" : "ready" } : n))
    );
    setMessage(`Node ${nodeId} status updated. Workloads will automatically be migrated.`);
  };

  const handleGenerateToken = () => {
    const randomHex = Math.random().toString(16).substring(2, 10) + Math.random().toString(16).substring(2, 10);
    setEnrollToken(`jp_node_tok_${randomHex}`);
    setShowEnrollModal(true);
  };

  return (
    <div className="space-y-8 max-w-5xl">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <PageHeader
          title="Servers & Cluster"
          description="Monitor connected nodes, scale worker capacity, and add servers to your JP cluster."
        />
        <Button onClick={handleGenerateToken}>
          + Connect New Server
        </Button>
      </div>

      {message && (
        <Alert tone="info">
          {message}
        </Alert>
      )}

      {/* Cluster Capacity Stats */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
          <p className="text-xs font-medium text-[var(--ink-muted)]">Active Nodes</p>
          <p className="text-2xl font-bold text-[var(--ink)] mt-1">{activeNodesCount} / {nodes.length}</p>
          <p className="text-xs text-emerald-400 mt-1">Quorum healthy</p>
        </div>

        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
          <p className="text-xs font-medium text-[var(--ink-muted)]">Total CPU Headroom</p>
          <p className="text-2xl font-bold text-[var(--ink)] mt-1">{totalCores} vCPUs</p>
          <p className="text-xs text-[var(--ink-muted)] mt-1">Capacity-aware scheduling</p>
        </div>

        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
          <p className="text-xs font-medium text-[var(--ink-muted)]">Cluster Memory</p>
          <p className="text-2xl font-bold text-[var(--ink)] mt-1">{totalMemoryGB} GB RAM</p>
          <p className="text-xs text-[var(--ink-muted)] mt-1">Shared workload allocation</p>
        </div>
      </div>

      {/* Nodes List */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] overflow-hidden">
        <div className="p-5 border-b border-[var(--border)]">
          <h2 className="text-base font-semibold text-[var(--ink)]">Connected Server Nodes</h2>
          <p className="text-xs text-[var(--ink-muted)]">
            Nodes automatically report heartbeats and receive desired-state container instructions.
          </p>
        </div>

        <div className="divide-y divide-[var(--border)]">
          {nodes.map((node) => {
            const cpuPercent = Math.round((node.cpuAllocated / node.cpuCores) * 100);
            const memPercent = Math.round((node.memoryAllocated / node.memoryMB) * 100);

            return (
              <div key={node.id} className="p-5 space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="flex items-center gap-3">
                    <span
                      className={`inline-block w-2.5 h-2.5 rounded-full ${
                        node.status === "ready"
                          ? "bg-emerald-500"
                          : node.status === "draining"
                          ? "bg-amber-500"
                          : "bg-red-500"
                      }`}
                    />
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-sm text-[var(--ink)]">{node.hostname}</span>
                        <span className="text-xs font-mono px-2 py-0.5 rounded bg-[var(--paper)] text-[var(--ink-muted)] border border-[var(--border)]">
                          {node.id}
                        </span>
                      </div>
                      <p className="text-xs text-[var(--ink-muted)]">IP: {node.ip} • Role: {node.role}</p>
                    </div>
                  </div>

                  <div className="flex items-center gap-2">
                    <Button
                      variant="secondary"
                      onClick={() => handleDrain(node.id)}
                    >
                      {node.status === "draining" ? "Resume Node" : "Drain Node"}
                    </Button>
                  </div>
                </div>

                {/* Resource Allocation Bars */}
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 pt-1">
                  <div>
                    <div className="flex justify-between text-xs text-[var(--ink-muted)] mb-1">
                      <span>CPU Allocation: {node.cpuAllocated} / {node.cpuCores} Cores</span>
                      <span>{cpuPercent}%</span>
                    </div>
                    <div className="h-1.5 w-full bg-[var(--paper)] rounded-full overflow-hidden border border-[var(--border)]">
                      <div
                        className="h-full bg-emerald-500 rounded-full"
                        style={{ width: `${Math.min(cpuPercent, 100)}%` }}
                      />
                    </div>
                  </div>

                  <div>
                    <div className="flex justify-between text-xs text-[var(--ink-muted)] mb-1">
                      <span>Memory: {node.memoryAllocated} / {node.memoryMB} MB</span>
                      <span>{memPercent}%</span>
                    </div>
                    <div className="h-1.5 w-full bg-[var(--paper)] rounded-full overflow-hidden border border-[var(--border)]">
                      <div
                        className="h-full bg-blue-500 rounded-full"
                        style={{ width: `${Math.min(memPercent, 100)}%` }}
                      />
                    </div>
                  </div>
                </div>

                <div className="flex items-center justify-between text-xs text-[var(--ink-faint)] pt-1">
                  <span>Running Workloads: {node.workloadCount} containers</span>
                  <span>Last Heartbeat: {node.lastHeartbeat}</span>
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Connect Server Modal / Card */}
      {showEnrollModal && (
        <div className="rounded-xl border border-[var(--brand)] bg-[var(--surface)] p-6 space-y-4">
          <div className="flex justify-between items-center">
            <h3 className="font-semibold text-[var(--ink)]">Enroll a New Linux Server</h3>
            <button
              onClick={() => setShowEnrollModal(false)}
              className="text-xs text-[var(--ink-muted)] hover:text-[var(--ink)]"
            >
              Close
            </button>
          </div>

          <p className="text-xs text-[var(--ink-muted)]">
            Run this single command on your worker VPS to install the lightweight node agent and register it with this control plane:
          </p>

          <div className="rounded-lg bg-[var(--paper)] p-4 font-mono text-xs text-[var(--brand)] border border-[var(--border)] select-all overflow-x-auto">
            curl -fsSL https://get.jpcloud.dev/agent | bash -s -- --control-plane https://panel.example.com --token {enrollToken}
          </div>

          <p className="text-[11px] text-[var(--ink-faint)]">
            Tokens expire in 1 hour and grant least-privilege node identity with automated heartbeats.
          </p>
        </div>
      )}
    </div>
  );
}
