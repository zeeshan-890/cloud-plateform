package provisioner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jp-cloud/addon/internal/catalog"
	"github.com/redis/go-redis/v9"
)

var identSafe = regexp.MustCompile(`[^a-z0-9_]`)

// Result is the outcome of provisioning an add-on instance.
type Result struct {
	Mode           string
	Endpoint       string
	ConnectionURL  string
	ConnectionHint string
	Metadata       map[string]any
}

// Manager provisions add-ons in simulate or shared mode.
type Manager struct {
	Mode string
	PG   *pgxpool.Pool
	HTTP *http.Client
}

func NewFromEnv(pg *pgxpool.Pool) *Manager {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("ADDON_MODE")))
	if mode == "" {
		mode = "simulate"
	}
	if mode != "shared" {
		mode = "simulate"
	}
	return &Manager{
		Mode: mode,
		PG:   pg,
		HTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

func sanitize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = identSafe.ReplaceAllString(n, "_")
	if len(n) > 40 {
		n = n[:40]
	}
	if n == "" || !((n[0] >= 'a' && n[0] <= 'z') || n[0] == '_') {
		n = "a_" + n
	}
	return n
}

func randomPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func shortProject(projectID string) string {
	s := strings.ReplaceAll(projectID, "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func (m *Manager) Create(ctx context.Context, orgID, projectID, engine, name string) (*Result, error) {
	safe := sanitize(name)
	short := shortProject(projectID)
	pass := randomPassword()
	resource := fmt.Sprintf("jp_%s_%s", short, safe)

	if m.Mode == "shared" {
		if res, err := m.createShared(ctx, engine, resource, pass, safe, short); err == nil {
			return res, nil
		}
		// fall through to simulate on shared failure
	}
	return m.createSimulate(engine, resource, pass, safe, short)
}

func (m *Manager) Delete(ctx context.Context, engine string, metadata map[string]any) error {
	if m.Mode != "shared" {
		return nil
	}
	switch engine {
	case "postgres":
		schema, _ := metadata["schema"].(string)
		role, _ := metadata["role"].(string)
		if m.PG != nil && schema != "" {
			_, _ = m.PG.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
			if role != "" {
				_, _ = m.PG.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
			}
		}
	case "mysql":
		dbName, _ := metadata["database"].(string)
		user, _ := metadata["user"].(string)
		dsn := os.Getenv("MYSQL_PROVISION_URL")
		if dsn != "" && dbName != "" {
			if db, err := sql.Open("mysql", dsn); err == nil {
				defer db.Close()
				_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+dbName+"`")
				if user != "" {
					_, _ = db.ExecContext(ctx, "DROP USER IF EXISTS '"+user+"'@'%'")
				}
			}
		}
	case "sqlite":
		if path, _ := metadata["path"].(string); path != "" {
			_ = os.Remove(path)
		}
	case "rabbitmq":
		vhost, _ := metadata["vhost"].(string)
		user, _ := metadata["user"].(string)
		base := strings.TrimRight(os.Getenv("RABBITMQ_MANAGEMENT_URL"), "/")
		userPass := os.Getenv("RABBITMQ_ADMIN_USER")
		if userPass == "" {
			userPass = "jp"
		}
		pass := os.Getenv("RABBITMQ_ADMIN_PASSWORD")
		if pass == "" {
			pass = "jp_dev_password"
		}
		if base != "" && vhost != "" {
			req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/vhosts/"+url.PathEscape(vhost), nil)
			if req != nil {
				req.SetBasicAuth(userPass, pass)
				resp, err := m.HTTP.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
			}
			if user != "" {
				req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/users/"+url.PathEscape(user), nil)
				if req != nil {
					req.SetBasicAuth(userPass, pass)
					resp, err := m.HTTP.Do(req)
					if err == nil {
						_ = resp.Body.Close()
					}
				}
			}
		}
	}
	return nil
}

// InternalServiceHost returns the internal Docker network hostname for inter-container communication.
func InternalServiceHost(engine, safe string) string {
	switch engine {
	case "postgres":
		return "postgres"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "mongodb":
		return "mongodb"
	case "redis", "valkey":
		return "redis"
	case "rabbitmq":
		return "rabbitmq"
	case "kafka":
		return "redpanda"
	case "minio":
		return "minio"
	case "clickhouse":
		return "clickhouse"
	case "meilisearch":
		return "meilisearch"
	case "elasticsearch":
		return "elasticsearch"
	default:
		return "addon-" + safe
	}
}

