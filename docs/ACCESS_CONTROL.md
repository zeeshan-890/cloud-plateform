# Users and access control

JP is self-hosted: there is no self-service signup by default and no billing. People get in because an administrator created
them (or invited them), and what they can see is decided per project and per area.

## Who can sign in

* The **first account** registered on a fresh install becomes the platform administrator (`POST /auth/bootstrap`; set
  `BOOTSTRAP_TOKEN` to require a token for this step).
* Afterwards `REGISTRATION_MODE` decides self-registration: `invite` (default: only with a valid invitation matching the e-mail),
  `closed`, `open`.
* **Admin-created users.** An org owner or admin creates users directly (`POST /orgs/{o}/users`): name, username
  (`[a-z0-9._-]{3,32}`, globally unique), optional e-mail, role `admin` or `member`, optional password. If no password is given the server
  generates a random one (16+ characters) and returns it **once**; it can never be retrieved later, only reset. `must_change_password`
  (default true) makes the first login succeed only with a limited token (scope `pwchange`) that can call nothing but change-password and `/auth/me`.
* Login accepts `login` = username **or** e-mail plus password (and a TOTP code when MFA is enabled).
* Admin-created ("managed") users cannot create organizations and are not visible outside their organization.
  Disabling or deleting a user, or resetting their password, revokes their sessions immediately.

## Roles

| Role | Capabilities |
|---|---|
| `owner` | everything in the organization, manages admins and roles, deletes the organization; the last owner cannot be removed or demoted |
| `admin` | full access to all projects, creates and manages users (not admins), creates projects; cannot grant more than they hold |
| `member` | **restricted**: sees only the projects it was granted, and in them only the granted areas |
| `viewer` | alias of a member with the view-only preset |

The platform administrator (first account, `is_admin`) additionally sees system settings, the security report and cluster (single-node) pages.

## Areas, levels, presets

Areas: `overview`, `deployments`, `builds`, `runtime`, `domains`, `secrets`, `addons`, `databases`, `storage`, `logs`, `metrics`,
`repository`, `settings`. Levels per area: `none`, `view`, `manage`. `view` covers GET/list/log reads; `manage` covers create, update, delete,
start/stop, deploy, upload, rotate.

| Preset | Grants |
|---|---|
| `viewer` | view: overview, deployments, builds, runtime, logs, metrics, domains |
| `developer` | viewer + manage: deployments, builds, runtime, storage; view: repository, secrets (metadata only), addons, databases |
| `operator` | developer + manage: domains, addons, databases, secrets, repository |
| `project_admin` | manage everything including settings, plus secret reveal |
| `custom` | an explicit area -> level map |

`secrets_reveal` is a separate flag on a grant. Plaintext secret values and add-on/database passwords are returned **only** when it is set
(or for owner/admin); everyone else sees metadata and a masked hint. Every reveal is audited.

## How it is enforced

Every project-scoped endpoint in every service calls `membership.Client.RequireArea(w, r, orgID, projectID, userID, area, level)`
(`packages/go-common/membership`). The check asks the organization service (`GET /internal/orgs/{o}/projects/{p}/access/{u}`) and **fails
closed**: an unreachable organization service, a non-member, a project of another org, or a member without a grant for that project is denied.
Org-level lists (projects, add-ons, search) are filtered to granted projects. `GET /auth/me` returns the memberships with the effective area map
per project, which the dashboard uses to build its navigation.

## Admin API (org owner/admin)

`GET|POST /orgs/{o}/users`, `PATCH|DELETE /orgs/{o}/users/{id}`, `PUT /orgs/{o}/users/{id}/grants` (replace grants),
`POST /orgs/{o}/users/{id}/reset-password`, `GET /orgs/{o}/users/{id}/sessions` and `DELETE .../sessions/{sid}`, `GET /orgs/{o}/access-presets`.
User creation, grants, resets, disables and failed logins are written to the audit log.
