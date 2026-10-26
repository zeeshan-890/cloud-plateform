# JP Backup, Restore & Disaster Recovery Guide

> **Database-Aware Backups, Persistent Volume Snapshots, Off-Site Storage, and Recovery Procedures.**

---

## 1. Backup Strategy Overview

JP differentiates between **Control Plane State** and **Customer Application Data**:

1. **Platform Control Plane State:**
   - PostgreSQL (`jp` database): Stores user accounts, organizations, projects, secrets, domains, and deployment records.
   - Redis: Ephemeral cache and stream state (loss forces re-login; does not corrupt durable records).
   - Traefik Dynamic Ingress: Dynamically regenerated from database records on boot.
2. **Customer Application Data:**
   - Managed Databases (`managed_databases` schemas).
   - MinIO Object Storage (`minio_data` volume).
   - Persistent Add-on Volumes (Postgres, MySQL, Mongo, Redis).

---

## 2. Automated Database-Aware Backups

Never copy a live PostgreSQL or MySQL data directory directly. Use transactional dump utilities:

### Control Plane PostgreSQL Backup:
```bash
docker exec jp-postgres-1 pg_dump -U jp -d jp -F c -f /tmp/jp_control_plane.dump
```

### Managed Database Backup:
```bash
docker exec jp-postgres-1 pg_dump -U jp -d jp --schema=app_{db_id} -F c -f /tmp/app_{db_id}.dump
```

---

## 3. Off-Site S3 Replication

- Backups are compressed with gzip and encrypted using AES-256 before replication.
- Replicated automatically to configured S3 or MinIO off-site buckets via `storage:8017`.
- Configurable retention policies: Daily backups kept 7 days; Weekly backups kept 4 weeks; Monthly backups kept 12 months.

---

## 4. Restore & Recovery Procedures

### Restoring Control Plane State:
1. Stop running microservices: `docker compose down`.
2. Start PostgreSQL container: `docker compose up -d postgres`.
3. Drop and recreate database:
   ```bash
   docker exec -it jp-postgres-1 psql -U jp -c "DROP DATABASE jp;"
   docker exec -it jp-postgres-1 psql -U jp -c "CREATE DATABASE jp;"
   ```
4. Restore dump archive:
   ```bash
   docker exec -i jp-postgres-1 pg_restore -U jp -d jp /tmp/jp_control_plane.dump
   ```
5. Restart all services: `docker compose up -d`.
