package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/jp-cloud/runtime/internal/dockerx"
)

// ExecProber runs probes inside the prober sidecar (busybox wget/nc) so they originate from a container that is
// attached to the project networks, while the runtime service itself stays off every tenant network.
type ExecProber struct {
	Engine    *dockerx.Engine
	Container string
}

var httpStatusLine = regexp.MustCompile(`HTTP/[0-9.]+ ([0-9]{3})`)

// Probe implements Prober with `wget -S` (headers go to stderr).
func (p ExecProber) Probe(ctx context.Context, ip string, port int, path string) (int, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/"
	}
	u := "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + path
	_, stderr, _, err := p.Engine.Exec(ctx, p.Container, []string{"wget", "-S", "-q", "-O", "/dev/null", "-T", "3", "-U", "jp-runtime-healthcheck/1", u})
	if err != nil {
		return 0, err
	}
	m := httpStatusLine.FindAllStringSubmatch(stderr, -1)
	if len(m) == 0 {
		return 0, errors.New("probe: no HTTP response")
	}
	code, _ := strconv.Atoi(m[len(m)-1][1])
	return code, nil
}

// TCP implements the TCP readiness probe with `nc -z`.
func (p ExecProber) TCP(ctx context.Context, ip string, port int) error {
	_, _, exit, err := p.Engine.Exec(ctx, p.Container, []string{"nc", "-z", "-w", "3", ip, strconv.Itoa(port)})
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("tcp probe: connection to %s:%d failed", ip, port)
	}
	return nil
}
