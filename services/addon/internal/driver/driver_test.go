package driver

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jp-cloud/addon/internal/catalog"
)

func TestBuildURLEscapesSpecialCharacters(t *testing.T) {
	pw := "p@ss:w/rd?#%[]!$&'()*+,;= é\""
	for _, tc := range []struct{ scheme, user, path string }{
		{"postgres", "app", "app"}, {"mysql", "app", "app"}, {"redis", "", "0"}, {"amqp", "app", ""},
	} {
		raw := BuildURL(tc.scheme, tc.user, pw, "addon-db", 1234, tc.path, "")
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: unparsable %q: %v", tc.scheme, raw, err)
		}
		got, _ := u.User.Password()
		if got != pw || u.User.Username() != tc.user || u.Hostname() != "addon-db" || u.Port() != "1234" {
			t.Errorf("%s: roundtrip failed: %q -> user=%q pw=%q host=%q", tc.scheme, raw, u.User.Username(), got, u.Host)
		}
		if strings.Contains(raw, "#") || strings.Count(raw, "@") != 1 || strings.Contains(raw, " ") {
			t.Errorf("%s: unescaped characters in %q", tc.scheme, raw)
		}
		c, err := CredsFromURL(raw)
		if err != nil || c.Password != pw {
			t.Errorf("CredsFromURL lost the password: %v %q", err, c.Password)
		}
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		p := NewPassword(1)
		if len(p) < 24 || seen[p] || strings.Trim(p, alnum) != "" {
			t.Fatalf("bad password %q", p)
		}
		seen[p] = true
	}
}

func TestEveryProvisionableItemBuildsAPlanWithServerSideCredentials(t *testing.T) {
	for _, it := range catalog.All() {
		if !it.Provisionable {
			if _, err := Build(it, "x1", NewPassword(32), Options{}); err == nil {
				t.Errorf("%s: building a non-provisionable item must fail", it.ID)
			}
			continue
		}
		if !Supported(it.Driver) {
			t.Errorf("%s: driver %s not implemented", it.ID, it.Driver)
			continue
		}
		pw := NewPassword(32)
		p, err := Build(it, "my-db", pw, Options{})
		if err != nil {
			t.Fatalf("%s: %v", it.ID, err)
		}
		if p.Image != it.DefaultImage || p.Port == 0 || p.Host != "addon-my-db" || p.Health.Type == "" || p.Health.TimeoutSeconds != 120 {
			t.Errorf("%s: incomplete plan %+v", it.ID, p)
		}
		if !strings.Contains(p.URL, "addon-my-db") {
			t.Errorf("%s: URL must use the project-network alias: %s", it.ID, p.URL)
		}
		if p.DefaultKey == "" {
			t.Errorf("%s: no default secret key", it.ID)
		}
		if it.NoAuth {
			continue
		}
		applied := len(p.Env) > 0
		if !applied {
			for _, v := range p.Command {
				applied = applied || strings.Contains(v, pw)
			}
		}
		if !applied {
			t.Errorf("%s: credentials are not configured on the server: env=%v cmd=%v", it.ID, p.Env, p.Command)
		}
		// the generated secret must reach the server through env or command
		found := false
		for _, v := range p.Env {
			found = found || strings.Contains(v, pw)
		}
		for _, v := range p.Command {
			found = found || strings.Contains(v, pw)
		}
		if !found {
			t.Errorf("%s: password never reaches the server: env=%v cmd=%v", it.ID, p.Env, p.Command)
		}
	}
}

func TestPostgresPlan(t *testing.T) {
	it, _ := catalog.Get("postgres")
	pw := NewPassword(32)
	p, _ := Build(it, "primary", pw, Options{PGStatStatements: true})
	if p.Env["POSTGRES_PASSWORD"] != pw || p.URL != "postgres://app:"+pw+"@addon-primary:5432/app?sslmode=disable" {
		t.Fatalf("%+v", p)
	}
	if len(p.Command) == 0 || !strings.Contains(strings.Join(p.Command, " "), "pg_stat_statements") {
		t.Fatalf("pg_stat_statements must be preloaded: %v", p.Command)
	}
	if p.Volume != "/var/lib/postgresql/data" || p.Health.Type != "cmd" {
		t.Fatalf("%+v", p)
	}
}

func TestRedisPlanRequiresPassword(t *testing.T) {
	it, _ := catalog.Get("redis")
	p, _ := Build(it, "cache", NewPassword(32), Options{})
	if !strings.Contains(strings.Join(p.Command, " "), "--requirepass") || p.Env["REDIS_PASSWORD"] == "" {
		t.Fatalf("%+v", p)
	}
	if !strings.HasPrefix(p.URL, "redis://:") {
		t.Fatalf("redis URL must carry the password with an empty user: %s", p.URL)
	}
}

func TestRejectsShortPassword(t *testing.T) {
	it, _ := catalog.Get("postgres")
	if _, err := Build(it, "x", "short", Options{}); err == nil {
		t.Fatal("short passwords must be refused")
	}
}

func TestBackupKinds(t *testing.T) {
	for drv, restore := range map[string]bool{"postgres": true, "mysql": true, "mariadb": true, "mongodb": true, "redis": false, "valkey": false} {
		k, ok := Backup(drv)
		if !ok || k.Dump == nil || (k.Restore != nil) != restore {
			t.Errorf("%s: %+v", drv, k)
			continue
		}
		spec := k.Dump(Creds{User: "app", Password: "secret-pw", DB: "app"})
		for _, a := range spec.Cmd {
			if strings.Contains(a, "secret-pw") {
				t.Errorf("%s: password must not appear in argv: %v", drv, spec.Cmd)
			}
		}
	}
	if _, ok := Backup("memcached"); ok {
		t.Fatal("no backups for memcached")
	}
}
