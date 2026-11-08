// Package jpyaml reads the repository's jp.yaml into the few settings the worker honours
// and a JSON snapshot that is stored on the deployment.
package jpyaml

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxFile = 64 << 10

// Config is the validated subset of jp.yaml used while building.
type Config struct {
	Found           bool
	Runtime         string
	Dockerfile      string // relative path inside the repo
	Context         string // relative directory inside the repo
	BuildCommand    string
	BuildEnv        map[string]string // plain, non-secret values (jp.yaml is public to repo readers)
	Port            int
	BlockOnCritical bool
	JSON            json.RawMessage // whole manifest as JSON (for the deployment record)
}

// Load reads jp.yaml / jp.yml from dir. A missing file is not an error.
func Load(dir string) (*Config, error) {
	for _, name := range []string{"jp.yaml", "jp.yml"} {
		p := filepath.Join(dir, name)
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("%s must be a regular file", name)
		}
		if fi.Size() > maxFile {
			return nil, fmt.Errorf("%s is larger than %d KiB", name, maxFile>>10)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		return Parse(raw)
	}
	return &Config{}, nil
}

// Parse validates raw YAML.
func Parse(raw []byte) (*Config, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("jp.yaml: %w", err)
	}
	c := &Config{Found: true, BuildEnv: map[string]string{}}
	norm := normalize(doc)
	root, _ := norm.(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	js, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	c.JSON = js
	c.Runtime, _ = root["runtime"].(string)
	if b, ok := root["build"].(map[string]any); ok {
		c.Dockerfile, _ = b["dockerfile"].(string)
		c.Context, _ = b["context"].(string)
		c.BuildCommand, _ = b["command"].(string)
		if env, ok := b["env"].(map[string]any); ok {
			for k, v := range env {
				c.BuildEnv[k] = fmt.Sprint(v)
			}
		}
	}
	if d, ok := root["deploy"].(map[string]any); ok {
		if p, ok := d["port"].(float64); ok {
			c.Port = int(p)
		}
	}
	if s, ok := root["security"].(map[string]any); ok {
		c.BlockOnCritical, _ = s["block_on_critical"].(bool)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// normalize converts yaml.v3's map[string]any / map[any]any trees into JSON-safe ones.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalize(x)
		}
		return t
	case map[any]any:
		m := map[string]any{}
		for k, x := range t {
			m[fmt.Sprint(k)] = normalize(x)
		}
		return m
	case []any:
		for i := range t {
			t[i] = normalize(t[i])
		}
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	}
	return v
}

// SafeRelPath validates a repo-relative path: no absolute paths, no "..", no NUL/newlines.
func SafeRelPath(p string) error {
	if p == "" {
		return nil
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") || strings.ContainsAny(p, "\x00\r\n") {
		return fmt.Errorf("path %q must be relative", p)
	}
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("path %q must not contain '..'", p)
		}
	}
	return nil
}

func (c *Config) validate() error {
	if err := SafeRelPath(c.Dockerfile); err != nil {
		return fmt.Errorf("build.dockerfile: %w", err)
	}
	if err := SafeRelPath(c.Context); err != nil {
		return fmt.Errorf("build.context: %w", err)
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("deploy.port out of range")
	}
	if strings.ContainsAny(c.BuildCommand, "\x00\r\n") {
		return fmt.Errorf("build.command must be a single line")
	}
	for k := range c.BuildEnv {
		if !validEnvName(k) {
			return fmt.Errorf("build.env: invalid name %q", k)
		}
	}
	return nil
}

func validEnvName(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for i, r := range k {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// ResolveInside joins rel under root and refuses anything that escapes it, including
// through symlinks.
func ResolveInside(root, rel string) (string, error) {
	if err := SafeRelPath(rel); err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(realRoot, realFull)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the repository", rel)
	}
	return realFull, nil
}
