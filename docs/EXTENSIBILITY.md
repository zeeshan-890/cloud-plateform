# JP Cloud Platform — Extensibility & Plugin Architecture

This guide provides a comprehensive developer cookbook for extending the JP Open-Source Self-Hosted Cloud Platform. It explains how to easily add new one-click marketplace apps, extend existing services, add new CLI commands, build new dashboard views, and introduce event-driven background workers.

---

## Table of Contents
1. [One-Click Apps (Marketplace Add-ons)](#1-one-click-apps-marketplace-add-ons)
   - [Method A: Zero-Recompile Dynamic JSON Drop-in](#method-a-zero-recompile-dynamic-json-drop-in-recommended-for-ops)
   - [Method B: Native Go Engine Registration](#method-b-native-go-engine-registration)
   - [Automatic Secret & URL Generation](#automatic-secret--url-generation)
   - [Custom Shared Provisioning Drivers](#custom-shared-provisioning-drivers)
2. [Adding a New Microservice](#2-adding-a-new-microservice)
   - [Service Directory Structure](#service-directory-structure)
   - [Go Workspace Registration](#go-workspace-registration)
   - [Edge Routing & Gateway Integration](#edge-routing--gateway-integration)
   - [Redis Streams Event Bus Pub/Sub](#redis-streams-event-bus-pubsub)
3. [Adding a New CLI Command](#3-adding-a-new-cli-command)
4. [Adding a New Dashboard Console Page](#4-adding-a-new-dashboard-console-page)
5. [Database Migrations & Schema Extensions](#5-database-migrations--schema-extensions)

---

## 1. One-Click Apps (Marketplace Add-ons)

The JP Add-on Engine (`services/addon`) features a pluggable, decoupled catalog and provisioner architecture. Any new one-click app automatically inherits:
- **Web Console UI**: Appears immediately in the grid at `/addons` with its logo, category, description, and input parameters.
- **CLI Support**: Instantly queryable via `jp addon catalog --project <pid>` and provisionable via `jp addon create <name> --engine <id>`.
- **Secret Encryption**: Connection strings and credentials are automatically encrypted via the Secret Service and injected into application deployment environments.
- **Event Bus Notifications**: Emits `addon.created` and `addon.deleted` events on `jp.addon` stream.

### Method A: Zero-Recompile Dynamic JSON Drop-in (Recommended for Ops)

You can add any new one-click app without recompiling the Go binary or rebuilding Docker images. The Addon service automatically scans the directory pointed to by `ADDON_CATALOG_DIR` (e.g. `/etc/jp/addons` or `./addons-catalog`) on startup.

#### Step 1: Create a JSON definition file
Save this file as `/etc/jp/addons/typesense.json`:

```json
{
  "id": "typesense",
  "name": "Typesense",
  "category": "search",
  "description": "Blazing-fast, typo-tolerant open-source search engine with instant search API.",
  "default_port": 8108,
  "scheme": "http",
  "env_prefix": "TYPESENSE_URL_",
  "default_image": "typesense/typesense:0.25.2",
  "secret_keys": [
    "TYPESENSE_URL_{NAME}",
    "TYPESENSE_API_KEY_{NAME}"
  ],
  "modes_supported": ["simulate", "shared"]
}
```

#### Step 2: Restart the Addon Service or Reload
The addon service registers `typesense`.
- In `simulate` mode, it automatically constructs:
  `http://<safe>:<pass>@localhost:8108/jp_<project>_<name>`
- In `shared` mode, set `TYPESENSE_PROVISION_URL=http://admin:masterkey@typesense-cluster:8108` and it automatically connects and credentials.

---

### Method B: Native Go Engine Registration

To bake a new engine directly into the binary:

#### Step 1: Add Item to `services/addon/internal/catalog/catalog.go`
In `RegisterDefaults()`:

```go
{
    ID: "meilisearch", 
    Name: "Meilisearch", 
    Category: "search",
    Description: "Lightning-fast, typo-tolerant search engine with instant search API.",
    DefaultPort: 7700,
    Scheme: "http",
    EnvPrefix: "MEILISEARCH_URL_",
    DefaultImage: "getmeili/meilisearch:v1.7",
    SecretKeys: []string{"MEILISEARCH_URL_{NAME}", "MEILISEARCH_API_KEY_{NAME}"},
    ModesSupported: []string{"simulate", "shared"},
},
```

#### Step 2: (Optional) Custom Simulation in `services/addon/internal/provisioner/provisioner.go`
If your engine requires specialized credential formats (like S3 keys or JWTs), add a case to `createSimulate()`:

```go
case "meilisearch":
    conn := fmt.Sprintf("http://:%s@localhost:7700", pass)
    hint := "http://:***@localhost:7700 (simulate)"
    meta["api_key"] = pass
    return &Result{
        Mode: "simulate", 
        Endpoint: "localhost:7700", 
        ConnectionURL: conn, 
        ConnectionHint: hint, 
        Metadata: meta,
    }, nil
```

If omitted, the **dynamic fallback** automatically creates a standard `<scheme>://<user>:<pass>@localhost:<port>/<resource>` connection string.

---

### Automatic Secret & URL Generation

When an addon is provisioned (e.g. engine `valkey`, instance name `session-cache`), the platform automatically:
1. Sanitizes the instance name (`session-cache` -> `SESSION_CACHE`).
2. Looks up the registered `EnvPrefix` (`VALKEY_URL_`).
3. Mints the secret reference:
   `secret://<org_id>/<project_id>/<environment>/VALKEY_URL_SESSION_CACHE`
4. Encrypts the raw connection string with AES-256-GCM via the Secret Service (`services/secret`).
5. Injects `VALKEY_URL_SESSION_CACHE` directly into application containers during deployment.

---

### Custom Shared Provisioning Drivers

In `shared` mode, if you want the platform to provision dedicated users or databases on a running cluster:
1. In `services/addon/internal/provisioner/provisioner.go`, add a provision driver method:
   ```go
   func (m *Manager) sharedCustomApp(ctx context.Context, resource, pass string) (*Result, error) {
       // Perform administrative API call or SQL statement
       return &Result{
           Mode: "shared",
           Endpoint: "app.internal:8080",
           ConnectionURL: fmt.Sprintf("http://user:%s@app.internal:8080/%s", pass, resource),
           Metadata: map[string]any{"resource": resource},
       }, nil
   }
   ```
2. Call it from `createShared(ctx, engine, ...)`.

---

## 2. Adding a New Microservice

Every microservice in the platform follows a modular, standardized architecture using Go standard library HTTP routes, pgxpool, and Redis Streams.

### Service Directory Structure

```
services/<name>/
├── cmd/
│   └── main.go              # Entrypoint, config load, graceful shutdown
├── internal/
│   ├── handlers/
│   │   ├── handlers.go      # HTTP ServeMux routes and middleware
│   │   └── handlers_test.go # Route unit tests
│   └── store/
│       ├── store.go         # SQL queries and pgxpool interactions
│       └── models.go        # Go domain structs
├── migrations/
│   └── <name>_001_init.sql  # Versioned forward-only migration
├── go.mod                   # Independent module definition
└── Dockerfile               # Multi-stage lightweight distroless/scratch container
```

### Go Workspace Registration

1. Create the module:
   ```powershell
   cd services/<name>
   go mod init github.com/jp-cloud/<name>
   ```
2. Add dependencies to `go.mod`:
   ```go
   require (
       github.com/jp-cloud/go-common v0.0.0
       github.com/jp-cloud/events v0.0.0
   )
   ```
3. Register the new service in root `go.work`:
   ```go
   use (
       ./services/<name>
   )
   ```

### Edge Routing & Gateway Integration

To expose the new service via the API Gateway or Traefik:
1. In `docker-compose.yml`, assign a port and Traefik router labels:
   ```yaml
   <name>:
     build:
       context: .
       dockerfile: services/<name>/Dockerfile
     environment:
       - HTTP_PORT=8023
       - DATABASE_URL=${DATABASE_URL}
       - REDIS_URL=${REDIS_URL}
       - JWT_SECRET=${JWT_SECRET}
     labels:
       - "traefik.enable=true"
       - "traefik.http.routers.<name>.rule=PathPrefix(`/api/v1/<name>`)"
       - "traefik.http.routers.<name>.entrypoints=websecure"
       - "traefik.http.services.<name>.loadbalancer.server.port=8023"
   ```
2. In `services/gateway/internal/proxy/proxy.go`, register reverse proxy routing:
   ```go
   mux.Handle("/api/v1/<name>/", http.StripPrefix("/api/v1/<name>", newReverseProxy("http://<name>:8023")))
   ```

### Redis Streams Event Bus Pub/Sub

#### Publishing an Event:
```go
import "github.com/jp-cloud/events"

env := events.New("jp.custom", "custom.action_completed", userID, orgID, map[string]any{
    "resource_id": id,
    "status": "ready",
})
_, err := events.PublishJSON(ctx, rdb, "jp.custom", env)
```

#### Consuming Events in Background Workers:
```go
err := events.EnsureGroup(ctx, rdb, "jp.custom", "jp-custom-worker")
for {
    msgs, err := events.ReadGroup(ctx, rdb, "jp.custom", "jp-custom-worker", "consumer-1", 1, 5*time.Second)
    for _, msg := range msgs {
        env, err := events.ParseEnvelope(msg)
        // Process event logic...
        _ = events.Ack(ctx, rdb, "jp.custom", "jp-custom-worker", msg.ID)
    }
}
```

---

## 3. Adding a New CLI Command

The CLI uses Cobra with tabwriter formatting and shared configuration storage (`~/.jp/config.json`).

### Step 1: Create `apps/cli/cmd/<feature>.go`
```go
package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"github.com/spf13/cobra"
)

var myFeatureCmd = &cobra.Command{
	Use:   "feature",
	Short: "Manage custom platform feature",
}

var myFeatureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List items",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		items, err := client.ListMyFeature(cfg.CurrentOrgID)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATUS")
		for _, it := range items {
			fmt.Fprintf(w, "%s\t%s\t%s\n", it.ID, it.Name, it.Status)
		}
		return w.Flush()
	},
}

func init() {
	myFeatureCmd.AddCommand(myFeatureListCmd)
	rootCmd.AddCommand(myFeatureCmd)
}
```

### Step 2: Add API Method in `apps/cli/pkg/client/client.go`
```go
func (c *Client) ListMyFeature(orgID string) ([]MyItem, error) {
    var res struct {
        Items []MyItem `json:"items"`
    }
    err := c.get(fmt.Sprintf("/orgs/%s/feature", orgID), &res)
    return res.Items, err
}
```

---

## 4. Adding a New Dashboard Console Page

The frontend console is built with Next.js (App Router), React 19, and Tailwind CSS.

### Step 1: Add API client method in `apps/dashboard/src/lib/api.ts`
```typescript
async listFeatureItems(orgId: string): Promise<FeatureItem[]> {
  const data = await request<{ items: FeatureItem[] }>(`/orgs/${orgId}/feature`);
  return data.items || [];
}
```

### Step 2: Create Page in `apps/dashboard/src/app/(console)/feature/page.tsx`
```tsx
"use client";

import { useState, useEffect } from "react";
import { useAuth } from "@/components/auth-context";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/page-header";

export default function FeaturePage() {
  const { currentOrg } = useAuth();
  const [items, setItems] = useState([]);

  useEffect(() => {
    if (!currentOrg) return;
    api.listFeatureItems(currentOrg.id).then(setItems);
  }, [currentOrg]);

  return (
    <div className="animate-fade-up">
      <PageHeader title="Feature Manager" description="Manage custom extended platform resources." />
      <div className="mt-6 rounded-md border border-[var(--border)] bg-[var(--surface)] p-4">
        {/* Render items grid or table */}
      </div>
    </div>
  );
}
```

### Step 3: Register in Navigation (`apps/dashboard/src/components/app-shell.tsx`)
In `CONSOLE_NAV_ITEMS`:
```typescript
{ label: "My Feature", href: "/feature" },
```

---

## 5. Database Migrations & Schema Extensions

The platform applies forward-only SQL migrations automatically on startup via `postgres.Migrate()`.

### Rules for Schema Extensions:
1. **Never drop or rename existing columns in place**: Use additive changes (e.g. `ADD COLUMN IF NOT EXISTS`).
2. **Use UUID primary keys**: `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
3. **Include tenant scoping**: Every table must have `org_id UUID NOT NULL` and indexed composite keys `(org_id, ...)` for strict multi-tenant isolation.
4. **Use JSONB for extensible attributes**: Enables adding arbitrary runtime options without altering schemas.
