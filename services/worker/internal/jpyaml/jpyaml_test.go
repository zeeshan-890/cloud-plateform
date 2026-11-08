package jpyaml

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHonoursBuildAndSecurity(t *testing.T) {
	c, err := Parse([]byte("name: x\nruntime: nodejs\nbuild:\n  dockerfile: docker/Dockerfile\n  context: app\n  command: npm run b\n  env:\n    NODE_ENV: production\ndeploy:\n  port: 4000\nsecurity:\n  block_on_critical: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Dockerfile != "docker/Dockerfile" || c.Context != "app" || c.Port != 4000 || !c.BlockOnCritical || c.BuildEnv["NODE_ENV"] != "production" || len(c.JSON) == 0 {
		t.Fatalf("%+v", c)
	}
}

func TestParseRejectsEscapes(t *testing.T) {
	for _, y := range []string{"build:\n  dockerfile: ../../etc/passwd\n", "build:\n  context: /etc\n", "build:\n  env:\n    'A B': x\n", "deploy:\n  port: 99999\n", "build: ["} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%q should be rejected", y)
		}
	}
}

func TestLoadMissingAndResolveInside(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); err != nil || c.Found {
		t.Fatalf("%v %v", c, err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	if p, err := ResolveInside(dir, "app"); err != nil || p == "" {
		t.Fatalf("%v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err == nil {
		if _, err := ResolveInside(dir, "link"); err == nil {
			t.Fatal("symlink escape allowed")
		}
	}
	if _, err := ResolveInside(dir, "../x"); err == nil {
		t.Fatal("dotdot allowed")
	}
}
