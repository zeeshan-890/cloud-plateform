package provisioner

import (
	"context"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"my_db", "my_db"},
		{"My-Db-123", "my_db_123"},
		{"123_invalid_start", "db_123_invalid_start"},
		{"", "db_"},
	}

	for _, tt := range tests {
		got := sanitize(tt.in)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildConn(t *testing.T) {
	baseURL := "postgres://admin:pass@postgres.internal:5432/jp"
	conn, hint := buildConn(baseURL, "app_user", "mypass123", "schema_app")

	if !strings.Contains(conn, "app_user:mypass123@postgres.internal:5432/jp") {
		t.Errorf("expected credentials and host in conn, got %s", conn)
	}
	if !strings.Contains(conn, "search_path=schema_app") {
		t.Errorf("expected search_path in conn query, got %s", conn)
	}
	if !strings.Contains(hint, "app_user:%2A%2A%2A@postgres.internal:5432") && !strings.Contains(hint, "app_user:***@postgres.internal:5432") {
		t.Errorf("expected masked password in hint, got %s", hint)
	}
	if strings.Contains(hint, "mypass123") {
		t.Errorf("CRITICAL: plaintext password leaked in connection hint: %s", hint)
	}
}

func TestProvisionerSimulate(t *testing.T) {
	p := NewFromEnv(nil)
	ctx := context.Background()

	res, err := p.Create(ctx, "org-1", "proj-uuid-1234", "orders_db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mode != "simulate" {
		t.Errorf("expected simulate mode, got %s", res.Mode)
	}
	if !strings.HasPrefix(res.RoleName, "jp_u_") {
		t.Errorf("expected role name starting with jp_u_, got %s", res.RoleName)
	}
	if !strings.HasPrefix(res.SchemaName, "jp_") {
		t.Errorf("expected schema name starting with jp_, got %s", res.SchemaName)
	}
}
