# addon service

Add-ons are REAL containers on the project's private network (alias `addon-<name>`), started through the runtime
(`POST /internal/runtime/addon`). Nothing is simulated and add-ons are never exposed publicly (`public_url` is always null).

Flow: validate -> reserve name -> check secret conflicts (409, never overwrite) -> start container with generated credentials applied to the
server -> wait for the readiness probe (120 s) -> write credentials via the secret service -> `ready`. Any failure removes the
container, volume, secrets and record and returns the real error.

* Catalog: 241 entries, `provisionable` + `reason` + `arch_ok` per entry (see `internal/catalog/specs.go`). Only provisionable entries can be created.
  Custom images: `POST .../addons/custom` (stored per project; the catalog is immutable at runtime). `ADDON_CATALOG_DIR` drop-in JSON is read once at start-up.
* Drivers: postgres(+timescaledb), mysql, mariadb, mongodb, redis, valkey, keydb, dragonfly, memcached, rabbitmq, redpanda(kafka), nats, meilisearch,
  typesense, elasticsearch, opensearch, qdrant, couchdb, arangodb, neo4j, clickhouse.
* Backups (postgres, mysql, mariadb, mongodb; redis/valkey snapshot only): dump runs inside the container through the runtime exec endpoint, streams to the
  storage service with a SHA-256 manifest; restore re-verifies the checksum, the archive and the object count.
* Volumes are named `jp-addon-<projectShort8>-<name>`; managed databases use name `db-<name>`.
* RBAC (areas): list/get/connection(masked) = `addons:view`; create/custom/start/stop/restart/delete/backup/restore = `addons:manage`; `?reveal=true` also needs
  the grant's `secrets_reveal`. Org-wide listing is filtered to projects where the user has `addons:view`.

Env: `RUNTIME_URL`, `SECRET_URL`, `STORAGE_URL`, `ORGANIZATION_URL`, `PROJECT_URL`, `ADDON_CATALOG_DIR` (optional).
Internal API (used by the database service): `POST /internal/addons/provision`, `GET|DELETE /internal/addons/{id}`, `.../connection`, `.../sql`,
`.../backups`, `DELETE /internal/projects/{projectId}`.
