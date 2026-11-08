# project service

Projects, `jp.yaml` desired state, drift detection and project deletion. Port 8003.

* `POST|GET /orgs/{o}/projects`, `GET|PATCH|DELETE /orgs/{o}/projects/{p}`, `PUT|POST|GET .../config`, `GET|POST .../config/drift`.
* On create it registers the system hostname with the domain service; on delete it orchestrates cleanup in runtime, domain, secret, addon,
  database, storage, deployment/build and repository and reports partial failures instead of ignoring them.
* Internal: `GET /internal/orgs/{o}/projects[/{p}]` (existence/ownership checks for other services).
* Areas: `overview`/`settings` view; PATCH/config apply/delete need `settings:manage`; creating projects needs org admin.
* Env: `DATABASE_URL`, `JWT_SECRET`, `INTERNAL_TOKEN`, `ORGANIZATION_URL` and the service URLs it calls during cleanup.
