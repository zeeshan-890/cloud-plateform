// Package driver turns a catalog entry into a concrete container plan: image, env, command,
// readiness probe and connection URL, with the credentials applied to the SERVER itself.
package driver

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jp-cloud/addon/internal/catalog"
)

// Health is the readiness probe sent to the runtime.
type Health struct {
	Type           string   `json:"type"`
	Port           int      `json:"port,omitempty"`
	Path           string   `json:"path,omitempty"`
	Cmd            []string `json:"cmd,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

// Creds are the generated credentials.
type Creds struct {
	User     string
	Password string
	DB       string
}

// Options tweak a plan.
type Options struct {
	// PGStatStatements preloads pg_stat_statements (postgres family) for slow-query diagnostics.
	PGStatStatements bool
}

// Plan is everything needed to start and use an add-on.
type Plan struct {
	Driver     string
	Image      string
	Port       int
	Volume     string
	Env        map[string]string
	Command    []string
	Health     Health
	Host       string            // addon-<slug>
	URL        string            // primary connection URL
	DefaultKey string            // default secret name (DATABASE_URL, REDIS_URL, ...)
	Extras     map[string]string // extra secrets: suffix -> value (API_KEY, ...)
	Cred       Creds
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// NewPassword returns a random alphanumeric password (crypto/rand), at least 24 characters.
func NewPassword(n int) string {
	if n < 24 {
		n = 24
	}
	b := make([]byte, n)
	max := big.NewInt(int64(len(alnum)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b[i] = alnum[v.Int64()]
	}
	return string(b)
}

// Host is the network alias of an add-on on the project network.
func Host(slug string) string { return "addon-" + slug }

// BuildURL builds a connection URL with properly escaped credentials.
func BuildURL(scheme, user, password, host string, port int, path, rawQuery string) string {
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port)), RawQuery: rawQuery}
	switch {
	case password != "":
		u.User = url.UserPassword(user, password)
	case user != "":
		u.User = url.User(user)
	}
	if path != "" {
		u.Path = "/" + strings.TrimPrefix(path, "/")
	}
	return u.String()
}

// Supported reports whether a driver id is implemented.
func Supported(id string) bool { _, ok := builders[id]; return ok }

type builder func(it catalog.Item, host string, c Creds, o Options) Plan

var builders = map[string]builder{
	"postgres": buildPostgres, "mysql": buildMySQL, "mariadb": buildMySQL, "mongodb": buildMongo,
	"redis": buildRedis, "valkey": buildRedis, "keydb": buildRedis, "dragonfly": buildDragonfly,
	"memcached": buildMemcached, "rabbitmq": buildRabbit, "redpanda": buildRedpanda, "nats": buildNATS,
	"meilisearch": buildMeili, "typesense": buildTypesense, "elasticsearch": buildElastic, "opensearch": buildOpenSearch,
	"qdrant": buildQdrant, "couchdb": buildCouch, "arangodb": buildArango, "neo4j": buildNeo4j, "clickhouse": buildClickhouse,
}

// Build creates the plan for a provisionable catalog item. The password is applied to the server.
func Build(it catalog.Item, slug string, password string, o Options) (Plan, error) {
	if !it.Provisionable {
		return Plan{}, fmt.Errorf("%s is not provisionable: %s", it.ID, it.Reason)
	}
	b, ok := builders[it.Driver]
	if !ok {
		return Plan{}, fmt.Errorf("no driver %q", it.Driver)
	}
	if len(password) < 24 {
		return Plan{}, fmt.Errorf("password too short")
	}
	host := Host(slug)
	p := b(it, host, Creds{Password: password}, o)
	p.Driver, p.Host = it.Driver, host
	if p.Image == "" {
		p.Image = it.DefaultImage
	}
	if p.Port == 0 {
		p.Port = it.DefaultPort
	}
	if p.Volume == "" {
		p.Volume = it.Volume
	}
	if p.Health.TimeoutSeconds == 0 {
		p.Health.TimeoutSeconds = 120
	}
	return p, nil
}

func tcp(port int) Health { return Health{Type: "tcp", Port: port} }

func buildPostgres(it catalog.Item, host string, c Creds, o Options) Plan {
	c.User, c.DB = "app", "app"
	p := Plan{
		Env:        map[string]string{"POSTGRES_USER": c.User, "POSTGRES_PASSWORD": c.Password, "POSTGRES_DB": c.DB, "PGDATA": it.Volume + "/pgdata"},
		Health:     Health{Type: "cmd", Cmd: []string{"pg_isready", "-h", "127.0.0.1", "-U", c.User, "-d", c.DB}},
		URL:        BuildURL("postgres", c.User, c.Password, host, 5432, c.DB, "sslmode=disable"),
		DefaultKey: "DATABASE_URL", Cred: c,
	}
	if o.PGStatStatements {
		p.Command = []string{"postgres", "-c", "shared_preload_libraries=pg_stat_statements", "-c", "pg_stat_statements.track=all"}
	}
	return p
}

func buildMySQL(it catalog.Item, host string, c Creds, _ Options) Plan {
	c.User, c.DB = "app", "app"
	ping := "mysqladmin ping -h 127.0.0.1 --silent"
	envPrefix := "MYSQL"
	if it.Driver == "mariadb" {
		ping = "(mariadb-admin ping -h 127.0.0.1 --silent || mysqladmin ping -h 127.0.0.1 --silent)"
		envPrefix = "MARIADB"
	}
	return Plan{
		Env: map[string]string{
			envPrefix + "_ROOT_PASSWORD": NewPassword(32), // root is never exposed; the app user owns database "app"
			envPrefix + "_USER":          c.User, envPrefix + "_PASSWORD": c.Password, envPrefix + "_DATABASE": c.DB,
		},
		Health:     Health{Type: "cmd", Cmd: []string{"sh", "-c", ping}},
		URL:        BuildURL("mysql", c.User, c.Password, host, 3306, c.DB, ""),
		DefaultKey: "DATABASE_URL", Cred: c,
	}
}

func buildMongo(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User, c.DB = "app", "app"
	return Plan{
		Env:        map[string]string{"MONGO_INITDB_ROOT_USERNAME": c.User, "MONGO_INITDB_ROOT_PASSWORD": c.Password, "MONGO_INITDB_DATABASE": c.DB},
		Health:     Health{Type: "cmd", Cmd: []string{"sh", "-c", `mongosh --quiet -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin --eval "db.runCommand({ping:1}).ok" | grep -q 1`}},
		URL:        BuildURL("mongodb", c.User, c.Password, host, 27017, c.DB, "authSource=admin"),
		DefaultKey: "MONGODB_URI", Cred: c,
	}
}

func buildRedis(it catalog.Item, host string, c Creds, _ Options) Plan {
	srv, cli := "redis-server", "redis-cli"
	switch it.Driver {
	case "valkey":
		srv, cli = "valkey-server", "valkey-cli"
	case "keydb":
		srv, cli = "keydb-server", "keydb-cli"
	}
	return Plan{
		Env:        map[string]string{"REDIS_PASSWORD": c.Password},
		Command:    []string{"sh", "-c", `exec ` + srv + ` --requirepass "$REDIS_PASSWORD" --appendonly yes --dir /data`},
		Health:     Health{Type: "cmd", Cmd: []string{"sh", "-c", cli + ` -a "$REDIS_PASSWORD" --no-auth-warning ping | grep -q PONG`}},
		URL:        BuildURL("redis", "", c.Password, host, 6379, "0", ""),
		DefaultKey: "REDIS_URL", Cred: Creds{Password: c.Password},
	}
}

func buildDragonfly(_ catalog.Item, host string, c Creds, _ Options) Plan {
	return Plan{
		Env:        map[string]string{"DFLY_requirepass": c.Password},
		Health:     tcp(6379),
		URL:        BuildURL("redis", "", c.Password, host, 6379, "0", ""),
		DefaultKey: "REDIS_URL", Cred: Creds{Password: c.Password},
	}
}

func buildMemcached(_ catalog.Item, host string, c Creds, _ Options) Plan {
	return Plan{
		Command: []string{"memcached", "-m", "128", "-c", "512"}, Health: tcp(11211),
		URL: net.JoinHostPort(host, "11211"), DefaultKey: "MEMCACHED_SERVERS",
	}
}

func buildRabbit(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "app"
	return Plan{
		Env:        map[string]string{"RABBITMQ_DEFAULT_USER": c.User, "RABBITMQ_DEFAULT_PASS": c.Password},
		Health:     Health{Type: "cmd", Cmd: []string{"rabbitmq-diagnostics", "-q", "check_running"}},
		URL:        BuildURL("amqp", c.User, c.Password, host, 5672, "", ""),
		DefaultKey: "RABBITMQ_URL", Cred: c,
	}
}

func buildRedpanda(_ catalog.Item, host string, c Creds, _ Options) Plan {
	return Plan{
		Command: []string{"redpanda", "start", "--overprovisioned", "--smp", "1", "--memory", "768M", "--reserve-memory", "0M",
			"--node-id", "0", "--check=false", "--kafka-addr", "0.0.0.0:9092", "--advertise-kafka-addr", host + ":9092"},
		Health: tcp(9092), URL: net.JoinHostPort(host, "9092"), DefaultKey: "KAFKA_BROKERS",
	}
}

func buildNATS(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "app"
	return Plan{
		Command:    []string{"--user", c.User, "--pass", c.Password, "-js", "-sd", "/data"},
		Health:     tcp(4222),
		URL:        BuildURL("nats", c.User, c.Password, host, 4222, "", ""),
		DefaultKey: "NATS_URL", Cred: c,
	}
}

func buildMeili(_ catalog.Item, host string, c Creds, _ Options) Plan {
	return Plan{
		Env:    map[string]string{"MEILI_MASTER_KEY": c.Password, "MEILI_ENV": "production", "MEILI_DB_PATH": "/meili_data", "MEILI_NO_ANALYTICS": "true"},
		Health: Health{Type: "http", Path: "/health", Port: 7700},
		URL:    BuildURL("http", "", "", host, 7700, "", ""), DefaultKey: "MEILISEARCH_URL",
		Extras: map[string]string{"API_KEY": c.Password}, Cred: Creds{Password: c.Password},
	}
}

func buildTypesense(_ catalog.Item, host string, c Creds, _ Options) Plan {
	return Plan{
		Command: []string{"--data-dir=/data", "--api-key=" + c.Password},
		Health:  Health{Type: "http", Path: "/health", Port: 8108},
		URL:     BuildURL("http", "", "", host, 8108, "", ""), DefaultKey: "TYPESENSE_URL",
		Extras: map[string]string{"API_KEY": c.Password}, Cred: Creds{Password: c.Password},
	}
}

func buildElastic(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "elastic"
	return Plan{
		Env: map[string]string{"discovery.type": "single-node", "ELASTIC_PASSWORD": c.Password, "xpack.security.enabled": "true",
			"xpack.security.http.ssl.enabled": "false", "ES_JAVA_OPTS": "-Xms256m -Xmx256m", "bootstrap.memory_lock": "false"},
		Health: Health{Type: "http", Path: "/", Port: 9200},
		URL:    BuildURL("http", c.User, c.Password, host, 9200, "", ""), DefaultKey: "ELASTICSEARCH_URL", Cred: c,
	}
}

func buildOpenSearch(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "admin"
	// OpenSearch's demo security config enforces a strength policy (upper, lower, digit, special).
	pw := c.Password + "-Aa1#"
	c.Password = pw
	return Plan{
		Env: map[string]string{"discovery.type": "single-node", "OPENSEARCH_INITIAL_ADMIN_PASSWORD": pw,
			"OPENSEARCH_JAVA_OPTS": "-Xms256m -Xmx256m", "bootstrap.memory_lock": "false"},
		Health: Health{Type: "cmd", Cmd: []string{"sh", "-c", `curl -sk -u "admin:$OPENSEARCH_INITIAL_ADMIN_PASSWORD" https://127.0.0.1:9200/ -o /dev/null -w '%{http_code}' | grep -q 200`}},
		// the demo certificate is self-signed: clients must skip verification or pin it
		URL: BuildURL("https", c.User, pw, host, 9200, "", ""), DefaultKey: "OPENSEARCH_URL", Cred: c,
	}
}

