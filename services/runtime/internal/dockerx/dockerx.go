package dockerx

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Engine talks to the Docker Engine API over the unix socket (or simulates).
type Engine struct {
	Mode   string // simulate | docker
	Socket string
	HTTP   *http.Client
}

type ContainerInfo struct {
	ID     string `json:"Id"`
	Names  []string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

func New(mode string) *Engine {
	if mode == "" {
		mode = os.Getenv("RUNTIME_MODE")
	}
	if mode == "" {
		mode = "simulate"
	}
	sock := os.Getenv("DOCKER_HOST")
	if sock == "" {
		sock = "unix:///var/run/docker.sock"
	}
	e := &Engine{Mode: mode, Socket: sock}
	if mode == "docker" {
		e.HTTP = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					path := strings.TrimPrefix(sock, "unix://")
					return net.DialTimeout("unix", path, 3*time.Second)
				},
			},
		}
	}
	return e
}

func (e *Engine) Available(ctx context.Context) bool {
	if e.Mode != "docker" || e.HTTP == nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/_ping", nil)
	if err != nil {
		return false
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (e *Engine) EffectiveMode(ctx context.Context) string {
	if e.Mode == "docker" && e.Available(ctx) {
		return "docker"
	}
	return "simulate"
}

// Start creates and starts a container with defaults.
func (e *Engine) Start(ctx context.Context, name, image string, port int) (containerID string, mode string, err error) {
	cid, m, _, err := e.StartWithConfig(ctx, name, image, port, nil, nil)
	return cid, m, err
}

// StartWithConfig creates and starts a container with discovered port, environment variables, and Traefik SSL router labels.
func (e *Engine) StartWithConfig(ctx context.Context, name, image string, port int, envs []string, hostnames []string) (containerID string, mode string, detectedPort int, err error) {
	mode = e.EffectiveMode(ctx)
	detectedPort = port
	if mode == "simulate" {
		id := "sim-" + strings.ReplaceAll(name, "/", "-")
		if len(id) > 64 {
			id = id[:64]
		}
		if detectedPort <= 0 {
			detectedPort = 3000
		}
		return id, mode, detectedPort, nil
	}

	// Auto-detect listening port if unspecified
	if detectedPort <= 0 && mode == "docker" && e.HTTP != nil {
		inspectReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/images/"+image+"/json", nil)
		if inspectResp, err := e.HTTP.Do(inspectReq); err == nil && inspectResp.StatusCode == http.StatusOK {
			defer inspectResp.Body.Close()
			var imgData struct {
				Config struct {
					ExposedPorts map[string]any `json:"ExposedPorts"`
					Env          []string       `json:"Env"`
				} `json:"Config"`
			}
			if err := json.NewDecoder(inspectResp.Body).Decode(&imgData); err == nil {
				for pStr := range imgData.Config.ExposedPorts {
					parts := strings.Split(pStr, "/")
					if len(parts) > 0 {
						if pNum, err := strconv.Atoi(parts[0]); err == nil && pNum > 0 {
							detectedPort = pNum
							break
						}
					}
				}
				if detectedPort <= 0 {
					for _, envItem := range imgData.Config.Env {
						if strings.HasPrefix(envItem, "PORT=") {
							if pNum, err := strconv.Atoi(strings.TrimPrefix(envItem, "PORT=")); err == nil && pNum > 0 {
								detectedPort = pNum
								break
							}
						}
					}
				}
			}
		}
	}
	if detectedPort <= 0 {
		detectedPort = 3000
	}

	// Remove any existing container with the same name to prevent 409 Conflict
	rmReq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, "http://localhost/containers/"+name+"?force=true", nil)
	if rmResp, err := e.HTTP.Do(rmReq); err == nil {
		_ = rmResp.Body.Close()
	}

	// Build Traefik routing rules including all project subdomains and custom domains
	var publicRules []string
	for _, h := range hostnames {
		h = strings.TrimSpace(h)
		if h != "" && !strings.HasSuffix(h, ".localhost") && !strings.HasSuffix(h, ".local") {
			publicRules = append(publicRules, fmt.Sprintf("Host(`%s`)", h))
		}
	}

	var rules []string
	rules = append(rules, publicRules...)
	rules = append(rules, fmt.Sprintf("PathPrefix(`/app/%s`)", name))
	if len(publicRules) == 0 {
		rules = append(rules, fmt.Sprintf("Host(`%s.localhost`)", name))
	}
	rule := strings.Join(rules, " || ")

	containerEnvs := []string{
		fmt.Sprintf("PORT=%d", detectedPort),
		"NODE_ENV=production",
		"HOST=0.0.0.0",
	}
	containerEnvs = append(containerEnvs, envs...)

	labels := map[string]string{
		"jp.managed":                                                 "true",
		"jp.name":                                                    name,
		"traefik.enable":                                             "true",
		"traefik.docker.network":                                     "jp_jp",
		"traefik.http.routers." + name + ".rule":                     rule,
		"traefik.http.routers." + name + ".entrypoints":              "web,websecure",
		"traefik.http.routers." + name + ".tls":                      "true",
		"traefik.http.routers." + name + ".priority":                 "80",
		"traefik.http.services." + name + ".loadbalancer.server.port": fmt.Sprintf("%d", detectedPort),
	}
	if len(publicRules) > 0 {
		labels["traefik.http.routers."+name+".tls.certresolver"] = "letsencrypt"
	}

	body := map[string]any{
		"Image": image,
		"Env":   containerEnvs,
		"ExposedPorts": map[string]any{
			fmt.Sprintf("%d/tcp", detectedPort): map[string]any{},
		},
		"HostConfig": map[string]any{
			"RestartPolicy": map[string]any{"Name": "on-failure", "MaximumRetryCount": 3},
			"Memory":        256 * 1024 * 1024,
			"NetworkMode":   "jp_jp",
		},
		"Labels": labels,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/containers/create?name="+name, bytes.NewReader(raw))
	if err != nil {
		return "", mode, detectedPort, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return "", mode, detectedPort, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", mode, detectedPort, fmt.Errorf("docker create %d: %s", resp.StatusCode, string(b))
	}
	var created struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(b, &created)
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/containers/"+created.ID+"/start", nil)
	if err != nil {
		return created.ID, mode, detectedPort, err
	}
	startResp, err := e.HTTP.Do(startReq)
	if err != nil {
		return created.ID, mode, detectedPort, err
	}
	defer startResp.Body.Close()
	if startResp.StatusCode >= 300 && startResp.StatusCode != 304 {
		sb, _ := io.ReadAll(startResp.Body)
		return created.ID, mode, detectedPort, fmt.Errorf("docker start %d: %s", startResp.StatusCode, string(sb))
	}
	return created.ID, mode, detectedPort, nil
}

func (e *Engine) Stop(ctx context.Context, containerID string) (mode string, err error) {
	mode = e.EffectiveMode(ctx)
	if mode == "simulate" || strings.HasPrefix(containerID, "sim-") {
		return "simulate", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/containers/"+containerID+"/stop?t=5", nil)
	if err != nil {
		return mode, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return mode, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 304 && resp.StatusCode != 404 {
		b, _ := io.ReadAll(resp.Body)
		return mode, fmt.Errorf("docker stop %d: %s", resp.StatusCode, string(b))
	}
	return mode, nil
}

func (e *Engine) InspectState(ctx context.Context, containerID string) (running bool, status string, err error) {
	mode := e.EffectiveMode(ctx)
	if mode == "simulate" || strings.HasPrefix(containerID, "sim-") {
		if containerID == "" {
			return false, "missing", nil
		}
		return true, "running (simulated)", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/containers/"+containerID+"/json", nil)
	if err != nil {
		return false, "", err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return false, "not_found", nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("docker inspect %d: %s", resp.StatusCode, string(b))
	}
	var info struct {
		State struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false, "", err
	}
	return info.State.Running, info.State.Status, nil
}

func (e *Engine) List(ctx context.Context) ([]ContainerInfo, string, error) {
	mode := e.EffectiveMode(ctx)
	if mode == "simulate" {
		return []ContainerInfo{}, mode, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/containers/json?all=true&filters="+urlQuery(`{"label":["jp.managed=true"]}`), nil)
	if err != nil {
		return nil, mode, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, mode, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, mode, fmt.Errorf("docker list %d: %s", resp.StatusCode, string(b))
	}
	var list []ContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, mode, err
	}
	if list == nil {
		list = []ContainerInfo{}
	}
	return list, mode, nil
}

func urlQuery(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

// LogEvent represents an emitted log entry from a container.
type LogEvent struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Level    string `json:"level"`
	Message  string `json:"message"`
	LoggedAt string `json:"logged_at"`
}

// ContainerLogs streams and decodes Docker multiplexed logs from stdout and stderr.
func (e *Engine) ContainerLogs(ctx context.Context, containerID string, tail int) ([]LogEvent, error) {
	if tail <= 0 {
		tail = 200
	}
	mode := e.EffectiveMode(ctx)
	if mode == "simulate" || strings.HasPrefix(containerID, "sim-") {
		return []LogEvent{
			{
				ID:       "sim-log-1",
				Source:   "runtime",
				Level:    "info",
				Message:  "Simulated runtime container: " + containerID,
				LoggedAt: time.Now().UTC().Format(time.RFC3339),
			},
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://localhost/containers/%s/logs?stdout=1&stderr=1&tail=%d&timestamps=1", containerID, tail), nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("docker logs %d: %s", resp.StatusCode, string(b))
	}

	var entries []LogEvent
	headerBuf := make([]byte, 8)
	for {
		// Docker multiplexed format has an 8-byte header per frame
		_, err := io.ReadFull(resp.Body, headerBuf)
		if err != nil {
			break
		}
		streamType := headerBuf[0] // 1 = stdout, 2 = stderr
		size := binary.BigEndian.Uint32(headerBuf[4:8])
		payload := make([]byte, size)
		_, err = io.ReadFull(resp.Body, payload)
		if err != nil {
			break
		}

		rawLine := strings.TrimRight(string(payload), "\r\n")
		if rawLine == "" {
			continue
		}

		level := "info"
		if streamType == 2 {
			level = "warn"
			low := strings.ToLower(rawLine)
			if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "exception") {
				level = "error"
			}
		}

		loggedAt := time.Now().UTC().Format(time.RFC3339)
		msg := rawLine
		if idx := strings.Index(rawLine, " "); idx > 0 {
			tsCandidate := rawLine[:idx]
			if _, err := time.Parse(time.RFC3339Nano, tsCandidate); err == nil {
				loggedAt = tsCandidate
				msg = rawLine[idx+1:]
			}
		}

		entries = append(entries, LogEvent{
			ID:       fmt.Sprintf("log-%d", len(entries)+1),
			Source:   "app",
			Level:    level,
			Message:  msg,
			LoggedAt: loggedAt,
		})
	}

	if entries == nil {
		entries = []LogEvent{}
	}
	return entries, nil
}
