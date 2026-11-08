package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogSanity(t *testing.T) {
	all := All()
	if len(all) != 241 {
		t.Fatalf("expected 241 catalog entries, got %d", len(all))
	}
	reserved := map[string]bool{"custom": true, "catalog": true, "provision": true, "backups": true, "connection": true}
	seen := map[string]bool{}
	prov := 0
	for _, it := range all {
		if !idRe.MatchString(it.ID) {
			t.Errorf("bad id %q", it.ID)
		}
		if seen[it.ID] {
			t.Errorf("duplicate id %q", it.ID)
		}
		seen[it.ID] = true
		if reserved[it.ID] {
			t.Errorf("id %q collides with a reserved word", it.ID)
		}
		if it.Provisionable {
			prov++
			if it.DefaultImage == "" || it.DefaultPort <= 0 || it.Probe == nil || it.Driver == "" || it.Driver == "generic" {
				t.Errorf("%s: provisionable without image/port/probe/driver: %+v", it.ID, it)
			}
			if strings.HasSuffix(it.DefaultImage, ":latest") && it.ID != "keydb" {
				t.Errorf("%s: provisionable image must be pinned, got %s", it.ID, it.DefaultImage)
			}
			if !it.ArchOK || it.Arch != "amd64,arm64" {
				t.Errorf("%s: provisionable images must be multi-arch", it.ID)
			}
			if it.Probe.Type == "http" && it.Probe.Path == "" {
				t.Errorf("%s: http probe needs a path", it.ID)
			}
			if it.Reason != "" {
				t.Errorf("%s: provisionable with a reason", it.ID)
			}
		} else if it.Reason == "" || ReasonText[it.Reason] == "" {
			t.Errorf("%s: non provisionable without a known reason (%q)", it.ID, it.Reason)
		}
	}
	if prov != len(provisionable) {
		t.Fatalf("provisionable count %d != specs %d", prov, len(provisionable))
	}
	for _, id := range []string{"postgres", "mysql", "mariadb", "mongodb", "redis", "valkey", "dragonfly", "rabbitmq", "clickhouse",
		"meilisearch", "elasticsearch", "opensearch", "kafka", "nats", "memcached"} {
		if it, ok := Get(id); !ok || !it.Provisionable {
			t.Errorf("first-class engine %s must be provisionable", id)
		}
	}
	if it, _ := Get("sqlite"); it.Provisionable || it.Reason != ReasonNoServer {
		t.Errorf("sqlite: %+v", it)
	}
	if it, _ := Get("minio"); it.Provisionable || it.Reason != ReasonImage {
		t.Errorf("minio image is not publicly pullable: %+v", it)
	}
	t.Logf("catalog: %d entries, %d provisionable", len(all), prov)
}

func TestImmutableCopies(t *testing.T) {
	a := All()
	a[0].ID = "hacked"
	if All()[0].ID == "hacked" {
		t.Fatal("All() must return a copy")
	}
}

func TestSecretName(t *testing.T) {
	for _, tc := range [][3]string{
		{"postgres", "primary", "POSTGRES_URL_PRIMARY"},
		{"valkey", "cache-prod", "VALKEY_URL_CACHE_PROD"},
		{"kafka", "events", "KAFKA_BROKERS_EVENTS"},
		{"nope", "x", "NOPE_URL_X"},
	} {
		if got := SecretName(tc[0], tc[1]); got != tc[2] {
			t.Errorf("SecretName(%s,%s)=%s want %s", tc[0], tc[1], got, tc[2])
		}
	}
}

func TestLoadCatalogDirStartupOnlyRules(t *testing.T) {
	dir := t.TempDir()
	write := func(n, c string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.json", `{"id":"my-queue","name":"My Queue","category":"queue","default_image":"example/q:1","default_port":1234,"provisionable":true,"probe":{"type":"tcp"},"driver":"generic"}`)
	write("incomplete.json", `{"id":"half-baked","name":"x","category":"tool","provisionable":true}`)
	write("override.json", `{"id":"postgres","name":"Evil","default_image":"evil/pg"}`)
	write("bad.json", `{not json`)
	n, err := LoadCatalogDir(dir)
	if err == nil {
		t.Fatal("invalid files must be reported")
	}
	if n != 2 {
		t.Fatalf("loaded=%d", n)
	}
	if it, ok := Get("my-queue"); !ok || !it.Provisionable {
		t.Fatalf("%+v", it)
	}
	if it, _ := Get("half-baked"); it.Provisionable || it.Reason == "" {
		t.Fatalf("incomplete entries must not be provisionable: %+v", it)
	}
	if it, _ := Get("postgres"); it.DefaultImage != "postgres:16-alpine" {
		t.Fatal("built-in entries cannot be overridden")
	}
}
