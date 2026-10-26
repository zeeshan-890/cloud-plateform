# JP Cloud Platform — Operations, Observability & Runbooks

This guide covers operational monitoring, health checks, metrics, alerting thresholds, log aggregation, and emergency incident runbooks for the JP Open-Source Self-Hosted Cloud Platform.

---

## 1. Observability Architecture

The platform follows a zero-telemetry, fully self-hosted observability model:
- **Metrics**: Prometheus pulls `/metrics` endpoints from all 22 microservices, Traefik edge router, and node-agents.
- **Visuals**: Pre-provisioned Grafana dashboards for cluster capacity, container runtimes, API latency, and queue lag.
- **Logs**: Structured JSON logs written to container stdout, collected via Docker log driver or Vector sidecar, indexed locally.
- **Tracing / Correlation**: Every incoming HTTP request is assigned a `UUIDv4` request ID (`X-Request-ID`), forwarded downstream across all microservices and logged in Redis Stream event envelopes.

---

## 2. Health Checks & Probes

Every microservice exposes standard health endpoints:

| Endpoint | Purpose | Success Criteria | Behavior on Failure |
| :--- | :--- | :--- | :--- |
| `GET /healthz` | Liveness & Readiness | HTTP 200 `{"status":"ok"}` | Docker restart / Traefik pulls from pool |
| `GET /metrics` | Prometheus Metrics Scrape | HTTP 200 Prometheus text format | Alerting triggered for scrape failure |

### Docker Healthcheck Example
```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://localhost:8001/healthz"]
  interval: 10s
  timeout: 3s
  retries: 3
  start_period: 5s
```

---

## 3. Metrics Dictionary

Prometheus scrapes core runtime and business metrics across services:

### HTTP & Gateway Metrics
- `http_requests_total{service="gateway",status="200",method="POST"}`: Total requests handled by service and response code.
- `http_request_duration_seconds{service="deploy",quantile="0.99"}`: Latency percentiles (p50, p95, p99).
- `http_requests_in_flight{service="auth"}`: Current concurrent requests.

### Event Bus & Worker Metrics
- `redis_stream_length{stream="jp.build"}`: Number of pending events in stream.
- `redis_consumer_group_lag{stream="jp.build",group="jp-workers"}`: Unprocessed builds in queue.
- `build_duration_seconds{status="success"}`: Build pipeline execution time.

### System & Infrastructure Metrics
- `node_cpu_utilization_ratio`: CPU usage per node.
- `node_memory_utilization_ratio`: Memory usage per node.
- `node_disk_available_bytes{mount="/"}`: Free disk space.
- `docker_container_count{status="running"}`: Active tenant containers.

---

## 4. Alerting Thresholds & Rules

| Alert Rule | Metric Condition | Severity | Action Triggered |
| :--- | :--- | :--- | :--- |
| **HighApiErrorRate** | `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) > 0.02` | Critical | Page on-call, check error logs |
| **BuildQueueStalled** | `redis_consumer_group_lag{stream="jp.build"} > 10` for 10m | Warning | Scale worker replicas or restart worker pool |
| **LowDiskSpace** | `node_disk_available_bytes / node_disk_size_bytes < 0.15` | Critical | Trigger volume expansion or docker prune |
| **HighMemoryPressure** | `node_memory_utilization_ratio > 0.88` for 5m | Warning | Evict non-essential caches, schedule scale-out |
| **CertExpiringSoon** | `cert_expiry_timestamp - time() < 14*86400` | Warning | Force ACME renewal job |
| **NodeHeartbeatMissing** | `time() - node_last_seen_timestamp > 45` | Critical | Evacuate workload, mark node `unhealthy` |

---

## 5. Operations Runbooks

### Runbook 1: Build Worker Stalled / Queue Backlog
**Symptoms**: Deployments stuck in `queued` state; Redis lag on `jp.build` growing.
**Diagnosis**:
1. Check running worker containers:
   ```bash
   docker ps --filter "name=worker"
   ```
2. Inspect worker logs for hung container builds or Docker socket timeouts:
   ```bash
   docker logs --tail 100 jp-cloud-worker-1
   ```
**Resolution**:
1. Restart the worker service:
   ```bash
   docker compose restart worker
   ```
2. If concurrency limit is saturated, scale worker replicas:
   ```bash
   docker compose up -d --scale worker=3
   ```

---

### Runbook 2: Docker Disk Exhaustion (No Space Left on Device)
**Symptoms**: Container start failures; `write /var/lib/docker/...: no space left on device`.
**Diagnosis**:
```bash
df -h
docker system df
```
**Resolution**:
1. Run emergency Docker prune for dangling build caches and stopped containers:
   ```bash
   docker system prune -af --volumes=false
   docker builder prune -af
   ```
2. Remove orphaned builder cache artifacts older than 48 hours:
   ```bash
   docker builder prune --filter "until=48h" -f
   ```
3. Expand underlying host EBS/NVMe volume.

---

### Runbook 3: Redis Stream Poison Message Recovery
**Symptoms**: A corrupted event payload causes worker panics repeatedly on the same message ID.
**Diagnosis**:
```bash
docker logs jp-cloud-worker-1 | grep "panic"
```
**Resolution**:
1. Identify the pending message ID:
   ```bash
   docker exec -it jp-cloud-redis-1 redis-cli XPENDING jp.build jp-workers
   ```
2. Acknowledge and drop the poison message ID:
   ```bash
   docker exec -it jp-cloud-redis-1 redis-cli XACK jp.build jp-workers <MESSAGE_ID>
   ```

---

### Runbook 4: Emergency Workload Rollback
**Symptoms**: A newly deployed commit has caused application crash loops or high 500 errors.
**Resolution**:
1. List previous successful releases via CLI:
   ```bash
   jp deploy list --project <PROJECT_ID>
   ```
2. Trigger instant rollback to the previous deployment ID:
   ```bash
   jp deploy rollback <DEPLOY_ID> --project <PROJECT_ID>
   ```
   *Traefik switches traffic instantaneously to the prior healthy container with zero downtime.*

---

### Runbook 5: SSL Certificate Renewal Failure
**Symptoms**: Dashboard alerts `CertExpiringSoon` or ACME challenge failures.
**Diagnosis**:
1. Check Traefik logs for Let's Encrypt challenge status:
   ```bash
   docker logs jp-cloud-traefik-1 | grep "acme"
   ```
2. Verify public DNS A-record points directly to the server public IP:
   ```bash
   dig +short your-domain.com
   ```
**Resolution**:
1. Ensure port 80 is not blocked by external cloud firewalls (AWS Security Groups, Hetzner Firewall, UFW).
2. Trigger manual certificate reissue via API or CLI:
   ```bash
   jp domains renew your-domain.com --project <PROJECT_ID>
   ```
