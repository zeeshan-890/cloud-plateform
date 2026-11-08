# ai service

Providers: `gemini` and `openai` (any OpenAI-compatible endpoint, https only; private/loopback destinations are blocked unless
`AI_ALLOW_PRIVATE_BASE_URL=true`). Configuration is PER ORGANISATION (`ai_config` table): provider, model, API key encrypted with AES-256-GCM
(`AI_ENC_KEY`, or derived from `SECRETS_MASTER_KEY`), never returned (only the last 4 characters). Changing/deleting it needs org admin.
If an org has no configuration the optional platform key is used (`AI_PLATFORM_PROVIDER/MODEL/KEY`, legacy `GEMINI_API_KEY`/`OPENAI_API_KEY`);
with no key at all answers come from a keyword heuristic and are labelled `mode: "heuristic"`.

* Everything sent to a third-party LLM is redacted first (tokens, bearer/JWT, URLs with credentials, env values, key=value secrets, PEM) and size capped.
* Tool retrieval (deployments, build logs, runtime logs) is limited to the requested project and to the areas the caller may view
  (`deployments`, `builds`, `logs`); requests are signed with the internal token and carry the caller's own authorization.
* Rate limit per organisation (`AI_RATE_PER_MIN`, default 20, Redis).
* RBAC: project endpoints need `logs:view` (`overview-suggestions`: `overview:view`); `ask` needs org membership (`logs:view` when a project is given).
