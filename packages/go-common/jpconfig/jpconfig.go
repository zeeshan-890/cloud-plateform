package jpconfig

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Manifest is the parsed jp.yaml desired state.
type Manifest struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Runtime     string            `json:"runtime" yaml:"runtime"`
	Build       *Build            `json:"build,omitempty" yaml:"build,omitempty"`
	Deploy      *Deploy           `json:"deploy,omitempty" yaml:"deploy,omitempty"`
	Domains     []string          `json:"domains,omitempty" yaml:"domains,omitempty"`
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

type Build struct {
	Command    string `json:"command,omitempty" yaml:"command,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	Context    string `json:"context,omitempty" yaml:"context,omitempty"`
	Output     string `json:"output,omitempty" yaml:"output,omitempty"`
}

type Deploy struct {
	Region      string            `json:"region,omitempty" yaml:"region,omitempty"`
	Replicas    int               `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	Port        int               `json:"port,omitempty" yaml:"port,omitempty"`
	Strategy    string            `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	Healthcheck string            `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

var allowedRuntimes = map[string]bool{
	"nodejs": true, "python": true, "go": true, "docker": true, "static": true,
	"node22": true, "node": true,
}

// Validate checks the manifest against packages/jp-schema rules.
func Validate(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	name := strings.TrimSpace(strings.ToLower(m.Name))
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 63 {
		return fmt.Errorf("name max length is 63")
	}
	if !validSlug(name) {
		return fmt.Errorf("name must be a lowercase slug (a-z0-9-)")
	}
	m.Name = name

	rt := strings.TrimSpace(strings.ToLower(m.Runtime))
	if rt == "" {
		return fmt.Errorf("runtime is required")
	}
	if !allowedRuntimes[rt] {
		return fmt.Errorf("runtime must be one of nodejs|python|go|docker|static (got %q)", m.Runtime)
	}
	m.Runtime = normalizeRuntime(rt)

	if m.Deploy != nil {
		if m.Deploy.Replicas == 0 {
			m.Deploy.Replicas = 1
		}
		if m.Deploy.Replicas < 1 || m.Deploy.Replicas > 10 {
			return fmt.Errorf("deploy.replicas must be 1–10")
		}
		if m.Deploy.Port != 0 && (m.Deploy.Port < 1 || m.Deploy.Port > 65535) {
			return fmt.Errorf("deploy.port out of range")
		}
		strat := strings.TrimSpace(strings.ToLower(m.Deploy.Strategy))
		if strat == "" {
			strat = "rolling"
		}
		if strat != "rolling" && strat != "blue_green" {
			return fmt.Errorf("deploy.strategy must be rolling|blue_green")
		}
		m.Deploy.Strategy = strat
	}
	return nil
}

func normalizeRuntime(rt string) string {
	switch rt {
	case "node", "node22":
		return "nodejs"
	default:
		return rt
	}
}

func validSlug(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// Strategy returns deploy strategy or "rolling".
func (m *Manifest) Strategy() string {
	if m != nil && m.Deploy != nil && m.Deploy.Strategy != "" {
		return m.Deploy.Strategy
	}
	return "rolling"
}

// ToMap returns a JSON-friendly map for storage.
func (m *Manifest) ToMap() map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// DriftStub compares desired vs applied keys (shallow). Returns true when they differ.
func DriftStub(desired, applied map[string]any) (drift bool, details []string) {
	if applied == nil && desired == nil {
		return false, nil
	}
	if applied == nil {
		return true, []string{"no applied config"}
	}
	if desired == nil {
		return false, nil
	}
	keys := []string{"name", "runtime", "build", "deploy", "domains", "env"}
	for _, k := range keys {
		a, aok := applied[k]
		d, dok := desired[k]
		if !dok {
			continue
		}
		if !aok {
			drift = true
			details = append(details, k+": missing in applied")
			continue
		}
		ab, _ := json.Marshal(a)
		db, _ := json.Marshal(d)
		if string(ab) != string(db) {
			drift = true
			details = append(details, k+": differs")
		}
	}
	return drift, details
}
