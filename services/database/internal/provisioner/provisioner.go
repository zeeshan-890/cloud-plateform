package provisioner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var identSafe = regexp.MustCompile(`[^a-z0-9_]`)

type Result struct {
	Mode           string
	SchemaName     string
	RoleName       string
	ConnectionURL  string
	ConnectionHint string
}

type Provisioner struct {
	Mode   string
	Admin  *pgxpool.Pool
	HostURL string // base postgres URL for building app connection strings
}

func NewFromEnv(admin *pgxpool.Pool) *Provisioner {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DB_MODE")))
	if mode == "" {
		mode = "simulate"
	}
	hostURL := os.Getenv("DATABASE_PROVISION_URL")
	if hostURL == "" {
		hostURL = os.Getenv("DATABASE_URL")
	}
	return &Provisioner{Mode: mode, Admin: admin, HostURL: hostURL}
}

func sanitize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = identSafe.ReplaceAllString(n, "_")
	if len(n) > 40 {
		n = n[:40]
	}
	if n == "" || !((n[0] >= 'a' && n[0] <= 'z') || n[0] == '_') {
		n = "db_" + n
	}
	return n
}

func randomPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *Provisioner) Create(ctx context.Context, orgID, projectID, name string) (*Result, error) {
	safe := sanitize(name)
	short := strings.ReplaceAll(projectID, "-", "")
	if len(short) > 8 {
		short = short[:8]
	}
	schema := fmt.Sprintf("jp_%s_%s", short, safe)
	role := fmt.Sprintf("jp_u_%s_%s", short, safe)
	if len(schema) > 63 {
		schema = schema[:63]
	}
	if len(role) > 63 {
		role = role[:63]
	}
	pass := randomPassword()

	if p.Mode == "simulate" || p.Admin == nil {
		hint := fmt.Sprintf("postgres://%s:***@localhost:5432/jp?search_path=%s (simulate)", role, schema)
		conn := fmt.Sprintf("postgres://%s:%s@localhost:5432/jp?sslmode=disable&search_path=%s", role, pass, schema)
		return &Result{Mode: "simulate", SchemaName: schema, RoleName: role, ConnectionURL: conn, ConnectionHint: hint}, nil
	}

	// Schema-per-db isolation on shared Postgres
	tx, err := p.Admin.Begin(ctx)
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
			// Fall back to simulate if privileges insufficient
			hint := fmt.Sprintf("postgres://%s:***@shared (simulate-fallback)", role)
			conn := fmt.Sprintf("postgres://%s:%s@localhost:5432/jp?sslmode=disable&search_path=%s", role, pass, schema)
			return &Result{Mode: "simulate", SchemaName: schema, RoleName: role, ConnectionURL: conn, ConnectionHint: hint}, nil
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	conn, hint := buildConn(p.HostURL, role, pass, schema)
	return &Result{Mode: "schema", SchemaName: schema, RoleName: role, ConnectionURL: conn, ConnectionHint: hint}, nil
}

func (p *Provisioner) Drop(ctx context.Context, schema, role string) error {
	if p.Mode == "simulate" || p.Admin == nil || schema == "" {
		return nil
	}
	_, _ = p.Admin.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
	if role != "" {
		_, _ = p.Admin.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
	}
	return nil
}

func buildConn(baseURL, role, pass, schema string) (conn, hint string) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		conn = fmt.Sprintf("postgres://%s:%s@localhost:5432/jp?sslmode=disable&search_path=%s", role, pass, schema)
		hint = fmt.Sprintf("postgres://%s:***@localhost:5432/jp?search_path=%s", role, schema)
		return
	}
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
