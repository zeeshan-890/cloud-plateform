-- Multi-server routing: optional http upstreams (JSON array of "http://ip:port") per project route.
-- Empty array = route to the local docker service (project_routes.service).
ALTER TABLE project_routes ADD COLUMN IF NOT EXISTS upstreams TEXT NOT NULL DEFAULT '[]';
