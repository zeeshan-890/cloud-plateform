# JP One-Click Application Catalog & Marketplace

> **Specification for Extensible Backing Services, Templates, and 240+ Managed Add-ons.**

---

## 1. Catalog Overview

The JP application catalog ([`catalog.go`](file:///c:/Users/Zeeshan/Music/cloud-plateform/services/addon/internal/catalog/catalog.go)) empowers users to provision **240+ popular open-source applications**, databases, message brokers, AI models, media servers, and productivity tools with a single click.

Applications are **strictly on-demand** and never auto-created or bundled into the idle platform footprint:
- **Zero Background Overhead:** Fresh installations only run core platform components (consuming ~250MB RAM).
- **On-Demand Lifecycle:** An application or database container is ONLY spun up when the user clicks **Provision** or deploys via the Web Dashboard.
- **Any Custom Docker Container:** Beyond the 240+ curated marketplace items, users can deploy any arbitrary container image from Docker Hub, GitHub Packages (GHCR), Quay, or private registries using the `+ Deploy Any Custom Container` interface.

---

## 2. Supported Categories (240+ Curated Applications)

| Category | Filter Key | Count | Popular Included Applications |
|---|---|---|---|
| **Databases** | `database` | 24+ | PostgreSQL, MySQL, MariaDB, MongoDB, SQLite, CockroachDB, ScyllaDB, SurrealDB, PocketBase, CouchDB, Neo4j, ArangoDB, Cassandra, FerretDB, TiDB, YugabyteDB, TimescaleDB, QuestDB |
| **Cache & Key-Value** | `cache` | 8+ | Redis, Valkey, Memcached, Dragonfly, KeyDB, Garnet, Aerospike |
| **Queues & Streaming** | `queue` | 8+ | RabbitMQ, Apache Kafka (Redpanda), NATS.io, Mosquitto MQTT, EMQX, ActiveMQ, Pulsar |
| **Search & Discovery** | `search` | 8+ | Meilisearch, Typesense, Elasticsearch, OpenSearch, ZincSearch, SearXNG |
| **AI, LLMs & Vector DBs** | `ai` | 18+ | Ollama, Open WebUI, Flowise, Langflow, Dify, LocalAI, AnythingLLM, LiteLLM, Qdrant, Chroma, Weaviate, Milvus, Text Generation WebUI, ComfyUI, Whisper ASR, LibreTranslate, Perplexica |
| **Analytics & BI** | `analytics` | 10+ | ClickHouse, Plausible, Umami, Matomo, PostHog, Metabase, Apache Superset, Shlink |
| **Media & Streaming** | `media` | 16+ | Jellyfin, Plex, Emby, Audiobookshelf, Navidrome, Immich, PhotoPrism, LibrePhotos, Calibre-Web, Kavita, Komga, TubeArchivist, Funkwhale, Mealie, Tandoor Recipes |
| **Networking & VPN** | `networking` | 14+ | Pi-hole, AdGuard Home, WireGuard (wg-easy), Headscale, Nginx Proxy Manager, Cloudflare Tunnel (cloudflared), Netmaker, Blocky DNS, Technitium DNS, SmokePing, Speedtest Tracker, LibreSpeed, Tor SOCKS Proxy |
| **Productivity & Office** | `productivity` | 18+ | Paperless-ngx, Stirling-PDF, BookStack, Wiki.js, Outline, Collabora Online, OnlyOffice Docs, Memos, Trilium Notes, SilverBullet, Joplin Server, Etherpad, CryptPad, Focalboard, Taiga, Leantime |
| **Chat & Social** | `communication` | 12+ | Mattermost, Rocket.Chat, Zulip, Matrix Synapse, Element Web, Mastodon, Lemmy, Pixelfed, PeerTube, Revolt Chat, LiveKit Server |
| **Finance & Invoicing** | `finance` | 8+ | Firefly III, Actual Budget, Invoice Ninja, Crater Invoice, ERPNext, Akaunting, Ghostfolio, Maybe Finance |
| **Dev Tools & Automation** | `tool` | 32+ | n8n, Activepieces, Appsmith, NocoDB, Baserow, ToolJet, Budibase, Node-RED, Windmill, Formbricks, Plane, Adminer, pgAdmin 4, phpMyAdmin, Gitea, Forgejo, Portainer, Mailpit, GitLab CE, Woodpecker CI, Drone CI, Hoppscotch, Code-Server, Harbor, Nexus, Verdaccio, OpenGist, Bytebase, CloudBeaver, Mongo Express, RedisInsight, Kafka UI, AKHQ, Watchtower |
| **CMS & Publishing** | `cms` | 8+ | Ghost, Strapi, Directus, WordPress, Payload CMS, MedusaJS, Discourse |
| **Storage & File Sync** | `storage` | 10+ | MinIO, Nextcloud, File Browser, Seafile, ownCloud, Syncthing, RustDesk, Duplicati, Borgmatic |
| **Security & Auth** | `security` | 12+ | Vaultwarden, Authentik, Keycloak, HashiCorp Vault, Authelia, Infisical, Vikunja, Bitwarden Unified, Passbolt, CrowdSec, Wazuh Manager, DefectDojo, ClamAV |
| **Monitoring & Status** | `monitoring` | 12+ | Uptime Kuma, Grafana, Prometheus, Loki, Tempo, Alertmanager, VictoriaMetrics, Netdata, GlitchTip, Glances, Scrutiny, Beszel, Checkmk, Zabbix, Jaeger, Zipkin |
| **Game Servers** | `gaming` | 8+ | Minecraft (PaperMC), Valheim Dedicated Server, Factorio Headless Server, Terraria (tShock), Palworld Server, Foundry VTT |

---

## 3. Provisioning Modes

1. **`simulate` Mode (Default for Development):**
   - Synthesizes live credentials, host mappings, and dynamic connection environment variables (`{ENGINE}_URL_{NAME}`).
   - Automatically injects secrets into the associated project so applications can consume them immediately.
2. **`shared` Mode (Container Execution):**
   - Launches an isolated container with private persistent volumes and network bridges.
   - Automatically monitors healthchecks, container status, and memory/CPU limits.

---

## 4. Deploy Any Custom Container

If an application is not in the built-in marketplace catalog:
1. Navigate to **Add-ons & Marketplace** in the Web Dashboard (`/addons`).
2. Switch to the **+ Deploy Any Custom Container** tab.
3. Supply:
   - **Service Name:** Unique identifier for your instance (e.g. `my-custom-crawler`).
   - **Engine ID:** Clean label (e.g. `crawler`).
   - **Docker Image Reference:** Any public or private image (e.g. `caddy:alpine`, `ghcr.io/org/repo:tag`).
   - **Exposed Port:** Main container listen port (e.g. `8080`).
   - **Description:** Optional details for your team.
4. Click **Deploy Custom Container**. JP registers the engine in runtime memory, provisions persistent storage, and injects connection variables into your project secrets.
