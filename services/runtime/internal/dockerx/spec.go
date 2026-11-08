package dockerx

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Volume is a named volume mounted into the container (never a host path).
type Volume struct {
	Name   string
	Target string
	Labels map[string]string
}

// ContainerSpec is everything needed to create a tenant container. All hardening is
// applied by CreateBody; callers cannot opt out of it.
type ContainerSpec struct {
	Name     string
	Image    string
	Env      []string
	Labels   map[string]string
	Network  string
	Aliases  []string
	Port     int // exposed (informational) port, 0 for none
	Memory   int64
	NanoCPUs int64
	// RestartPolicy: "", "no", "on-failure", "always", "unless-stopped".
	RestartPolicy string
	MaxRetries    int
	Cmd           []string
	AutoRemove    bool
	Volume        *Volume
	// ExtraCaps are added on top of NET_BIND_SERVICE (used for stateful add-on images
	// that must chown their data dir and drop privileges).
	ExtraCaps []string
	// PublishIP, when set, publishes Port on that host IP with a daemon-chosen host port (apps on remote nodes are
	// reached by the control plane at <node private ip>:<host port>). Empty = nothing is published.
	PublishIP string
	// PublishPort is the host port to publish on (required with PublishIP): a fixed port survives container restarts,
	// a daemon-chosen one does not.
	PublishPort int
}

const (
	DefaultMemory   = 512 << 20
	DefaultNanoCPUs = 1_000_000_000
	PidsLimit       = 512
)

// Validate rejects anything that must never reach the daemon.
func (s ContainerSpec) Validate() error {
	if !ValidName(s.Name) {
		return fmt.Errorf("invalid container name %q", truncate(s.Name, 60))
	}
	if err := ValidImageRef(s.Image); err != nil {
		return err
	}
	if !ValidName(s.Network) {
		return fmt.Errorf("invalid network name")
	}
	for _, e := range s.Env {
		i := strings.IndexByte(e, '=')
		if i <= 0 || !ValidEnvKey(e[:i]) {
			return fmt.Errorf("invalid environment variable name")
		}
		if strings.ContainsRune(e, 0) {
			return fmt.Errorf("environment value contains NUL")
		}
	}
	if s.PublishIP != "" && net.ParseIP(s.PublishIP) == nil {
		return fmt.Errorf("invalid publish address")
	}
	if s.PublishPort < 0 || s.PublishPort > 65535 {
		return fmt.Errorf("invalid publish port")
	}
	for k := range s.Labels {
		if !labelKey.MatchString(k) {
			return fmt.Errorf("invalid label key %q", truncate(k, 60))
		}
	}
	for _, a := range s.Aliases {
		if !ValidName(a) {
			return fmt.Errorf("invalid network alias %q", truncate(a, 60))
		}
	}
	if s.Volume != nil {
		if !ValidName(s.Volume.Name) || !strings.HasPrefix(s.Volume.Target, "/") ||
			strings.Contains(s.Volume.Target, "..") || strings.ContainsAny(s.Volume.Target, ":,\x00") {
			return fmt.Errorf("invalid volume mount")
		}
	}
	for _, c := range s.ExtraCaps {
		if !validCap(c) {
			return fmt.Errorf("invalid capability %q", c)
		}
	}
	return nil
}

func validCap(c string) bool {
	if c == "" || len(c) > 32 {
		return false
	}
	for _, r := range c {
		if (r < 'A' || r > 'Z') && r != '_' {
			return false
		}
	}
	return true
}

// CreateBody renders the Docker create payload with the mandatory hardening.
func (s ContainerSpec) CreateBody() map[string]any {
	mem := s.Memory
	if mem <= 0 {
		mem = DefaultMemory
	}
	cpu := s.NanoCPUs
	if cpu <= 0 {
		cpu = DefaultNanoCPUs
	}
	restart := map[string]any{"Name": "no"}
	switch s.RestartPolicy {
	case "on-failure":
		n := s.MaxRetries
		if n <= 0 {
			n = 5
		}
		restart = map[string]any{"Name": "on-failure", "MaximumRetryCount": n}
	case "always", "unless-stopped":
		restart = map[string]any{"Name": "unless-stopped"}
	}
	if s.AutoRemove {
		restart = map[string]any{"Name": "no"}
	}
	caps := append([]string{"NET_BIND_SERVICE"}, s.ExtraCaps...)
	host := map[string]any{
		"NetworkMode":     s.Network,
		"Privileged":      false,
		"CapDrop":         []string{"ALL"},
		"CapAdd":          caps,
		"SecurityOpt":     []string{"no-new-privileges:true"},
		"PidsLimit":       PidsLimit,
		"Memory":          mem,
		"MemorySwap":      mem, // no swap
		"NanoCpus":        cpu,
		"ReadonlyRootfs":  false,
		"Tmpfs":           map[string]string{"/tmp": "rw,nosuid,size=128m"},
		"RestartPolicy":   restart,
		"AutoRemove":      s.AutoRemove,
		"PublishAllPorts": false,
		"LogConfig": map[string]any{
			"Type":   "json-file",
			"Config": map[string]string{"max-size": "10m", "max-file": "3"},
		},
		"Init": true,
	}
	if s.Volume != nil {
		vol := map[string]any{"Type": "volume", "Source": s.Volume.Name, "Target": s.Volume.Target}
		if len(s.Volume.Labels) > 0 {
			vol["VolumeOptions"] = map[string]any{"Labels": s.Volume.Labels}
		}
		host["Mounts"] = []any{vol}
	}
	if s.PublishIP != "" && s.Port > 0 {
		host["PortBindings"] = map[string]any{fmt.Sprintf("%d/tcp", s.Port): []any{map[string]any{"HostIp": s.PublishIP, "HostPort": hostPortStr(s.PublishPort)}}}
	}
	endpoint := map[string]any{}
	if len(s.Aliases) > 0 {
		endpoint["Aliases"] = s.Aliases
	}
	body := map[string]any{
		"Image":      s.Image,
		"Env":        s.Env,
		"Labels":     s.Labels,
		"HostConfig": host,
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{s.Network: endpoint},
		},
	}
	if s.Port > 0 {
		body["ExposedPorts"] = map[string]any{fmt.Sprintf("%d/tcp", s.Port): map[string]any{}}
	}
	if len(s.Cmd) > 0 {
		body["Cmd"] = s.Cmd
	}
	return body
}

func hostPortStr(p int) string {
	if p <= 0 {
		return ""
	}
	return strconv.Itoa(p)
}