// PublicServiceHost returns the public Server IP or hostname for outside access.
func PublicServiceHost() string {
	h := strings.TrimSpace(os.Getenv("SERVER_IP"))
	if h == "" {
		h = strings.TrimSpace(os.Getenv("PUBLIC_IP"))
	}
	if h == "" {
		h = strings.TrimSpace(os.Getenv("HOST_IP"))
	}
	if h == "" {
		h = "localhost"
	}
	return h
}

func (m *Manager) createSimulate(engine, resource, pass, safe, short string) (*Result, error) {
	meta := map[string]any{"resource": resource, "simulate": true}
	intHost := InternalServiceHost(engine, safe)
	pubHost := PublicServiceHost()
	meta["internal_host"] = intHost
	meta["server_ip"] = pubHost

	switch engine {
	case "postgres":
		schema, role := resource, "jp_u_"+short+"_"+safe
		if len(role) > 63 {
			role = role[:63]
		}
		conn := fmt.Sprintf("postgres://%s:%s@localhost:5432/jp?sslmode=disable&search_path=%s", role, pass, schema)
		intConn := fmt.Sprintf("postgres://%s:%s@%s:5432/jp?sslmode=disable&search_path=%s", role, pass, intHost, schema)
		pubConn := fmt.Sprintf("postgres://%s:%s@%s:5432/jp?sslmode=disable&search_path=%s", role, pass, pubHost, schema)
		hint := fmt.Sprintf("postgres://%s:***@localhost:5432/jp?search_path=%s (simulate)", role, schema)
		meta["schema"] = schema
		meta["role"] = role
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 5432
		return &Result{Mode: "simulate", Endpoint: "localhost:5432", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "mysql", "mariadb":
		user := "jp_" + short + "_" + safe
		if len(user) > 32 {
			user = user[:32]
		}
		conn := fmt.Sprintf("mysql://%s:%s@localhost:3306/%s", user, pass, resource)
		intConn := fmt.Sprintf("mysql://%s:%s@%s:3306/%s", user, pass, intHost, resource)
		pubConn := fmt.Sprintf("mysql://%s:%s@%s:3306/%s", user, pass, pubHost, resource)
		hint := fmt.Sprintf("mysql://%s:***@localhost:3306/%s (simulate)", user, resource)
		meta["database"] = resource
		meta["user"] = user
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 3306
		return &Result{Mode: "simulate", Endpoint: "localhost:3306", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "mongodb":
		user := "jp_" + short + "_" + safe
		conn := fmt.Sprintf("mongodb://%s:%s@localhost:27017/%s?authSource=%s", user, pass, resource, resource)
		intConn := fmt.Sprintf("mongodb://%s:%s@%s:27017/%s?authSource=%s", user, pass, intHost, resource, resource)
		pubConn := fmt.Sprintf("mongodb://%s:%s@%s:27017/%s?authSource=%s", user, pass, pubHost, resource, resource)
		hint := fmt.Sprintf("mongodb://%s:***@localhost:27017/%s (simulate)", user, resource)
		meta["database"] = resource
		meta["user"] = user
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 27017
		return &Result{Mode: "simulate", Endpoint: "localhost:27017", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "redis":
		dbIdx := hashDB(short + safe)
		conn := fmt.Sprintf("redis://:%s@localhost:6380/%d", pass, dbIdx)
		intConn := fmt.Sprintf("redis://:%s@%s:6379/%d", pass, intHost, dbIdx)
		pubConn := fmt.Sprintf("redis://:%s@%s:6379/%d", pass, pubHost, dbIdx)
		hint := fmt.Sprintf("redis://:***@localhost:6380/%d (simulate)", dbIdx)
		meta["db"] = dbIdx
		meta["key_prefix"] = resource + ":"
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 6379
		return &Result{Mode: "simulate", Endpoint: "localhost:6380", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "valkey":
		dbIdx := hashDB(short + safe)
		conn := fmt.Sprintf("redis://:%s@localhost:6379/%d", pass, dbIdx)
		intConn := fmt.Sprintf("redis://:%s@%s:6379/%d", pass, intHost, dbIdx)
		pubConn := fmt.Sprintf("redis://:%s@%s:6379/%d", pass, pubHost, dbIdx)
		hint := fmt.Sprintf("redis://:***@localhost:6379/%d (simulate)", dbIdx)
		meta["db"] = dbIdx
		meta["key_prefix"] = resource + ":"
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 6379
		return &Result{Mode: "simulate", Endpoint: "localhost:6379", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "rabbitmq":
		user := "jp_" + short + "_" + safe
		vhost := resource
		conn := fmt.Sprintf("amqp://%s:%s@localhost:5672/%s", user, pass, url.PathEscape(vhost))
		intConn := fmt.Sprintf("amqp://%s:%s@%s:5672/%s", user, pass, intHost, url.PathEscape(vhost))
		pubConn := fmt.Sprintf("amqp://%s:%s@%s:5672/%s", user, pass, pubHost, url.PathEscape(vhost))
		hint := fmt.Sprintf("amqp://%s:***@localhost:5672/%s (simulate)", user, vhost)
		meta["vhost"] = vhost
		meta["user"] = user
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 5672
		return &Result{Mode: "simulate", Endpoint: "localhost:5672", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "kafka":
		prefix := resource + "."
		brokers := "localhost:9092"
		conn := brokers
		intConn := fmt.Sprintf("%s:9092", intHost)
		pubConn := fmt.Sprintf("%s:9092", pubHost)
		hint := brokers + " topic_prefix=" + prefix + " (simulate)"
		meta["topic_prefix"] = prefix
		meta["brokers"] = brokers
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 9092
		return &Result{Mode: "simulate", Endpoint: brokers, ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "sqlite":
		path := filepath.ToSlash(filepath.Join("/tmp/jp-addons", resource+".db"))
		meta["path"] = path
		meta["internal_url"] = path
		meta["public_url"] = path
		return &Result{Mode: "simulate", Endpoint: path, ConnectionURL: path, ConnectionHint: path + " (simulate)", Metadata: meta}, nil
	case "meilisearch":
		conn := fmt.Sprintf("http://:%s@localhost:7700", pass)
		intConn := fmt.Sprintf("http://:%s@%s:7700", pass, intHost)
		pubConn := fmt.Sprintf("http://:%s@%s:7700", pass, pubHost)
		hint := "http://:***@localhost:7700 (simulate)"
		meta["api_key"] = pass
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 7700
		return &Result{Mode: "simulate", Endpoint: "localhost:7700", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "elasticsearch":
		conn := fmt.Sprintf("http://elastic:%s@localhost:9200", pass)
		intConn := fmt.Sprintf("http://elastic:%s@%s:9200", pass, intHost)
		pubConn := fmt.Sprintf("http://elastic:%s@%s:9200", pass, pubHost)
		hint := "http://elastic:***@localhost:9200 (simulate)"
		meta["user"] = "elastic"
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 9200
		return &Result{Mode: "simulate", Endpoint: "localhost:9200", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "clickhouse":
		conn := fmt.Sprintf("http://default:%s@localhost:8123/%s", pass, resource)
		intConn := fmt.Sprintf("http://default:%s@%s:8123/%s", pass, intHost, resource)
		pubConn := fmt.Sprintf("http://default:%s@%s:8123/%s", pass, pubHost, resource)
		hint := fmt.Sprintf("http://default:***@localhost:8123/%s (simulate)", resource)
		meta["database"] = resource
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 8123
		return &Result{Mode: "simulate", Endpoint: "localhost:8123", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "minio":
		accessKey := "jp_" + short + "_" + safe
		if len(accessKey) > 20 {
			accessKey = accessKey[:20]
		}
		conn := "http://localhost:9000"
		intConn := fmt.Sprintf("http://%s:9000", intHost)
		pubConn := fmt.Sprintf("http://%s:9000", pubHost)
		hint := fmt.Sprintf("s3://%s:***@localhost:9000 (simulate)", accessKey)
		meta["access_key"] = accessKey
		meta["secret_key"] = pass
		meta["endpoint"] = conn
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 9000
		return &Result{Mode: "simulate", Endpoint: "localhost:9000", ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
	case "adminer":
		conn := "http://localhost:8080"
		intConn := fmt.Sprintf("http://%s:8080", intHost)
		pubConn := fmt.Sprintf("http://%s:8080", pubHost)
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 8080
		return &Result{Mode: "simulate", Endpoint: "localhost:8080", ConnectionURL: conn, ConnectionHint: "http://localhost:8080 (simulate)", Metadata: meta}, nil
	case "n8n":
		conn := "http://localhost:5678"
		intConn := fmt.Sprintf("http://%s:5678", intHost)
		pubConn := fmt.Sprintf("http://%s:5678", pubHost)
		meta["encryption_key"] = pass
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 5678
		return &Result{Mode: "simulate", Endpoint: "localhost:5678", ConnectionURL: conn, ConnectionHint: "http://localhost:5678 (simulate)", Metadata: meta}, nil
	case "vaultwarden":
		conn := "http://localhost:80"
		intConn := fmt.Sprintf("http://%s:80", intHost)
		pubConn := fmt.Sprintf("http://%s:80", pubHost)
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 80
		return &Result{Mode: "simulate", Endpoint: "localhost:80", ConnectionURL: conn, ConnectionHint: "http://localhost:80 (simulate)", Metadata: meta}, nil
	case "pocketbase":
		conn := "http://localhost:8090"
		intConn := fmt.Sprintf("http://%s:8090", intHost)
		pubConn := fmt.Sprintf("http://%s:8090", pubHost)
		meta["internal_url"] = intConn
		meta["public_url"] = pubConn
		meta["port"] = 8090
		return &Result{Mode: "simulate", Endpoint: "localhost:8090", ConnectionURL: conn, ConnectionHint: "http://localhost:8090 (simulate)", Metadata: meta}, nil
	default:
		// Dynamic fallback for any registered custom marketplace app
		if it, ok := catalog.Get(engine); ok {
			port := it.DefaultPort
			if port <= 0 {
				port = 8080
			}
			scheme := it.Scheme
			if scheme == "" {
				scheme = "http"
			}
			endpoint := fmt.Sprintf("localhost:%d", port)
			conn := fmt.Sprintf("%s://%s:%s@%s/%s", scheme, safe, pass, endpoint, resource)
			intConn := fmt.Sprintf("%s://%s:%d", scheme, intHost, port)
			pubConn := fmt.Sprintf("%s://%s:%d", scheme, pubHost, port)
			hint := fmt.Sprintf("%s://%s:***@%s/%s (simulate)", scheme, safe, endpoint, resource)
			meta["internal_url"] = intConn
			meta["public_url"] = pubConn
			meta["port"] = port
			return &Result{Mode: "simulate", Endpoint: endpoint, ConnectionURL: conn, ConnectionHint: hint, Metadata: meta}, nil
		}
		return nil, fmt.Errorf("unsupported engine %s", engine)
	}
}

func (m *Manager) createShared(ctx context.Context, engine, resource, pass, safe, short string) (*Result, error) {
	switch engine {
	case "postgres":
		return m.sharedPostgres(ctx, resource, pass, safe, short)
	case "mysql":
		return m.sharedMySQL(ctx, resource, pass, safe, short)
	case "mariadb":
		return m.sharedMySQL(ctx, resource, pass, safe, short)
	case "mongodb":
		return m.sharedMongo(resource, pass, safe, short)
	case "redis":
		return m.sharedRedis(ctx, resource, pass, safe, short)
	case "valkey":
		return m.sharedRedis(ctx, resource, pass, safe, short)
	case "rabbitmq":
		return m.sharedRabbit(ctx, resource, pass, safe, short)
	case "kafka":
		return m.sharedKafka(resource)
	case "sqlite":
		return m.sharedSQLite(resource)
	default:
		envVar := strings.ToUpper(sanitize(engine)) + "_PROVISION_URL"
		if dsn := os.Getenv(envVar); dsn != "" {
			return &Result{
				Mode:           "shared",
				Endpoint:       dsn,
				ConnectionURL:  dsn,
				ConnectionHint: dsn,
				Metadata:       map[string]any{"engine": engine, "resource": resource},
			}, nil
		}
		return nil, fmt.Errorf("unsupported engine %s", engine)
	}
}

func (m *Manager) sharedPostgres(ctx context.Context, resource, pass, safe, short string) (*Result, error) {
	if m.PG == nil {
		return nil, fmt.Errorf("no postgres pool")
	}
	schema := resource
	role := "jp_u_" + short + "_" + safe
	if len(schema) > 63 {
		schema = schema[:63]
	}
	if len(role) > 63 {
		role = role[:63]
	}
	hostURL := os.Getenv("DATABASE_PROVISION_URL")
	if hostURL == "" {
		hostURL = os.Getenv("DATABASE_URL")
	}
	tx, err := m.PG.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s WITH LOGIN PASSWORD '%s';
			END IF;
		END $$;`, role, role, pass),
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s AUTHORIZATION %s`, schema, role),
		fmt.Sprintf(`GRANT ALL ON SCHEMA %s TO %s`, schema, role),
		fmt.Sprintf(`ALTER ROLE %s SET search_path TO %s`, role, schema),
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	conn, hint, endpoint := buildPostgresConn(hostURL, role, pass, schema)
	return &Result{
		Mode: "shared", Endpoint: endpoint, ConnectionURL: conn, ConnectionHint: hint,
		Metadata: map[string]any{"schema": schema, "role": role},
	}, nil
}

func buildPostgresConn(baseURL, role, pass, schema string) (conn, hint, endpoint string) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		conn = fmt.Sprintf("postgres://%s:%s@localhost:5432/jp?sslmode=disable&search_path=%s", role, pass, schema)
		hint = fmt.Sprintf("postgres://%s:***@localhost:5432/jp?search_path=%s", role, schema)
		endpoint = "localhost:5432"
		return
	}
	endpoint = u.Host
	u.User = url.UserPassword(role, pass)
	q := u.Query()
	q.Set("search_path", schema)
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "disable")
	}
	u.RawQuery = q.Encode()
	conn = u.String()
	u2, _ := url.Parse(conn)
	u2.User = url.UserPassword(role, "***")
	hint = u2.String()
	return
}

func (m *Manager) sharedMySQL(ctx context.Context, resource, pass, safe, short string) (*Result, error) {
	dsn := os.Getenv("MYSQL_PROVISION_URL")
	if dsn == "" {
		return nil, fmt.Errorf("MYSQL_PROVISION_URL not set")
	}
	user := "jp_" + short + "_" + safe
	if len(user) > 32 {
		user = user[:32]
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+resource+"`"); err != nil {
		return nil, err
	}
	_, _ = db.ExecContext(ctx, fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'", user, pass))
	_, _ = db.ExecContext(ctx, fmt.Sprintf("GRANT ALL ON `%s`.* TO '%s'@'%%'", resource, user))
	_, _ = db.ExecContext(ctx, "FLUSH PRIVILEGES")

	host := "mysql:3306"
	if u, err := url.Parse("mysql://" + strings.TrimPrefix(dsn, "mysql://")); err == nil && u.Host != "" {
		// dsn may be user:pass@tcp(host:3306)/
		host = extractMySQLHost(dsn)
	}
	conn := fmt.Sprintf("mysql://%s:%s@%s/%s", user, pass, host, resource)
	hint := fmt.Sprintf("mysql://%s:***@%s/%s", user, host, resource)
	return &Result{
		Mode: "shared", Endpoint: host, ConnectionURL: conn, ConnectionHint: hint,
		Metadata: map[string]any{"database": resource, "user": user},
	}, nil
}

func extractMySQLHost(dsn string) string {
	// formats: user:pass@tcp(host:3306)/db or user:pass@host:3306/db
	if i := strings.Index(dsn, "@tcp("); i >= 0 {
		rest := dsn[i+5:]
		if j := strings.Index(rest, ")"); j >= 0 {
			return rest[:j]
		}
	}
	if i := strings.Index(dsn, "@"); i >= 0 {
		rest := dsn[i+1:]
		if j := strings.IndexAny(rest, "/?"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return "mysql:3306"
}

func (m *Manager) sharedMongo(resource, pass, safe, short string) (*Result, error) {
	base := strings.TrimSpace(os.Getenv("MONGO_PROVISION_URL"))
	if base == "" {
		return nil, fmt.Errorf("MONGO_PROVISION_URL not set")
	}
	// Shared mode without mongo driver: mint credentials against configured host (namespace by db name).
	user := "jp_" + short + "_" + safe
	u, err := url.Parse(base)
	host := "mongo:27017"
	if err == nil && u.Host != "" {
		host = u.Host
	}
	conn := fmt.Sprintf("mongodb://%s:%s@%s/%s?authSource=%s", user, pass, host, resource, resource)
	hint := fmt.Sprintf("mongodb://%s:***@%s/%s", user, host, resource)
	return &Result{
		Mode: "shared", Endpoint: host, ConnectionURL: conn, ConnectionHint: hint,
		Metadata: map[string]any{"database": resource, "user": user, "note": "credentials issued; ensure admin bootstrap for auth if required"},
	}, nil
}

func (m *Manager) sharedRedis(ctx context.Context, resource, pass, safe, short string) (*Result, error) {
	redisURL := os.Getenv("REDIS_ADDON_URL")
	if redisURL == "" {
		return nil, fmt.Errorf("REDIS_ADDON_URL not set")
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	dbIdx := hashDB(short + safe)
	opt.DB = dbIdx
	rdb := redis.NewClient(opt)
	defer rdb.Close()
	key := resource + ":_jp_provisioned"
	if err := rdb.Set(ctx, key, "1", 0).Err(); err != nil {
		return nil, err
	}
	host := opt.Addr
	conn := fmt.Sprintf("redis://:%s@%s/%d", pass, host, dbIdx)
	if opt.Password != "" {
		conn = fmt.Sprintf("redis://:%s@%s/%d", opt.Password, host, dbIdx)
		pass = opt.Password
	} else {
		// Prefer shared redis password from URL; instance pass stored in metadata only if no auth
		conn = fmt.Sprintf("redis://%s/%d", host, dbIdx)
	}
	hint := fmt.Sprintf("redis://%s/%d key_prefix=%s:", host, dbIdx, resource)
	_ = pass
	return &Result{
		Mode: "shared", Endpoint: host, ConnectionURL: conn, ConnectionHint: hint,
		Metadata: map[string]any{"db": dbIdx, "key_prefix": resource + ":"},
	}, nil
}

func (m *Manager) sharedRabbit(ctx context.Context, resource, pass, safe, short string) (*Result, error) {
	base := strings.TrimRight(os.Getenv("RABBITMQ_MANAGEMENT_URL"), "/")
	amqpHost := os.Getenv("RABBITMQ_AMQP_HOST")
	if amqpHost == "" {
		amqpHost = "rabbitmq:5672"
	}
	if base == "" {
		return nil, fmt.Errorf("RABBITMQ_MANAGEMENT_URL not set")
	}
	adminUser := getenv("RABBITMQ_ADMIN_USER", "jp")
	adminPass := getenv("RABBITMQ_ADMIN_PASSWORD", "jp_dev_password")
	user := "jp_" + short + "_" + safe
	vhost := resource

	if err := m.rabbitPut(ctx, base, adminUser, adminPass, "/api/vhosts/"+url.PathEscape(vhost), nil); err != nil {
		return nil, err
	}
	userBody, _ := json.Marshal(map[string]any{"password": pass, "tags": ""})
	if err := m.rabbitPut(ctx, base, adminUser, adminPass, "/api/users/"+url.PathEscape(user), userBody); err != nil {
		return nil, err
	}
	permBody, _ := json.Marshal(map[string]string{"configure": ".*", "write": ".*", "read": ".*"})
	if err := m.rabbitPut(ctx, base, adminUser, adminPass, "/api/permissions/"+url.PathEscape(vhost)+"/"+url.PathEscape(user), permBody); err != nil {
		return nil, err
	}
	conn := fmt.Sprintf("amqp://%s:%s@%s/%s", user, pass, amqpHost, url.PathEscape(vhost))
	hint := fmt.Sprintf("amqp://%s:***@%s/%s", user, amqpHost, vhost)
	return &Result{
		Mode: "shared", Endpoint: amqpHost, ConnectionURL: conn, ConnectionHint: hint,
		Metadata: map[string]any{"vhost": vhost, "user": user},
	}, nil
}

func (m *Manager) rabbitPut(ctx context.Context, base, user, pass, path string, body []byte) error {
	var rdr *strings.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+path, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("rabbitmq api %s: %d", path, resp.StatusCode)
	}
	return nil
}

func (m *Manager) sharedKafka(resource string) (*Result, error) {
	brokers := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
	if brokers == "" {
		return nil, fmt.Errorf("KAFKA_BROKERS not set")
	}
	prefix := resource + "."
	hint := brokers + " topic_prefix=" + prefix
	return &Result{
		Mode: "shared", Endpoint: brokers, ConnectionURL: brokers, ConnectionHint: hint,
		Metadata: map[string]any{"topic_prefix": prefix, "brokers": brokers, "note": "use topic_prefix for isolation (ACL stub)"},
	}, nil
}

func (m *Manager) sharedSQLite(resource string) (*Result, error) {
	dir := os.Getenv("ADDON_SQLITE_DIR")
	if dir == "" {
		dir = "/var/jp/addons/sqlite"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, resource+".db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	slash := filepath.ToSlash(path)
	return &Result{
		Mode: "shared", Endpoint: slash, ConnectionURL: slash, ConnectionHint: slash,
		Metadata: map[string]any{"path": slash},
	}, nil
}

func hashDB(s string) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = 31*h + int(s[i])
	}
	if h < 0 {
		h = -h
	}
	return h%14 + 1 // avoid 0; leave room under 16
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// MetadataJSON marshals metadata for storage.
func MetadataJSON(m map[string]any) json.RawMessage {
	if m == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// ParseMetadata unmarshals metadata from JSON.
func ParseMetadata(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}