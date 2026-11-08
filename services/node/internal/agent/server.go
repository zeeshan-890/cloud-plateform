package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strconv"
	"sync"
	"time"
)

const (
	tokenHeader   = "X-Node-Token"
	maxCreateBody = 1 << 20
)

type server struct {
	cfg    Config
	log    *slog.Logger
	tr     *http.Transport
	proxy  *httputil.ReverseProxy
	tokSum [32]byte
}

// NewHandler builds the agent HTTP handler.
func NewHandler(cfg Config, log *slog.Logger) http.Handler {
	s := &server{cfg: cfg, log: log, tokSum: sha256.Sum256([]byte(cfg.Token))}
	s.tr = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", cfg.DockerSocket)
		},
		DisableCompression:  true,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     60 * time.Second,
	}
	s.proxy = &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "docker"
			r.Host = "docker"
			r.Header.Del(tokenHeader)
		},
		Transport:     s.tr,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("docker proxy error", "path", r.URL.Path, "err", err)
			writeErr(w, http.StatusBadGateway, "bad_gateway", "docker daemon unreachable")
		},
	}
	return s
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (s *server) authed(r *http.Request) bool {
	sum := sha256.Sum256([]byte(r.Header.Get(tokenHeader)))
	return subtle.ConstantTimeCompare(sum[:], s.tokSum[:]) == 1
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/node/health" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"status\":\"ok\"}\n")
		return
	}
	if !s.authed(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid node token")
		return
	}
	if r.URL.Path == "/node/info" {
		s.info(w, r)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	p := stripVersion(clean)
	if !pathAllowed(p) {
		s.log.Warn("path denied", "method", r.Method, "path", clean)
		writeErr(w, http.StatusForbidden, "forbidden", "this docker API endpoint is not allowed on a node")
		return
	}
	r.URL.Path, r.URL.RawPath = clean, ""
	if r.Method == http.MethodPost && p == "containers/create" {
		if !s.checkCreateReq(w, r) {
			return
		}
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *server) checkCreateReq(w http.ResponseWriter, r *http.Request) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxCreateBody+1))
	_ = r.Body.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read request body")
		return false
	}
	if len(data) > maxCreateBody {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "container create body exceeds 1 MiB")
		return false
	}
	var cb createBody
	if err := json.Unmarshal(data, &cb); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	if err := checkCreate(&cb); err != nil {
		s.log.Warn("container create denied", "reason", err.Error())
		writeErr(w, http.StatusForbidden, "forbidden", err.Error())
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.Header.Set("Content-Length", strconv.Itoa(len(data)))
	r.Header.Del("Transfer-Encoding")
	r.TransferEncoding = nil
	return true
}

type nodeInfo struct {
	Name             string   `json:"name"`
	DockerVersion    string   `json:"docker_version"`
	CPUs             int      `json:"cpus"`
	MemoryTotalBytes int64    `json:"memory_total_bytes"`
	ContainersRun    int      `json:"containers_running"`
	ContainersTotal  int      `json:"containers_total"`
	PrivateAddrs     []string `json:"private_addrs"`
	DockerError      string   `json:"docker_error,omitempty"`
}

func (s *server) info(w http.ResponseWriter, r *http.Request) {
	ni := nodeInfo{Name: s.cfg.Name, PrivateAddrs: privateAddrs()}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/info", nil)
	resp, err := s.tr.RoundTrip(req)
	if err != nil {
		ni.DockerError = err.Error()
	} else {
		defer resp.Body.Close()
		var d struct {
			ServerVersion     string `json:"ServerVersion"`
			NCPU              int    `json:"NCPU"`
			MemTotal          int64  `json:"MemTotal"`
			ContainersRunning int    `json:"ContainersRunning"`
			Containers        int    `json:"Containers"`
		}
		if resp.StatusCode != 200 {
			ni.DockerError = "docker /info returned " + resp.Status
		} else if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&d); err != nil {
			ni.DockerError = "decode docker info: " + err.Error()
		} else {
			ni.DockerVersion, ni.CPUs, ni.MemoryTotalBytes = d.ServerVersion, d.NCPU, d.MemTotal
			ni.ContainersRun, ni.ContainersTotal = d.ContainersRunning, d.Containers
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ni)
}

func privateAddrs() []string {
	out := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
			out = append(out, ip4.String())
		}
	}
	return out
}

// LimitListener bounds the number of concurrently open accepted connections.
func LimitListener(l net.Listener, n int) net.Listener {
	return &limitListener{Listener: l, sem: make(chan struct{}, n)}
}

type limitListener struct {
	net.Listener
	sem chan struct{}
}

func (l *limitListener) Accept() (net.Conn, error) {
	l.sem <- struct{}{}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{Conn: c, release: sync.OnceFunc(func() { <-l.sem })}, nil
}

type limitConn struct {
	net.Conn
	release func()
}

func (c *limitConn) Close() error { defer c.release(); return c.Conn.Close() }
