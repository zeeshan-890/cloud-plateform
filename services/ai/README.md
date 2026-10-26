# ai service (Phase 7)

Ports: **8019**

Explains failed deployments/builds using tools (build logs, runtime logs, deploy events).

| Mode | When |
|------|------|
| `simulate` | No `OPENAI_API_KEY` — heuristic explanation from logs |
| `openai` | Hosted LLM via `OPENAI_API_KEY` + optional `AI_BASE_URL` / `AI_MODEL` |

No local LLM.

## Enable

1. Set `OPENAI_API_KEY` in `.env` (optional `AI_BASE_URL`, `AI_MODEL`).
2. Start Compose with `--profile platform` so this service runs.
3. Verify: `GET /orgs/{orgId}/ai/status` or Dashboard → **AI ops** — mode should be `openai`.

Without a key, Explain / Ask still work in `simulate` mode.

## Clients

- Dashboard: **AI ops** (Explain + Ask), **Deployments** / **Builds** → Explain failure
- CLI: `jp ai "why did this fail?" --project <id>`
