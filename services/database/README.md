# database service

A managed database is a DEDICATED Postgres container per database, provisioned through the add-on service (`/internal/addons/provision`,
kind `managed_db`, `pg_stat_statements` preloaded and created). Nothing touches the platform Postgres; failures are reported, never simulated
(`DB_MODE` no longer exists). The connection URL (`addon-db-<name>:5432` on the project network) is stored via the secret service.

* Slow-query diagnostics read `pg_stat_statements` inside the container (queries are redacted + fingerprinted): `GET .../databases/{id}/slow-queries`.
* Backups/restore run `pg_dump -Fc` / `pg_restore` in the container, stored in the storage service with a SHA-256 manifest; restore verifies the
  checksum, the archive and the table count (`POST .../backups`, `GET .../backups`, `POST .../backups/{backupId}/restore`).
* RBAC (area `databases`): list/get/slow-queries/backups list = view; create/delete/backup/restore = manage; password reveal needs `secrets_reveal`.
* Legacy shared-schema databases are marked `failed/legacy` and their login roles are set to NOLOGIN at start-up.

Env: `ADDON_SVC_URL`, `ORGANIZATION_URL`, `PROJECT_URL`.
