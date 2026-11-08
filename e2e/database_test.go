package e2e

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDatabases_ManagedPostgres_ConnectBackupRestore(t *testing.T) {
	e := NeedSSH(t)
	admin := AdminClient(t)
	img := FixtureImage(t)
	org := NewOrg(t, admin)
	p := NewProject(t, admin, org)
	host := SystemHost(t, admin, p)
	admin.Timeout = 10 * time.Minute
	defer func() { admin.Timeout = 60 * time.Second }()

	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)

	// ---- create
	cr := admin.Must(t, "POST", p.Path("/databases"), map[string]any{"name": "e2edb", "secret_name": "DATABASE_URL"}, 201).Map(t)
	db := Dig(cr, "database")
	dbID := Str(db, "id")
	if dbID == "" || Str(db, "status") != "ready" {
		t.Fatalf("managed database not ready: %s", mustJSON(cr))
	}
	if Dig(db, "public_url") != nil {
		t.Errorf("managed database has a public_url")
	}
	if r := admin.Do("POST", p.Path("/databases"), map[string]any{"name": "e2edb"}); r.Status != 409 {
		t.Errorf("duplicate database name: want 409, got %d", r.Status)
	}
	if r := admin.Do("POST", p.Path("/databases"), map[string]any{"name": "Bad Name!"}); r.Status != 400 {
		t.Errorf("invalid database name: want 400, got %d", r.Status)
	}
	if l := admin.Must(t, "GET", p.Path("/databases"), nil, 200).Map(t); len(Arr(l, "databases")) != 1 {
		t.Errorf("database list = %v", Arr(l, "databases"))
	}

	// ---- the container is private
	var dbc *Ctr
	for _, c := range e.ProjectContainers(t, p.ID) {
		if strings.Contains(c.Image, "postgres") {
			c := c
			dbc = &c
		}
	}
	if dbc == nil {
		t.Fatalf("no postgres container for the project: %+v", e.ProjectContainers(t, p.ID))
	}
	ins := e.Inspect(t, dbc.ID)
	for pt, b := range ins.Net.Ports {
		if len(b) > 0 {
			t.Errorf("database publishes %s: %v", pt, b)
		}
	}
	if _, ok := ins.Net.Networks[p.Network()]; !ok || len(ins.Net.Networks) != 1 {
		t.Errorf("database must live only on %s: %v", p.Network(), ins.Net.Networks)
	}
	if e.PortOpen("5432") {
		t.Errorf("5432 reachable from outside")
	}

	// ---- app connects (restart to pick up DATABASE_URL)
	DeployApp(t, admin, p, img, nil)
	WaitEdge(t, e, host, "/health", 200, 90*time.Second)
	Eventually(t, 90*time.Second, 3*time.Second, "app writes/reads through the managed database", func() (bool, string) {
		code, body, err := e.EdgeGet(host, "/probe?only=postgres")
		if err != nil {
			return false, err.Error()
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		return code == 200 && Bool(m, "postgres", "ok"), truncate(body, 300)
	})

	// ---- credentials for direct SQL via docker exec (admin reveal)
	sec := admin.Must(t, "GET", p.Path("/environments/production/secrets/DATABASE_URL"), nil, 200).Map(t)
	u, err := url.Parse(Str(sec, "secret", "value"))
	if err != nil || u.User == nil {
		t.Fatalf("cannot parse DATABASE_URL: %v", err)
	}
	pass, _ := u.User.Password()
	sql := func(q string) string {
		return e.Run(t, "docker exec -e PGPASSWORD="+shQuote(pass)+" "+dbc.ID+" psql -h 127.0.0.1 -U "+shQuote(u.User.Username())+" -d "+shQuote(strings.TrimPrefix(u.Path, "/"))+" -tAc "+shQuote(q))
	}
	count := func() int {
		n, _ := strconv.Atoi(strings.TrimSpace(sql("SELECT count(*) FROM e2e_backup_marker")))
		return n
	}
	sql("CREATE TABLE IF NOT EXISTS e2e_backup_marker (id int primary key, note text)")
	sql("INSERT INTO e2e_backup_marker SELECT g, 'row-' || g FROM generate_series(1,25) g")
	if count() != 25 {
		t.Fatalf("marker rows not inserted")
	}

	// ---- backup
	bk := admin.Must(t, "POST", p.Path("/databases/"+dbID+"/backups"), nil, 201).Map(t)
	bid := Str(bk, "backup", "id")
	if bid == "" || Num(bk, "backup", "size_bytes") <= 0 {
		t.Fatalf("backup without id/size: %s", mustJSON(bk))
	}
	if st := Str(bk, "backup", "status"); st != "completed" && st != "ready" && st != "succeeded" && st != "done" {
		t.Errorf("backup status = %q", st)
	}
	if Str(bk, "backup", "sha256") == "" {
		t.Errorf("backup has no checksum")
	}
	lb := admin.Must(t, "GET", p.Path("/databases/"+dbID+"/backups"), nil, 200).Map(t)
	if len(Arr(lb, "backups")) != 1 {
		t.Errorf("backup list = %v", lb)
	}

	// ---- destroy data, restore, verify
	sql("DELETE FROM e2e_backup_marker WHERE id > 5")
	sql("INSERT INTO e2e_backup_marker VALUES (1000, 'after-backup') ON CONFLICT DO NOTHING")
	if count() == 25 {
		t.Fatalf("test setup: data not modified")
	}
	rs := admin.Must(t, "POST", p.Path("/databases/"+dbID+"/backups/"+bid+"/restore"), nil, 200).Map(t)
	if !Bool(rs, "restored") || !Bool(rs, "verified") {
		t.Errorf("restore not verified: %s", mustJSON(rs))
	}
	if n := count(); n != 25 {
		t.Errorf("after restore marker rows = %d, want 25 (data of the backup)", n)
	}
	if out := sql("SELECT count(*) FROM e2e_backup_marker WHERE id=1000"); strings.TrimSpace(out) != "0" {
		t.Errorf("row written after the backup survived the restore")
	}
	if r := admin.Do("POST", p.Path("/databases/"+dbID+"/backups/"+zeroUUID+"/restore"), nil); r.Status != 404 {
		t.Errorf("restore of unknown backup: want 404, got %d", r.Status)
	}
	// still usable by the app after the restore
	code, body, _ := e.EdgeGet(host, "/probe?only=postgres")
	if code != 200 {
		t.Errorf("app probe after restore: %d %s", code, truncate(body, 200))
	}

	// ---- diagnostics + RBAC
	sq := admin.Must(t, "GET", p.Path("/databases/"+dbID+"/slow-queries"), nil, 200).Map(t)
	if Str(sq, "source") != "pg_stat_statements" {
		t.Errorf("slow-queries source = %q", Str(sq, "source"))
	}
	viewer := CreateUser(t, admin, org, "member", false, Grant{"project_id": p.ID, "preset": "developer"})
	r := viewer.C.Must(t, "GET", p.Path("/databases/"+dbID), nil, 200)
	if strings.Contains(string(r.Body), pass) {
		t.Errorf("view-only user receives the database password")
	}
	if r := viewer.C.Do("POST", p.Path("/databases/"+dbID+"/backups"), nil); r.Status != 403 {
		t.Errorf("developer (databases: view) creating a backup: want 403, got %d", r.Status)
	}
	if r := viewer.C.Do("DELETE", p.Path("/databases/"+dbID), nil); r.Status != 403 {
		t.Errorf("developer deleting a database: want 403, got %d", r.Status)
	}

	// ---- delete removes the container
	admin.Must(t, "DELETE", p.Path("/databases/"+dbID+"?purge=true"), nil, 204)
	if out, _ := e.TryRun("docker ps -a -q --filter id=" + dbc.ID); strings.TrimSpace(out) != "" {
		t.Errorf("database container still exists after delete")
	}
	admin.Must(t, "GET", p.Path("/databases/"+dbID), nil, 404)
}
