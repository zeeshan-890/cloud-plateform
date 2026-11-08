# storage service

MinIO-backed object storage. One bucket per project, named `jp-<projectShort8>-<sha256(projectId)[:6]>`. No simulate backend: the service
refuses to start without `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY` (random, never `minioadmin` in production).

* Keys: valid UTF-8, <= 1024 bytes, no `..`/`.` segments, no control characters, no leading `/`; keys under `_backups/` are reserved for add-on/database backups.
* Upload: `POST .../storage/objects` (JSON base64, `STORAGE_MAX_UPLOAD_MB`, default 50) or streaming `PUT .../storage/objects?key=` (`STORAGE_MAX_OBJECT_MB`, default 2048).
  Content type is sniffed when the client sends none. Quota per project `STORAGE_QUOTA_MB` (default 5120, 0 = unlimited).
* Download: `GET .../storage/download?key=` streams through the API (HTML/SVG/JS are served as attachments with `nosniff`). `POST .../signed-url` returns the API
  download path and, only when `STORAGE_PUBLIC_URL` points at a publicly reachable MinIO, a presigned URL for that host.
* Listing is paginated (`limit<=1000`, `cursor`). `GET .../storage/usage`, `DELETE .../storage/bucket?confirm=true`.
* RBAC (area `storage`): list/download/usage = view; upload/delete/bucket = manage.

Internal: `PUT|GET|DELETE /internal/projects/{projectId}/objects?key=` (backups), `DELETE /internal/projects/{projectId}` (bucket + objects).
