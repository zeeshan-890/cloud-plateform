package agent

import (
	"fmt"
	"regexp"
	"strings"
)

var versionPrefix = regexp.MustCompile(`^/v1\.[0-9]+(/|$)`)

// stripVersion removes an optional /v1.NN prefix and the leading slash.
func stripVersion(p string) string {
	p = p[len(versionPrefix.FindString(p)):]
	return strings.TrimPrefix(p, "/")
}

var allowExact = map[string]bool{"_ping": true, "version": true, "info": true, "events": true, "system/df": true, "volumes": true, "networks": true}
var allowPrefix = []string{"containers/", "images/", "networks/", "volumes/", "exec/"}

// pathAllowed reports whether the (version-stripped, cleaned) path may be proxied.
func pathAllowed(p string) bool {
	if allowExact[p] {
		return true
	}
	for _, pre := range allowPrefix {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

type createBody struct {
	HostConfig struct {
		Privileged  bool     `json:"Privileged"`
		NetworkMode string   `json:"NetworkMode"`
		PidMode     string   `json:"PidMode"`
		IpcMode     string   `json:"IpcMode"`
		UsernsMode  string   `json:"UsernsMode"`
		UTSMode     string   `json:"UTSMode"`
		Binds       []string `json:"Binds"`
		Mounts      []struct {
			Type   string `json:"Type"`
			Source string `json:"Source"`
		} `json:"Mounts"`
		Devices     []any    `json:"Devices"`
		CapAdd      []string `json:"CapAdd"`
		SecurityOpt []string `json:"SecurityOpt"`
	} `json:"HostConfig"`
}

func hostPath(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '/' || s[0] == 92 || s[0] == '.' || s[0] == '~' {
		return true
	}
	return len(s) >= 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'))
}

// checkCreate returns a non-nil error describing why a container create body is refused.
func checkCreate(cb *createBody) error {
	h := &cb.HostConfig
	if h.Privileged {
		return fmt.Errorf("privileged containers are not allowed")
	}
	nm := strings.ToLower(h.NetworkMode)
	if nm == "host" || strings.HasPrefix(nm, "container:") {
		return fmt.Errorf("NetworkMode %q is not allowed", h.NetworkMode)
	}
	for name, v := range map[string]string{"PidMode": h.PidMode, "IpcMode": h.IpcMode, "UsernsMode": h.UsernsMode, "UTSMode": h.UTSMode} {
		if strings.EqualFold(v, "host") {
			return fmt.Errorf("%s=host is not allowed", name)
		}
	}
	for _, b := range h.Binds {
		src, _, found := strings.Cut(b, ":")
		if found && hostPath(src) {
			return fmt.Errorf("bind mount of host path %q is not allowed", src)
		}
	}
	for _, m := range h.Mounts {
		t := strings.ToLower(m.Type)
		if t == "bind" || t == "npipe" || hostPath(m.Source) {
			return fmt.Errorf("host bind mounts are not allowed (source %q)", m.Source)
		}
	}
	if len(h.Devices) > 0 {
		return fmt.Errorf("host devices are not allowed")
	}
	for _, c := range h.CapAdd {
		switch strings.TrimPrefix(strings.ToUpper(c), "CAP_") {
		case "SYS_ADMIN", "SYS_PTRACE", "NET_ADMIN", "ALL":
			return fmt.Errorf("capability %q is not allowed", c)
		}
	}
	for _, s := range h.SecurityOpt {
		if strings.Contains(strings.ToLower(s), "unconfined") {
			return fmt.Errorf("security option %q is not allowed", s)
		}
	}
	return nil
}
