# Multiple servers

One JP installation can run projects on **more than one server**. The control plane (API, database, build, edge) stays on the
first server; additional servers run **whole projects** (the app, its add-ons and databases) and are reached over the private
network. This page says exactly what that does and does not do.

## What you get

* **Add a server with one command.** In the dashboard open *Cluster -> Servers -> Add server*, name it, tick
  *Run projects on this server* if it should host projects, and copy the command it shows. Run it on the new server.
* **Clear errors.** The command tests every connection and the dashboard explains each problem in plain language:
  a port blocked by the cloud firewall (with the exact Security List / NSG / Security Group to change), a service that is not
  listening, servers that are not on the same private network, a missing private address, a CPU architecture mismatch.
* **Place a project on a server.** *Project -> Settings -> Runs on* (platform administrators). New deployments, add-ons and
  databases of that project run there; the public domain still points at the control plane's edge, which forwards to the
  server over the private network.
* **Zero-downtime redeploys** work across servers (verified: 1,013 requests during a rolling redeploy, 0 failures).

## What it is not

* It is **not** load balancing or automatic scheduling. A project lives on exactly one server. (The domain service already
  accepts several upstreams per hostname, which is the groundwork for replicas; nothing places replicas yet.)
* It is **not** high availability. If a server is down its projects are down; the dashboard says which server and why.
* Add-ons and databases cannot be moved with a project. Moving a project away from its add-ons is refused with a clear reason.
* Build and the registry stay on the control plane. **Images are built for the control plane's CPU architecture**, so a server
  with a different architecture (an x86 server with an ARM control plane, or the reverse) cannot run them. The dashboard warns
  at join time and a deploy says so plainly. Use servers of the same architecture, or push multi-architecture images.

## How it works

```
 browser -> control plane Traefik (:80/:443) --private network--> server B:  app container (published on B's private IP)
                  |                                                         add-ons (project network on B, never published)
   runtime -------+--- node agent (B, TCP 2377, private address only) ---> B's Docker daemon
```

* The **node agent** (`services/node`, standard library only) is installed by the join command as a systemd service. It forwards
  the Docker API to the control plane, authenticated by a per-server secret that is stored sealed (AES-GCM) in the runtime
  database, and refuses anything outside an allow-list (no privileged containers, host namespaces, host mounts, ...). It also
  forwards `127.0.0.1:5000` on the server to the control plane's registry so tenant images can be pulled.
* Each project on a server gets the same hardened container settings and private project network as on the control plane.
* Apps are published on the server's **private IP at a fixed port** chosen by the runtime from 32768-60999 (a fixed port
  survives restarts; a daemon-chosen one does not). The control plane health-checks and routes to `http://<private ip>:<port>`.
* Re-running the join command rotates the agent secret.

## Requirements

* All servers on one private network (same VCN/VPC or peered). Public-only servers are not supported for project hosting.
* From the control plane's private address the new server must allow **TCP 2377** (agent) and **TCP 32768-60999** (apps).
  The join command opens these in the server's own firewall (firewalld/ufw) for the control plane only; the **cloud firewall**
  (Oracle Security List/NSG, AWS Security Group, ...) is yours to open and the command tells you if it blocks.
* The new server must reach the control plane's registry: set `REGISTRY_PRIVATE_BIND` (its private IP),
  `REGISTRY_PRIVATE_PORT=5000`, `NODE_REGISTRY_UPSTREAM=<that ip>:5000` and `CONTROL_PLANE_PRIVATE_IP` on the control plane,
  and allow TCP 5000 from the private subnet. See `.env.example`.
* Docker on the new server. The command installs it with `dnf` or `apt` if it is missing (this takes minutes on small servers).
* 1 GB RAM is the practical minimum for a server that hosts projects; 512 MB works only for tiny apps and needs swap.

## Removing a server

*Cluster -> Servers -> Stop running projects*. It is refused while projects are placed on the server or containers still run
there. Deleting a project removes its containers, volumes and network from whichever server it ran on.

## Tests

* Unit: `services/node` (proxy, allow-list, create hardening, upgrade streaming, registry forwarder), `services/runtime`
  (placement, remote routing, fixed ports and retry, probing, restart stability, reconcile and delete per server, fail-closed
  on a missing or unreachable node), `services/domain` (upstream routes), `services/gateway` (join, diagnosis, node registration,
  admin passthrough).
* End to end (`e2e/multiserver_test.go`, needs `E2E_NODE`, `E2E_NODE_IMAGE`; skipped otherwise): deploy to a real second
  server, route through the public edge, remote add-on reachable by the app, refused move, zero-downtime redeploy, project delete
  cleanup, agent port not reachable from the internet.
