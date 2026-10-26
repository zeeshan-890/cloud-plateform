package provisioner

import (
	"context"
	"strings"
	"testing"

	"github.com/jp-cloud/addon/internal/catalog"
)

func TestProvisionerSimulate(t *testing.T) {
	mgr := NewFromEnv(nil)
	ctx := context.Background()

	engines := []string{"postgres", "mysql", "mariadb", "mongodb", "redis", "valkey", "rabbitmq", "meilisearch", "clickhouse", "minio"}
	for _, eng := range engines {
		res, err := mgr.Create(ctx, "org-1", "proj-1", eng, "testdb")
		if err != nil {
			t.Errorf("failed to simulate %s: %v", eng, err)
			continue
		}
		if res.Mode != "simulate" {
			t.Errorf("expected mode simulate for %s, got %s", eng, res.Mode)
		}
		if res.ConnectionURL == "" {
			t.Errorf("expected non-empty ConnectionURL for %s", eng)
		}
		if res.Endpoint == "" {
			t.Errorf("expected non-empty Endpoint for %s", eng)
		}
	}
}

func TestProvisionerDynamicCustomEngine(t *testing.T) {
	catalog.Register(catalog.Item{
		ID:          "dragonfly",
		Name:        "Dragonfly",
		Category:    "cache",
		DefaultPort: 6379,
		Scheme:      "redis",
		EnvPrefix:   "DRAGONFLY_URL_",
	})

	mgr := NewFromEnv(nil)
	ctx := context.Background()

	res, err := mgr.Create(ctx, "org-1", "proj-1", "dragonfly", "cache")
	if err != nil {
		t.Fatalf("failed to simulate dynamic engine dragonfly: %v", err)
	}
	if !strings.HasPrefix(res.ConnectionURL, "redis://") {
		t.Errorf("expected redis:// scheme, got %s", res.ConnectionURL)
	}
	if !strings.Contains(res.ConnectionURL, "localhost:6379") {
		t.Errorf("expected localhost:6379 in connection url, got %s", res.ConnectionURL)
	}
}
