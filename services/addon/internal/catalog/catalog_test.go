package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogDefaults(t *testing.T) {
	all := All()
	t.Logf("Total catalog engines registered: %d", len(all))
	if len(all) < 15 {
		t.Fatalf("expected at least 15 default engines, got %d", len(all))
	}

	expected := []string{"postgres", "mysql", "mariadb", "mongodb", "redis", "valkey", "rabbitmq", "kafka", "sqlite", "meilisearch", "clickhouse", "minio"}
	for _, id := range expected {
		if !ValidEngine(id) {
			t.Errorf("expected engine %q to be valid", id)
		}
		item, ok := Get(id)
		if !ok || item.ID != id {
			t.Errorf("expected to get valid item for %q", id)
		}
	}
}

func TestSecretName(t *testing.T) {
	tests := []struct {
		engine string
		name   string
		want   string
	}{
		{"postgres", "primary", "POSTGRES_URL_PRIMARY"},
		{"valkey", "cache-prod", "VALKEY_URL_CACHE_PROD"},
		{"meilisearch", "search", "MEILISEARCH_URL_SEARCH"},
		{"custom_app", "my-app", "CUSTOM_APP_URL_MY_APP"},
	}

	for _, tt := range tests {
		got := SecretName(tt.engine, tt.name)
		if got != tt.want {
			t.Errorf("SecretName(%q, %q) = %q, want %q", tt.engine, tt.name, got, tt.want)
		}
	}
}

func TestDynamicRegistrationAndLoadDir(t *testing.T) {
	custom := Item{
		ID:          "typesense",
		Name:        "Typesense",
		Category:    "search",
		Description: "Fast, typo-tolerant search engine",
		DefaultPort: 8108,
		Scheme:      "http",
		EnvPrefix:   "TYPESENSE_URL_",
	}
	Register(custom)

	if !ValidEngine("typesense") {
		t.Fatalf("expected typesense to be registered")
	}
	item, ok := Get("typesense")
	if !ok || item.DefaultPort != 8108 {
		t.Fatalf("expected typesense port 8108, got %+v", item)
	}

	tmpDir := t.TempDir()
	jsonContent := `{
		"id": "chroma",
		"name": "ChromaDB",
		"category": "database",
		"description": "AI-native open-source embedding database",
		"default_port": 8000,
		"scheme": "http",
		"env_prefix": "CHROMA_URL_"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "chroma.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("failed to write test json: %v", err)
	}

	if err := LoadCatalogDir(tmpDir); err != nil {
		t.Fatalf("LoadCatalogDir failed: %v", err)
	}

	if !ValidEngine("chroma") {
		t.Fatalf("expected dynamically loaded chroma engine to be valid")
	}
}
