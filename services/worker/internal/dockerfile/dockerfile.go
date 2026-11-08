// Package dockerfile detects the project type and generates a sane Dockerfile when the
// repository has none. It fails (instead of building a placeholder image) when it cannot
// tell how to run the project.
package dockerfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Plan is what the builder writes into the build context.
type Plan struct {
	Framework  string
	Dockerfile string
	Files      map[string]string // extra files to create next to the Dockerfile
	Port       int
}

// Options carry jp.yaml hints.
type Options struct {
	Runtime      string // jp.yaml runtime: nodejs|python|go|static|docker
	Port         int    // jp.yaml deploy.port (0 = framework default)
	BuildCommand string // jp.yaml build.command
}

type pkgJSON struct {
	Name         string            `json:"name"`
	Main         string            `json:"main"`
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	DevDeps      map[string]string `json:"devDependencies"`
	Engines      map[string]string `json:"engines"`
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func (p *pkgJSON) has(dep string) bool {
	_, a := p.Dependencies[dep]
	_, b := p.DevDeps[dep]
	return a || b
}

var digits = regexp.MustCompile(`\d+`)

func nodeVersion(p *pkgJSON) string {
	if m := digits.FindString(p.Engines["node"]); m != "" {
		switch m {
		case "18", "20", "22":
			return m
		}
	}
	return "20"
}

// Detect inspects dir and returns the generation plan.
func Detect(dir string, o Options) (*Plan, error) {
	rt := strings.ToLower(o.Runtime)
	switch {
	case rt == "static":
		return staticPlan(dir, o, ""), nil
	case exists(dir, "package.json") && (rt == "" || rt == "nodejs" || rt == "node"):
		return nodePlan(dir, o)
	case exists(dir, "go.mod") && (rt == "" || rt == "go"):
		return goPlan(dir, o)
	case (exists(dir, "requirements.txt") || exists(dir, "pyproject.toml")) && (rt == "" || rt == "python"):
		return pythonPlan(dir, o)
	case exists(dir, "index.html"):
		return staticPlan(dir, o, ""), nil
	}
	return nil, errors.New("no Dockerfile found and the project type could not be detected " +
		"(supported: Node.js, Go, Python, static sites); add a Dockerfile or a jp.yaml with `runtime`")
}

func port(o Options, def int) int {
	if o.Port > 0 {
		return o.Port
	}
	return def
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const nginxConf = `server {
  listen 8080;
  server_name _;
  root /usr/share/nginx/html;
  index index.html;
  location / { try_files $uri $uri/ /index.html; }
}
`

func staticPlan(dir string, o Options, buildStage string) *Plan {
	p := &Plan{Framework: "static", Port: 8080, Files: map[string]string{"nginx.jp.conf": nginxConf}}
	if buildStage == "" {
		p.Dockerfile = "FROM nginxinc/nginx-unprivileged:alpine\nCOPY nginx.jp.conf /etc/nginx/conf.d/default.conf\nCOPY . /usr/share/nginx/html\nEXPOSE 8080\n"
	}
	return p
}

func nodePlan(dir string, o Options) (*Plan, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	var pk pkgJSON
	if err := json.Unmarshal(raw, &pk); err != nil {
		return nil, fmt.Errorf("package.json is not valid JSON: %w", err)
	}
	ver := nodeVersion(&pk)
	install := "RUN npm install --no-audit --no-fund"
	pre := ""
	switch {
	case exists(dir, "pnpm-lock.yaml"):
		pre = "RUN corepack enable\n"
		install = "RUN pnpm install --frozen-lockfile"
	case exists(dir, "yarn.lock"):
		pre = "RUN corepack enable\n"
		install = "RUN yarn install --frozen-lockfile"
	case exists(dir, "package-lock.json"):
		install = "RUN npm ci --no-audit --no-fund"
	}
	build := ""
	switch {
	case o.BuildCommand != "":
		build = "RUN " + o.BuildCommand + "\n"
	case pk.Scripts["build"] != "":
		build = "RUN npm run build\n"
	}

	isSPA := (pk.has("vite") || pk.has("react-scripts") || pk.has("@vue/cli-service")) && !pk.has("next") && !pk.has("express") && pk.Scripts["start"] == "" ||
		(pk.has("vite") && !pk.has("next") && !pk.has("express") && !pk.has("nuxt"))
	if isSPA || strings.EqualFold(o.Runtime, "static") {
		out := "dist"
		if pk.has("react-scripts") {
			out = "build"
		}
		fw := "react"
		if pk.has("vue") || pk.has("@vue/cli-service") {
			fw = "vue"
		}
		df := fmt.Sprintf("FROM node:%s-alpine AS builder\nWORKDIR /app\nCOPY package*.json yarn.lock* pnpm-lock.yaml* ./\n%s%s\nCOPY . .\n%s"+
			"FROM nginxinc/nginx-unprivileged:alpine\nCOPY nginx.jp.conf /etc/nginx/conf.d/default.conf\nCOPY --from=builder /app/%s /usr/share/nginx/html\nEXPOSE 8080\n",
			ver, pre, install, build, out)
		return &Plan{Framework: fw, Dockerfile: df, Port: 8080, Files: map[string]string{"nginx.jp.conf": nginxConf}}, nil
	}

	fw := "node"
	def := 3000
	switch {
	case pk.has("next"):
		fw = "next"
	case pk.has("express"):
		fw = "express"
		def = 3000
	case pk.has("fastify"):
		fw = "fastify"
	case pk.has("@nestjs/core"):
		fw = "nest"
	}
	pt := port(o, def)
	var cmd string
	switch {
	case pk.Scripts["start"] != "":
		cmd = `["npm","start"]`
	case pk.Main != "" && !strings.ContainsAny(pk.Main, `"\`+"\n"):
		cmd = `["node",` + quote(pk.Main) + `]`
	case exists(dir, "server.js"):
		cmd = `["node","server.js"]`
	case exists(dir, "index.js"):
		cmd = `["node","index.js"]`
	case exists(dir, "app.js"):
		cmd = `["node","app.js"]`
	default:
		return nil, errors.New("Node.js project has no `start` script, `main` entry or server.js/index.js/app.js; add a start script or a Dockerfile")
	}
	df := fmt.Sprintf("FROM node:%s-alpine\nWORKDIR /app\nCOPY package*.json yarn.lock* pnpm-lock.yaml* ./\n%s%s\nCOPY . .\n%s"+
		"ENV NODE_ENV=production PORT=%d HOST=0.0.0.0\nRUN chown -R node:node /app\nUSER node\nEXPOSE %d\nCMD %s\n",
		ver, pre, install, build, pt, pt, cmd)
	return &Plan{Framework: fw, Dockerfile: df, Port: pt}, nil
}

var goVer = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

func goPlan(dir string, o Options) (*Plan, error) {
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	ver := "1.22"
	if m := goVer.FindSubmatch(mod); m != nil {
		ver = string(m[1])
	}
	target := ""
	switch {
	case exists(dir, "main.go"):
		target = "."
	default:
		entries, _ := os.ReadDir(filepath.Join(dir, "cmd"))
		var names []string
		for _, e := range entries {
			if e.IsDir() && exists(filepath.Join(dir, "cmd"), filepath.Join(e.Name(), "main.go")) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, errors.New("Go project has no main.go in the root or cmd/<name>/main.go; add a Dockerfile")
		}
		target = "./cmd/" + names[0]
	}
	pt := port(o, 8080)
	df := fmt.Sprintf("FROM golang:%s-alpine AS builder\nWORKDIR /src\nCOPY go.mod go.sum* ./\nRUN go mod download\nCOPY . .\n"+
		"RUN CGO_ENABLED=0 go build -trimpath -ldflags=\"-s -w\" -o /out/app %s\n"+
		"FROM alpine:3.20\nRUN apk add --no-cache ca-certificates && adduser -D -u 10001 app\nCOPY --from=builder /out/app /app\n"+
		"ENV PORT=%d\nUSER app\nEXPOSE %d\nCMD [\"/app\"]\n", ver, target, pt, pt)
	return &Plan{Framework: "go", Dockerfile: df, Port: pt}, nil
}

var pyModule = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*(FastAPI|Flask)\(`)

func pythonPlan(dir string, o Options) (*Plan, error) {
	reqs := ""
	if b, err := os.ReadFile(filepath.Join(dir, "requirements.txt")); err == nil {
		reqs = strings.ToLower(string(b))
	}
	install := "RUN pip install --no-cache-dir -r requirements.txt\n"
	copyDeps := "COPY requirements.txt ./\n"
	if reqs == "" {
		install = "RUN pip install --no-cache-dir .\n"
		copyDeps = "COPY . .\n"
	}
	pt := port(o, 8000)
	var cmd, fw string
	for _, cand := range []string{"main.py", "app.py", "app/main.py", "server.py"} {
		b, err := os.ReadFile(filepath.Join(dir, cand))
		if err != nil {
			continue
		}
		module := strings.ReplaceAll(strings.TrimSuffix(cand, ".py"), "/", ".")
		if m := pyModule.FindSubmatch(b); m != nil {
			obj := string(m[1])
			if string(m[2]) == "FastAPI" {
				fw = "fastapi"
				if !strings.Contains(reqs, "uvicorn") {
					install += "RUN pip install --no-cache-dir uvicorn\n"
				}
				cmd = fmt.Sprintf(`["sh","-c","exec uvicorn %s:%s --host 0.0.0.0 --port ${PORT}"]`, module, obj)
			} else {
				fw = "flask"
				if !strings.Contains(reqs, "gunicorn") {
					install += "RUN pip install --no-cache-dir gunicorn\n"
				}
				cmd = fmt.Sprintf(`["sh","-c","exec gunicorn --bind 0.0.0.0:${PORT} %s:%s"]`, module, obj)
			}
			break
		}
		if fw == "" {
			fw, cmd = "python", fmt.Sprintf(`["python",%s]`, quote(cand))
		}
	}
	if cmd == "" {
		return nil, errors.New("Python project has no main.py/app.py/server.py entry; add a Dockerfile")
	}
	df := fmt.Sprintf("FROM python:3.12-slim\nWORKDIR /app\nENV PYTHONUNBUFFERED=1 PORT=%d\n%s%sCOPY . .\n"+
		"RUN useradd -u 10001 -m app && chown -R app /app\nUSER app\nEXPOSE %d\nCMD %s\n", pt, copyDeps, install, pt, cmd)
	return &Plan{Framework: fw, Dockerfile: df, Port: pt}, nil
}

// Write materialises the plan inside dir without overwriting existing files.
func (p *Plan) Write(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile.jp"), []byte(p.Dockerfile), 0o644); err != nil {
		return err
	}
	for name, body := range p.Files {
		if strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("invalid generated file %q", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if !exists(dir, ".dockerignore") {
		_ = os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte(".git\nnode_modules\n.env\n.env.*\n*.pem\n*.key\n"), 0o644)
	}
	return nil
}
