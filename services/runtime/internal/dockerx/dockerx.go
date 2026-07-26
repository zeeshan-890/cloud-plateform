package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

// Start creates and starts a container, or simulates one.
func (e *Engine) Start(ctx context.Context, name, image string, port int) (containerID string, mode string, err error) {
	mode = e.EffectiveMode(ctx)
	if mode == "simulate" {
		id := "sim-" + strings.ReplaceAll(name, "/", "-")
		if len(id) > 64 {
			id = id[:64]
		}
		return id, mode, nil
	}
	body := map[string]any{
		"Image": image,
		"ExposedPorts": map[string]any{
			fmt.Sprintf("%d/tcp", port): map[string]any{},
		},
		"HostConfig": map[string]any{
			"RestartPolicy": map[string]any{"Name": "on-failure", "MaximumRetryCount": 3},
			"Memory":        128 * 1024 * 1024,
		},
		"Labels": map[string]string{
			"jp.managed": "true",
			"jp.name":    name,
		},
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/containers/create?name="+name, bytes.NewReader(raw))
	if err != nil {
		return "", mode, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return "", mode, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		// If image missing, still record simulate-style failure path for caller.
		return "", mode, fmt.Errorf("docker create %d: %s", resp.StatusCode, string(b))
	}
	var created struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(b, &created)
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/containers/"+created.ID+"/start", nil)
	if err != nil {
		return created.ID, mode, err
	}
	startResp, err := e.HTTP.Do(startReq)
	if err != nil {
		return created.ID, mode, err
	}
	defer startResp.Body.Close()
	if startResp.StatusCode >= 300 && startResp.StatusCode != 304 {
		sb, _ := io.ReadAll(startResp.Body)
		return created.ID, mode, fmt.Errorf("docker start %d: %s", startResp.StatusCode, string(sb))
	}
	return created.ID, mode, nil
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
