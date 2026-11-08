package driver

import (
	"fmt"
	"net/url"
	"strings"
)

// ExecSpec is a command executed inside the add-on container (credentials go through env, never argv).
type ExecSpec struct {
	Cmd []string
	Env map[string]string
}

// BackupKind describes what a driver can do.
type BackupKind struct {
	Ext     string // file extension of the artifact
	Dump    func(c Creds) ExecSpec
	Restore func(c Creds) ExecSpec // nil = restore not supported
	Verify  func(c Creds) ExecSpec // lists the archive from stdin (cheap integrity check before restore); may be nil
	Stats   func(c Creds) ExecSpec // prints a single integer (objects count) used to verify a restore
}

// Backup returns the backup implementation of a driver (ok=false when none).
func Backup(drv string) (BackupKind, bool) {
	k, ok := backups[drv]
	return k, ok
}

var backups = map[string]BackupKind{
	"postgres": {
		Ext: "pgdump",
		Dump: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"pg_dump", "-Fc", "--no-owner", "-h", "127.0.0.1", "-U", c.User, "-d", c.DB}, Env: map[string]string{"PGPASSWORD": c.Password}}
		},
		Restore: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"pg_restore", "--clean", "--if-exists", "--no-owner", "--exit-on-error", "-h", "127.0.0.1", "-U", c.User, "-d", c.DB}, Env: map[string]string{"PGPASSWORD": c.Password}}
		},
		Verify: func(c Creds) ExecSpec { return ExecSpec{Cmd: []string{"pg_restore", "--list"}} },
		Stats: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"psql", "-h", "127.0.0.1", "-U", c.User, "-d", c.DB, "-At", "-c",
				"SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')"}, Env: map[string]string{"PGPASSWORD": c.Password}}
		},
	},
	"mysql":   mysqlBackup("mysqldump", "mysql"),
	"mariadb": mysqlBackup("mariadb-dump", "mariadb"),
	"mongodb": {
		Ext: "archive.gz",
		Dump: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"mongodump", "--archive", "--gzip", "--db", c.DB, "--username", c.User, "--authenticationDatabase", "admin", "--host", "127.0.0.1"},
				Env: map[string]string{}}.withMongoPass(c)
		},
		Restore: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"mongorestore", "--archive", "--gzip", "--drop", "--username", c.User, "--authenticationDatabase", "admin", "--host", "127.0.0.1"}}.withMongoPass(c)
		},
		Stats: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"sh", "-c", `mongosh --quiet -u "$U" -p "$P" --authenticationDatabase admin "$D" --eval "db.getCollectionNames().length"`},
				Env: map[string]string{"U": c.User, "P": c.Password, "D": c.DB}}
		},
	},
	// Redis family: an RDB snapshot can be taken online; restoring needs a server restart so it is not offered.
	"redis":  redisBackup("redis-cli"),
	"valkey": redisBackup("valkey-cli"),
}

// withMongoPass supplies the password through a config-free env var read by a wrapper shell.
func (e ExecSpec) withMongoPass(c Creds) ExecSpec {
	cmd := append([]string{"sh", "-c", `exec "$@" --password "$MONGO_PW"`, "sh"}, e.Cmd...)
	return ExecSpec{Cmd: cmd, Env: map[string]string{"MONGO_PW": c.Password}}
}

func mysqlBackup(dump, cli string) BackupKind {
	return BackupKind{
		Ext: "sql",
		Dump: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{dump, "--single-transaction", "--routines", "--triggers", "-h", "127.0.0.1", "-u", c.User, "--databases", c.DB}, Env: map[string]string{"MYSQL_PWD": c.Password}}
		},
		Restore: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{cli, "-h", "127.0.0.1", "-u", c.User}, Env: map[string]string{"MYSQL_PWD": c.Password}}
		},
		Stats: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{cli, "-h", "127.0.0.1", "-u", c.User, "-N", "-B", "-e",
				fmt.Sprintf("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='%s'", strings.ReplaceAll(c.DB, "'", ""))}, Env: map[string]string{"MYSQL_PWD": c.Password}}
		},
	}
}

func redisBackup(cli string) BackupKind {
	return BackupKind{
		Ext: "rdb",
		Dump: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"sh", "-c", cli + ` -a "$REDISCLI_AUTH" --no-auth-warning SAVE >&2 && cat /data/dump.rdb`}, Env: map[string]string{"REDISCLI_AUTH": c.Password}}
		},
		Stats: func(c Creds) ExecSpec {
			return ExecSpec{Cmd: []string{"sh", "-c", cli + ` --no-auth-warning DBSIZE | tr -dc 0-9`}, Env: map[string]string{"REDISCLI_AUTH": c.Password}}
		},
	}
}

// CredsFromURL recovers user/password/db from a connection URL produced by BuildURL.
func CredsFromURL(raw string) (Creds, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Creds{}, fmt.Errorf("invalid connection url")
	}
	c := Creds{DB: strings.TrimPrefix(u.Path, "/")}
	if u.User != nil {
		c.User = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	return c, nil
}
