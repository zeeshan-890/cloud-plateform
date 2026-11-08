package gateway

import (
	"encoding/json"
	"testing"
)

func TestServerInventory_AdminOnly(t *testing.T) {
	e := newEnv(t)
	member := e.user("member", false)
	admin := e.user("", true)
	for _, c := range []struct{ m, p string }{{"GET", "/api/v1/cluster/servers"}, {"PUT", "/api/v1/cluster/servers/a"}, {"DELETE", "/api/v1/cluster/servers/a"}} {
		if rec := e.do(c.m, c.p, "", nil); rec.Code != 401 {
			t.Errorf("%s %s anonymous = %d, want 401", c.m, c.p, rec.Code)
		}
		if rec := e.do(c.m, c.p, member.token, map[string]any{"name": "a"}); rec.Code != 403 {
			t.Errorf("%s %s non-admin = %d, want 403", c.m, c.p, rec.Code)
		}
	}
	if rec := e.do("GET", "/api/v1/cluster/servers", admin.token, nil); rec.Code != 200 {
		t.Fatalf("admin list = %d %s", rec.Code, rec.Body)
	}
}

func TestServerInventory_CRUDAndValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.user("", true)
	good := map[string]any{"name": "db-1", "role": "database", "public_addr": "92.4.70.208", "private_addr": "10.0.0.88", "prefer": "private",
		"ports": []map[string]any{{"port": 6379, "scope": "private", "label": "redis"}, {"port": 5432, "scope": "private", "label": "postgres"}}}
	rec := e.do("PUT", "/api/v1/cluster/servers/db-1", admin.token, good)
	if rec.Code != 200 {
		t.Fatalf("put = %d %s", rec.Code, rec.Body)
	}
	var got serverEntry
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Ports[0].Port != 5432 || got.Ports[0].Proto != "tcp" {
		t.Errorf("ports should be sorted with tcp default: %+v", got.Ports)
	}

	var list struct{ Servers []serverEntry }
	rec = e.do("GET", "/api/v1/cluster/servers", admin.token, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Servers) != 1 || list.Servers[0].PrivateAddr != "10.0.0.88" {
		t.Fatalf("list = %s", rec.Body)
	}

	bad := []map[string]any{
		{"name": "db-1", "private_addr": "10.0.0.88; rm -rf /"},
		{"name": "db-1"},
		{"name": "db-1", "private_addr": "10.0.0.88", "prefer": "sideways"},
		{"name": "db-1", "private_addr": "10.0.0.88", "prefer": "public"},
		{"name": "db-1", "private_addr": "10.0.0.88", "role": "wizard"},
		{"name": "db-1", "private_addr": "10.0.0.88", "ports": []map[string]any{{"port": 70000}}},
		{"name": "db-1", "private_addr": "10.0.0.88", "ports": []map[string]any{{"port": 80, "scope": "public"}}},
		{"name": "db-1", "private_addr": "10.0.0.88", "ports": []map[string]any{{"port": 80}, {"port": 80}}},
		{"name": "other", "private_addr": "10.0.0.88"}, // name must match URL
	}
	for i, b := range bad {
		if rec := e.do("PUT", "/api/v1/cluster/servers/db-1", admin.token, b); rec.Code != 400 {
			t.Errorf("bad[%d] = %d, want 400 (%s)", i, rec.Code, rec.Body)
		}
	}
	// rejected writes must not have changed the stored server
	rec = e.do("GET", "/api/v1/cluster/servers", admin.token, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Servers) != 1 || len(list.Servers[0].Ports) != 2 {
		t.Errorf("invalid write changed state: %s", rec.Body)
	}

	if rec := e.do("DELETE", "/api/v1/cluster/servers/db-1", admin.token, nil); rec.Code != 204 {
		t.Errorf("delete = %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/v1/cluster/servers/db-1", admin.token, nil); rec.Code != 404 {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
}

func TestServerInventory_Limit(t *testing.T) {
	e := newEnv(t)
	admin := e.user("", true)
	for i := 0; i < 32; i++ {
		name := "n" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if rec := e.do("PUT", "/api/v1/cluster/servers/"+name, admin.token, map[string]any{"name": name, "private_addr": "10.0.0.1"}); rec.Code != 200 {
			t.Fatalf("put %s = %d", name, rec.Code)
		}
	}
	if rec := e.do("PUT", "/api/v1/cluster/servers/extra", admin.token, map[string]any{"name": "extra", "private_addr": "10.0.0.1"}); rec.Code != 400 {
		t.Errorf("33rd server = %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/cluster/servers/naa", admin.token, map[string]any{"name": "naa", "private_addr": "10.0.0.2"}); rec.Code != 200 {
		t.Errorf("updating an existing server at the limit = %d, want 200", rec.Code)
	}
}
