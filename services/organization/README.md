# organization service

Organizations, members, invitations, admin-created users and project grants. Port 8002.

* `POST|GET /orgs`, `GET|DELETE /orgs/{o}` (delete = owner), invitations (`/orgs/{o}/invites`, `POST /orgs/invites/accept`).
* Users and access (owner/admin): `/orgs/{o}/users` (+ `/{id}`, `/grants`, `/reset-password`, `/sessions`), `GET /orgs/{o}/access-presets`; members: `/orgs/{o}/members`.
* Internal: `GET /internal/orgs/{o}/projects/{p}/access/{u}` is the authority for every area check in every service
  (`packages/go-common/membership`); also membership, memberships of a user, project-delete cleanup.
* Env: `DATABASE_URL`, `JWT_SECRET`, `INTERNAL_TOKEN`, `IDENTITY_URL`, `NOTIFICATION_URL`, `PROJECT_URL`, `PUBLIC_DASHBOARD_URL`.
* Security: fail-closed; admins cannot grant more than they hold, the last owner cannot be removed; user changes are audited.
