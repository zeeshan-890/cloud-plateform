# ai service (Phase 7)

Ports: **8019**

Explains failed deployments/builds using tools (build logs, runtime logs, deploy events).

| Mode | When |
|------|------|
| `simulate` | No `OPENAI_API_KEY` — heuristic explanation from logs |
| `openai` | Hosted LLM via `OPENAI_API_KEY` + optional `AI_BASE_URL` / `AI_MODEL` |

No local LLM.
