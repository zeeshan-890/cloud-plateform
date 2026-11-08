package catalog

import "strings"

// Reasons an entry is not provisionable.
const (
	ReasonNoServer    = "not_a_server"
	ReasonPublicUI    = "public_web_app"
	ReasonCompanion   = "needs_companion_services"
	ReasonPrivileged  = "needs_privileged_access"
	ReasonPublicPorts = "needs_public_ports"
	ReasonImage       = "image_unavailable"
	ReasonNoArm64     = "no_arm64_image"
	ReasonLicence     = "needs_licence_or_account"
	ReasonUnsupported = "no_credential_setup"
)

// ReasonText is the human readable explanation of a reason code.
var ReasonText = map[string]string{
	ReasonNoServer:    "Embedded library, there is no server process to run.",
	ReasonPublicUI:    "End-user web application. Add-ons live on the private project network and are not exposed publicly, so its UI would be unreachable; deploy it as a project instead.",
	ReasonCompanion:   "Needs separate database/cache/worker containers; multi-container stacks are not supported by single-container add-ons.",
	ReasonPrivileged:  "Needs docker.sock, host networking, devices or extra capabilities which tenant containers never get.",
	ReasonPublicPorts: "Game/UDP server that must be reachable from the internet; add-ons are private.",
	ReasonImage:       "The default image does not exist on its registry (or is private) so it cannot be pulled.",
	ReasonNoArm64:     "The image has no linux/arm64 variant; the platform runs on arm64 hosts.",
	ReasonLicence:     "Requires a licence, claim token or vendor account before it can run.",
	ReasonUnsupported: "Runs as a single container but no safe credential/config setup is implemented yet; use the custom add-on endpoint with your own configuration.",
}

type spec struct {
	Driver, Image, Volume string
	Port                  int
	Probe                 Probe
	Backup, Restore       bool
	NoAuth                bool
	MinMem                int
}

// provisionable holds the runnable specs. Images are pinned and were verified (registry manifest
// inspection) to ship linux/amd64 and linux/arm64.
var provisionable = map[string]spec{
	"postgres":      {Driver: "postgres", Image: "postgres:16-alpine", Port: 5432, Volume: "/var/lib/postgresql/data", Probe: Probe{Type: "cmd"}, Backup: true, Restore: true},
	"timescaledb":   {Driver: "postgres", Image: "timescale/timescaledb:2.17.2-pg16", Port: 5432, Volume: "/var/lib/postgresql/data", Probe: Probe{Type: "cmd"}, Backup: true, Restore: true},
	"mysql":         {Driver: "mysql", Image: "mysql:8.4", Port: 3306, Volume: "/var/lib/mysql", Probe: Probe{Type: "cmd"}, Backup: true, Restore: true, MinMem: 512},
	"mariadb":       {Driver: "mariadb", Image: "mariadb:11.4", Port: 3306, Volume: "/var/lib/mysql", Probe: Probe{Type: "cmd"}, Backup: true, Restore: true},
	"mongodb":       {Driver: "mongodb", Image: "mongo:7.0", Port: 27017, Volume: "/data/db", Probe: Probe{Type: "cmd"}, Backup: true, Restore: true},
	"redis":         {Driver: "redis", Image: "redis:7.2-alpine", Port: 6379, Volume: "/data", Probe: Probe{Type: "cmd"}, Backup: true},
	"valkey":        {Driver: "valkey", Image: "valkey/valkey:8-alpine", Port: 6379, Volume: "/data", Probe: Probe{Type: "cmd"}, Backup: true},
	"keydb":         {Driver: "keydb", Image: "eqalpha/keydb:latest", Port: 6379, Volume: "/data", Probe: Probe{Type: "cmd"}},
	"dragonfly":     {Driver: "dragonfly", Image: "ghcr.io/dragonflydb/dragonfly:v1.25.2", Port: 6379, Volume: "/data", Probe: Probe{Type: "tcp"}},
	"memcached":     {Driver: "memcached", Image: "memcached:1.6-alpine", Port: 11211, Probe: Probe{Type: "tcp"}, NoAuth: true},
	"rabbitmq":      {Driver: "rabbitmq", Image: "rabbitmq:3.13-management-alpine", Port: 5672, Volume: "/var/lib/rabbitmq", Probe: Probe{Type: "cmd"}, MinMem: 512},
	"kafka":         {Driver: "redpanda", Image: "redpandadata/redpanda:v24.2.7", Port: 9092, Volume: "/var/lib/redpanda/data", Probe: Probe{Type: "tcp"}, NoAuth: true, MinMem: 1024},
	"nats":          {Driver: "nats", Image: "nats:2.10-alpine", Port: 4222, Volume: "/data", Probe: Probe{Type: "tcp"}},
	"meilisearch":   {Driver: "meilisearch", Image: "getmeili/meilisearch:v1.11", Port: 7700, Volume: "/meili_data", Probe: Probe{Type: "http", Path: "/health"}},
	"typesense":     {Driver: "typesense", Image: "typesense/typesense:27.1", Port: 8108, Volume: "/data", Probe: Probe{Type: "http", Path: "/health"}},
	"elasticsearch": {Driver: "elasticsearch", Image: "docker.elastic.co/elasticsearch/elasticsearch:8.15.3", Port: 9200, Volume: "/usr/share/elasticsearch/data", Probe: Probe{Type: "http", Path: "/"}, MinMem: 1024},
	"opensearch":    {Driver: "opensearch", Image: "opensearchproject/opensearch:2.17.1", Port: 9200, Volume: "/usr/share/opensearch/data", Probe: Probe{Type: "cmd"}, MinMem: 1024},
	"qdrant":        {Driver: "qdrant", Image: "qdrant/qdrant:v1.12.4", Port: 6333, Volume: "/qdrant/storage", Probe: Probe{Type: "http", Path: "/healthz"}},
	"couchdb":       {Driver: "couchdb", Image: "couchdb:3.3", Port: 5984, Volume: "/opt/couchdb/data", Probe: Probe{Type: "http", Path: "/_up"}},
	"arangodb":      {Driver: "arangodb", Image: "arangodb:3.11", Port: 8529, Volume: "/var/lib/arangodb3", Probe: Probe{Type: "http", Path: "/_api/version"}, MinMem: 512},
	"neo4j":         {Driver: "neo4j", Image: "neo4j:5.26-community", Port: 7687, Volume: "/data", Probe: Probe{Type: "http", Path: "/", Port: 7474}, MinMem: 1024},
	"clickhouse":    {Driver: "clickhouse", Image: "clickhouse/clickhouse-server:24.8-alpine", Port: 8123, Volume: "/var/lib/clickhouse", Probe: Probe{Type: "http", Path: "/ping"}, MinMem: 1024},
}

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, i := range ids {
		m[i] = true
	}
	return m
}