func buildQdrant(_ catalog.Item, host string, c Creds, _ Options) Plan {
	return Plan{
		Env:    map[string]string{"QDRANT__SERVICE__API_KEY": c.Password},
		Health: Health{Type: "http", Path: "/healthz", Port: 6333},
		URL:    BuildURL("http", "", "", host, 6333, "", ""), DefaultKey: "QDRANT_URL",
		Extras: map[string]string{"API_KEY": c.Password}, Cred: Creds{Password: c.Password},
	}
}

func buildCouch(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "admin"
	return Plan{
		Env:    map[string]string{"COUCHDB_USER": c.User, "COUCHDB_PASSWORD": c.Password},
		Health: Health{Type: "http", Path: "/_up", Port: 5984},
		URL:    BuildURL("http", c.User, c.Password, host, 5984, "", ""), DefaultKey: "COUCHDB_URL", Cred: c,
	}
}

func buildArango(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "root"
	return Plan{
		Env:    map[string]string{"ARANGO_ROOT_PASSWORD": c.Password},
		Health: Health{Type: "http", Path: "/_api/version", Port: 8529},
		URL:    BuildURL("http", c.User, c.Password, host, 8529, "", ""), DefaultKey: "ARANGODB_URL", Cred: c,
	}
}

func buildNeo4j(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User = "neo4j"
	return Plan{
		Env: map[string]string{"NEO4J_AUTH": c.User + "/" + c.Password, "NEO4J_server_memory_heap_initial__size": "256m",
			"NEO4J_server_memory_heap_max__size": "512m", "NEO4J_server_memory_pagecache_size": "128m"},
		Health: Health{Type: "http", Path: "/", Port: 7474},
		URL:    BuildURL("bolt", c.User, c.Password, host, 7687, "", ""), DefaultKey: "NEO4J_URL", Cred: c,
	}
}

func buildClickhouse(_ catalog.Item, host string, c Creds, _ Options) Plan {
	c.User, c.DB = "app", "app"
	return Plan{
		Env: map[string]string{"CLICKHOUSE_USER": c.User, "CLICKHOUSE_PASSWORD": c.Password, "CLICKHOUSE_DB": c.DB,
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"},
		Health: Health{Type: "http", Path: "/ping", Port: 8123},
		URL:    BuildURL("http", c.User, c.Password, host, 8123, c.DB, ""), DefaultKey: "CLICKHOUSE_URL", Cred: c,
	}
}

// CustomPlan builds the plan for a user-defined container (no credentials are managed).
func CustomPlan(slug, image string, port int, env map[string]string, volume string) Plan {
	host := Host(slug)
	return Plan{
		Driver: "custom", Image: image, Port: port, Volume: volume, Env: env, Host: host,
		Health:     Health{Type: "tcp", Port: port, TimeoutSeconds: 120},
		URL:        BuildURL("http", "", "", host, port, "", ""),
		DefaultKey: "",
	}
}
