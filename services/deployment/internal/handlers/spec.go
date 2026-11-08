package handlers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var memRe = regexp.MustCompile(`^(?i)(\d+(?:\.\d+)?)\s*(b|k|kb|ki|kib|m|mb|mi|mib|g|gb|gi|gib)?$`)

// parseMemoryMB converts "512", "512MB", "1g", "256Mi" into megabytes (0 = unset/invalid).
func parseMemoryMB(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		m := memRe.FindStringSubmatch(strings.TrimSpace(t))
		if m == nil {
			return 0
		}
		n, _ := strconv.ParseFloat(m[1], 64)
		switch strings.ToLower(m[2]) {
		case "g", "gb", "gi", "gib":
			n *= 1024
		case "k", "kb", "ki", "kib":
			n /= 1024
		case "b":
			n /= 1024 * 1024
		}
		return int(n)
	}
	return 0
}

func parseCPUs(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	}
	return 0
}

// SpecFromConfig derives the runtime spec from a deployment's jp.yaml snapshot:
// deploy.port/healthcheck/replicas/restart, resources.memory/cpus, env + deploy.env.
// Unknown or malformed values are ignored (the runtime clamps and validates again).
func SpecFromConfig(raw json.RawMessage) map[string]any {
	spec := map[string]any{}
	var cfg map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &cfg) != nil {
		return spec
	}
	env := map[string]string{}
	addEnv := func(v any) {
		if m, ok := v.(map[string]any); ok {
			for k, x := range m {
				env[k] = fmt.Sprint(x)
			}
		}
	}
	addEnv(cfg["env"])
	if d, ok := cfg["deploy"].(map[string]any); ok {
		if p, ok := d["port"].(float64); ok && p > 0 {
			spec["port"] = int(p)
		}
		if h, ok := d["healthcheck"].(string); ok && strings.HasPrefix(h, "/") {
			spec["health_path"] = h
		}
		if n, ok := d["health_timeout_seconds"].(float64); ok && n > 0 {
			spec["health_timeout_seconds"] = int(n)
		}
		if r, ok := d["replicas"].(float64); ok && r >= 1 {
			spec["replicas"] = int(r)
		}
		for _, k := range []string{"restart", "restart_policy"} {
			if s, ok := d[k].(string); ok && s != "" {
				spec["restart_policy"] = s
			}
		}
		addEnv(d["env"])
	}
	if r, ok := cfg["resources"].(map[string]any); ok {
		if mb := parseMemoryMB(r["memory"]); mb > 0 {
			spec["memory_mb"] = mb
		}
		for _, k := range []string{"cpus", "cpu"} {
			if c := parseCPUs(r[k]); c > 0 {
				spec["cpus"] = c
			}
		}
	}
	if len(env) > 0 {
		spec["env"] = env
	}
	return spec
}

// StrategyFromConfig returns deploy.strategy when set.
func StrategyFromConfig(raw json.RawMessage) string {
	var cfg struct {
		Deploy struct {
			Strategy string `json:"strategy"`
		} `json:"deploy"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	return cfg.Deploy.Strategy
}