var (
	companion = set("wordpress", "ghost", "strapi", "directus", "nextcloud", "seafile", "plausible", "posthog", "umami", "matomo",
		"dify", "gitlab", "mattermost", "rocketchat", "zulip", "mastodon", "lemmy", "pixelfed", "peertube", "revolt", "discourse",
		"authentik", "immich", "paperless-ngx", "outline", "taiga", "erpnext", "invoiceninja", "crater", "akaunting", "medusa", "payload",
		"baserow", "appsmith", "activepieces", "tooljet", "budibase", "windmill", "plane", "harbor", "defectdojo", "bookstack", "wikijs",
		"infisical", "passbolt", "bitwarden", "librephotos", "funkwhale", "tubearchivist", "glitchtip", "superset", "milvus", "ferretdb",
		"owncloud", "focalboard", "leantime", "firefly-iii", "maybe", "formbricks", "keycloak", "ghostfolio", "tandoor", "mealie")
	privileged = set("portainer", "watchtower", "dozzle", "netdata", "pihole", "adguardhome", "wgeasy", "headscale", "cloudflared",
		"netmaker", "blocky", "technitium", "crowdsec", "wazuh", "scrutiny", "home-assistant", "zigbee2mqtt", "esphome", "rustdesk",
		"tor-proxy", "smokeping", "speedtest-tracker", "librespeed", "npm", "borgmatic", "duplicati", "syncthing", "beszel", "glances",
		"zabbix", "checkmk", "clamav", "cloudbeaver", "codeserver", "livekit", "collabora", "onlyoffice")
	gaming  = set("minecraft", "valheim", "factorio", "terraria", "palworld", "foundryvtt")
	licence = set("plex", "emby")
	// single-container data/infra services for which no credential driver exists yet
	infra = set("cockroachdb", "scylladb", "surrealdb", "pocketbase", "cassandra", "tidb", "yugabytedb", "questdb", "aerospike", "garnet",
		"pulsar", "emqx", "activemq", "mosquitto", "zincsearch", "vespa", "marqo", "weaviate", "chroma", "victoriametrics", "prometheus",
		"loki", "tempo", "alertmanager", "jaeger", "zipkin", "vault", "localai", "ollama", "litellm", "fastchat", "anythingllm",
		"text-generation-webui", "comfyui", "whisper-asr", "libretranslate", "mailpit", "minio", "typesense-dashboard", "redisinsight",
		"adminer", "pgadmin", "phpmyadmin", "mongo-express", "kafka-ui", "akhq")
)

// finalize derives the provisioning fields of a built-in entry.
func finalize(it Item) Item {
	ar := archData[it.ID]
	if sp, ok := provisionable[it.ID]; ok {
		it.Provisionable, it.Driver, it.DefaultImage, it.DefaultPort, it.Volume = true, sp.Driver, sp.Image, sp.Port, sp.Volume
		p := sp.Probe
		it.Probe = &p
		it.Backup, it.Restore, it.NoAuth, it.MinMemoryMB = sp.Backup, sp.Restore, sp.NoAuth, sp.MinMem
		it.ArchOK, it.Arch = true, "amd64,arm64" // verified for the pinned image above
		it.Scheme = schemeFor(sp.Driver, it.Scheme)
		return it
	}
	it.Driver = "generic"
	switch {
	case it.DefaultImage == "":
		it.Reason = ReasonNoServer
	case strings.Contains(ar.Err, "404") || strings.Contains(ar.Err, "401"):
		it.Reason = ReasonImage
	case ar.Checked && !ar.ARM64:
		it.Reason = ReasonNoArm64
	case gaming[it.ID]:
		it.Reason = ReasonPublicPorts
	case licence[it.ID]:
		it.Reason = ReasonLicence
	case privileged[it.ID]:
		it.Reason = ReasonPrivileged
	case infra[it.ID]:
		it.Reason = ReasonUnsupported
	case companion[it.ID]:
		it.Reason = ReasonCompanion
	default:
		it.Reason = ReasonPublicUI
	}
	if ar.Checked {
		it.ArchOK = ar.AMD64 && ar.ARM64
		var a []string
		if ar.AMD64 {
			a = append(a, "amd64")
		}
		if ar.ARM64 {
			a = append(a, "arm64")
		}
		it.Arch = strings.Join(a, ",")
	}
	return it
}

func schemeFor(driver, def string) string {
	switch driver {
	case "postgres":
		return "postgres"
	case "mysql", "mariadb":
		return "mysql"
	case "redis", "valkey", "keydb", "dragonfly":
		return "redis"
	}
	return def
}
