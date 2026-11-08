# Add-ons and managed databases

Add-ons and managed databases are **real containers** started by the `runtime` service on the project's private network
(`jp_p_<project8>`), with generated credentials. Nothing is shared with the platform's own Postgres/Redis.

## Provisioning flow

1. The addon service builds a spec from the catalog entry (`default_image`, port, environment, data volume) and calls the runtime
   (`POST /internal/runtime/addon`). Credentials are generated per add-on and passed to the server itself
   (e.g. `POSTGRES_PASSWORD`, Redis `--requirepass`, Mongo root user, MySQL root password).
2. The container is reachable from the app as `addon-<slug>` on the project network, no published ports, no Traefik labels.
3. The connection URL (e.g. `DATABASE_URL`, `REDIS_URL`, `MONGODB_URI` - the names come from the catalog entry's `secret_keys`) is
   stored as a project secret via the secret service (production and preview by default); apps read it from their environment.
4. The add-on is `ready` only after a real readiness probe (tcp / http / exec inside the container) passes. If it does not, the
   status becomes `failed` with the message and the container is removed.
5. Stop/start/restart go through runtime; delete removes the container and, with purge, its named volume
   `jp-addon-<project8>-<slug>` and the secrets it created.

Managed databases use the same mechanism (a dedicated Postgres container per database), and project deletion removes add-on
containers and volumes as part of the cascade.

## Catalog: 22 provisionable of 241 entries

`services/addon/internal/catalog` lists 241 entries; **22 are provisionable** (a pinned image verified to ship linux/amd64 and
linux/arm64, generated credentials, a readiness probe): postgres, timescaledb, mysql, mariadb, mongodb, redis, valkey, keydb, dragonfly,
memcached, rabbitmq, kafka, nats, meilisearch, typesense, elasticsearch, opensearch, qdrant, couchdb, arangodb, neo4j, clickhouse.
The other entries are shown disabled in the dashboard with the reason (`unavailable_reason`):

| Reason | Meaning |
|---|---|
| `not_a_server` | embedded library, nothing to run |
| `public_web_app` | end-user web application; add-ons are private (no router), so deploy it as a project instead |
| `needs_companion_services` | needs several containers (database + cache + worker); single-container add-ons only |
| `needs_privileged_access` | needs docker.sock, host networking, devices or capabilities that tenant containers never get |
| `needs_public_ports` | game/UDP servers that must be internet-reachable |
| `image_unavailable` / `no_arm64_image` | image missing, private, or without arm64 (the test server is arm64) |
| `needs_licence_or_account` | requires a licence, claim token or vendor account |
| `no_credential_setup` | runs as one container but no safe credential setup is implemented; use the custom add-on endpoint |

Operators can add entries as JSON in the directory named by `ADDON_CATALOG_DIR` (mounted into the addon container); entries must carry a
complete spec or they are loaded as non-provisionable. Custom images can be started through the custom add-on flow
(subject to `RUNTIME_ALLOWED_IMAGE_PREFIXES` if set).

## Backups

Platform backups (`scripts/backup.sh`) do **not** include tenant volumes unless you pass `--with-tenant-volumes`
(crash-consistent tarballs of the named volumes). Where the addon/database services offer per-service dump/restore (pg_dump,
mongodump, ... executed inside the container through the runtime exec endpoint), the UI states which engines support it;
engines without it report "unsupported" rather than faking a backup.

## Limits to know

* One container per add-on, no replication or failover.
* Memory/CPU limits come from the add-on spec; there is no per-volume disk quota.
* Data volumes live on the host's Docker storage; losing the host loses them unless you back them up.
