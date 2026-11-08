package dockerfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for n, c := range files {
		p := filepath.Join(dir, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestNodeWithStartScriptAndLockfile(t *testing.T) {
	dir := write(t, map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"14"},"engines":{"node":">=22"}}`, "package-lock.json": "{}"})
	p, err := Detect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM node:22-alpine", "npm ci", "npm run build", `CMD ["npm","start"]`, "USER node", "PORT=3000"} {
		if !strings.Contains(p.Dockerfile, want) {
			t.Errorf("missing %q in:\n%s", want, p.Dockerfile)
		}
	}
	if p.Framework != "next" || p.Port != 3000 {
		t.Errorf("plan = %+v", p)
	}
}

func TestPortFromJPYamlAndBuildCommand(t *testing.T) {
	dir := write(t, map[string]string{"package.json": `{"main":"srv.js","dependencies":{"express":"4"}}`})
	p, err := Detect(dir, Options{Port: 4100, BuildCommand: "npm run compile"})
	if err != nil || !strings.Contains(p.Dockerfile, "PORT=4100") || !strings.Contains(p.Dockerfile, "RUN npm run compile") || !strings.Contains(p.Dockerfile, `["node","srv.js"]`) {
		t.Fatalf("%v\n%v", err, p)
	}
}

func TestViteSPAUsesUnprivilegedNginx(t *testing.T) {
	dir := write(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5"},"dependencies":{"react":"18"}}`})
	p, err := Detect(dir, Options{})
	if err != nil || !strings.Contains(p.Dockerfile, "nginx-unprivileged") || !strings.Contains(p.Dockerfile, "/app/dist") || p.Port != 8080 || p.Files["nginx.jp.conf"] == "" {
		t.Fatalf("%v\n%v", err, p)
	}
}

func TestGoAndPythonAndStatic(t *testing.T) {
	g := write(t, map[string]string{"go.mod": "module x\n\ngo 1.23\n", "cmd/api/main.go": "package main"})
	p, err := Detect(g, Options{})
	if err != nil || !strings.Contains(p.Dockerfile, "golang:1.23-alpine") || !strings.Contains(p.Dockerfile, "./cmd/api") {
		t.Fatalf("%v %v", err, p)
	}
	py := write(t, map[string]string{"requirements.txt": "fastapi\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"})
	p, err = Detect(py, Options{})
	if err != nil || p.Framework != "fastapi" || !strings.Contains(p.Dockerfile, "uvicorn main:app") || !strings.Contains(p.Dockerfile, "pip install --no-cache-dir uvicorn") {
		t.Fatalf("%v %v", err, p)
	}
	s := write(t, map[string]string{"index.html": "<html>"})
	if p, err = Detect(s, Options{}); err != nil || p.Framework != "static" {
		t.Fatalf("%v %v", err, p)
	}
}

func TestUndetectableFailsInsteadOfPlaceholder(t *testing.T) {
	if _, err := Detect(write(t, map[string]string{"README.md": "hi"}), Options{}); err == nil {
		t.Fatal("must fail, not generate a placeholder image")
	}
	if _, err := Detect(write(t, map[string]string{"package.json": `{"name":"x"}`}), Options{}); err == nil {
		t.Fatal("node project without a start entry must fail")
	}
}

func TestWriteDoesNotClobberAndAddsDockerignore(t *testing.T) {
	dir := write(t, map[string]string{".dockerignore": "custom\n"})
	p := &Plan{Dockerfile: "FROM x\n", Files: map[string]string{"a.conf": "x"}}
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".dockerignore")); string(b) != "custom\n" {
		t.Fatal(".dockerignore overwritten")
	}
	if err := (&Plan{Files: map[string]string{"../evil": "x"}}).Write(dir); err == nil {
		t.Fatal("path traversal in generated files")
	}
}
