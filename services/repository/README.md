# repository

Phase 2 — GitHub connect + webhooks (port **8005**).

## Modes

| Mode | When |
|------|------|
| **github_app** | `GITHUB_APP_ID` + private key set — real App install URL, installation token repo list, commit statuses |
| **github** | `GITHUB_TOKEN` set (user PAT) — list `/user/repos` |
| **stub / mock** | Neither — stub install + mock repo list |

## Features

- GitHub App install start/callback + setup redirect (`GET /github/setup`)
- List installations / available repos / connect-disconnect per project
- `POST /webhooks/github` with HMAC verification
  - `push` on default branch → production deploy
  - `push` on other branches → preview (`preview/{branch}`, `[preview]` message)
  - `pull_request` opened/synchronize/reopened → preview (`preview/pr-{N}`)
  - `pull_request` closed → teardown preview (runtime stop + domain delete via deployment)
- `POST /internal/github/commit-status` — used by deployment service

## Env

| Variable | Purpose |
|----------|---------|
| `GITHUB_WEBHOOK_SECRET` | HMAC for webhooks |
| `GITHUB_APP_ID` / `GITHUB_APP_SLUG` / `GITHUB_APP_PRIVATE_KEY` | Real App |
| `GITHUB_APP_PRIVATE_KEY_PATH` | Alternate key file |
| `GITHUB_TOKEN` | Fallback user PAT for repo list |
| `DASHBOARD_URL` | Redirect after App setup |
| `PUBLIC_BASE_URL` | Stub install URL + status target |
| `DEPLOYMENT_URL` | Create deploys / teardown from webhooks |

Preview hostnames are assigned by the scheduler after start (`pr-{N}-{projectId8}.{PREVIEW_BASE_DOMAIN}`).
