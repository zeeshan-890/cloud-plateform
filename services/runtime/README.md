# runtime (8010)

Owns Docker. Tenant containers run on a private network `jp_p_<proj8>` (Traefik attached), hardened (cap_drop ALL + NET_BIND_SERVICE,
no-new-privileges, pids 512, memory/cpu limits, tmpfs /tmp, json-file 10m x3, no host mounts) with only the contract labels.

* Deploy (`POST /internal/runtime/start`, from the scheduler): start new -> HTTP health gate over the container IP -> (blue/green: domain route flip) -> drain -> stop old. Failure removes the NEW containers, keeps the old, returns 422 with the stage. Idempotent per deployment.
* Previews use their own service and never touch production; fork previews get no secrets.
* Env from the secret service; jobs = one-off AutoRemove containers with exit code in `runtime_job_runs`; add-ons via `/internal/runtime/addon`.
* Supervision API (`desired`, `probe`, `restart`), reconcile loop, `stats`, `prune`, `DELETE /internal/projects/{id}`, metrics sampling.
* `RUNTIME_MODE=docker` (default); `simulate` is explicit, dev only, refused in production. Docker down => 503 `docker_unavailable`.
* Access: `runtime` area (view/manage).
