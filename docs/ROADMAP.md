# Roadmap (not implemented)

Nothing on this page exists in the code today. JP runs the control plane on one server and can place whole projects on other servers ([MULTI_SERVER.md](MULTI_SERVER.md)); the items below are what is still missing.

Ideas under consideration, in no particular order:

* Replicas of one project spread over several servers with load balancing (the domain service already accepts several upstreams per hostname; nothing places replicas yet), automatic scheduling, and moving add-ons/databases between servers.
* Multi-architecture image builds, so a server with a different CPU architecture can run platform-built images.
* High availability of the control plane itself.
* Egress filtering and per-tenant disk quotas.
* Stronger isolation options (gVisor/Kata runtime class, rootless Docker).
* Digest-pinned base images and signed releases enabled by default.
* Tenant volume backups with application-consistent dumps for more add-on engines.
* Wildcard certificates via DNS-01.
