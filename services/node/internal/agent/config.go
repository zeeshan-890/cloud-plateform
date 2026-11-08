// Package agent implements the JP node agent: an authenticated, hardened
// reverse proxy to the local Docker daemon plus a registry port forwarder.
package agent

import (
	"fmt"
	"os"
	"strings"
)

// Config is the agent configuration.
type Config struct {
	Token            string
	Name             string
	Listen           string
	DockerSocket     string
	RegistryUpstream string
	RegistryListen   string
}

// LoadConfig reads configuration from the environment via getenv.
func LoadConfig(getenv func(string) string) (Config, error) {
	def := func(k, d string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return d
	}
	host, _ := os.Hostname()
	c := Config{
		Token:            strings.TrimSpace(getenv("NODE_TOKEN")),
		Name:             def("NODE_NAME", host),
		Listen:           def("NODE_LISTEN", "0.0.0.0:2377"),
		DockerSocket:     def("DOCKER_SOCKET", "/var/run/docker.sock"),
		RegistryUpstream: strings.TrimSpace(getenv("NODE_REGISTRY_UPSTREAM")),
		RegistryListen:   def("NODE_REGISTRY_LISTEN", "127.0.0.1:5000"),
	}
	if len(c.Token) < 32 {
		return c, fmt.Errorf("NODE_TOKEN is required and must be at least 32 characters")
	}
	return c, nil
}
