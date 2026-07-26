# deployment

Phase 2 — Deployments (port **8006**).

- Create/list deployments (API + internal from-git webhook path)
- Status: `queued` → `building` → `ready` | `failed`
- Rollback creates a new deploy with `rollback_of` set to a previous successful deploy ID
- Enqueues builds via build service
