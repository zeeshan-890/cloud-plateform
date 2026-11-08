# identity service

Users, sessions, MFA and platform settings. Port 8001.

* Auth: `POST /auth/{bootstrap,register,login,refresh,logout,logout-all,password}`, `GET /auth/me` (memberships and per-project areas),
  sessions (`/auth/sessions`), personal access tokens (`/auth/pats`), TOTP (`/auth/mfa/{setup,enable,disable}`).
  Login accepts username or e-mail; `must_change_password` users get a limited `pwchange` token.
* `GET /auth/bootstrap/status`, `POST /auth/bootstrap`: the first account becomes platform admin (optionally requires `BOOTSTRAP_TOKEN`).
  Afterwards `REGISTRATION_MODE=closed|invite|open`.
* Platform settings (SMTP, AI keys): `GET|PATCH /system/settings` (platform admin), stored encrypted; services read them via `/internal/settings`.
* Internal (`X-Internal-Token`): managed-user lifecycle used by the organization service (`/internal/managed-users/*`), user lookup, PAT verification, security stats.
* Env: `DATABASE_URL`, `REDIS_URL`, `JWT_SECRET`, `INTERNAL_TOKEN`, `IDENTITY_ENC_KEY` (TOTP seeds + settings), `REGISTRATION_MODE`, `BOOTSTRAP_TOKEN`, `ORGANIZATION_URL`.
* Security: passwords hashed, login/refresh rate-limited, refresh rotation and revocation list in Redis, generated passwords returned once.
