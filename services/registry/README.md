# registry (metadata API)

Phase 3 — Image metadata (port **8009**). Companion to Docker `registry:2` on :5000.

- `GET /orgs/{orgId}/projects/{projectId}/images`
- `POST /internal/images` (worker registers after build)
