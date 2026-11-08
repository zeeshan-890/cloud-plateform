package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef-token"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeDocker starts an HTTP server on a unix socket and returns the socket path.
func fakeDocker(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func newAgent(t *testing.T, docker http.Handler) *httptest.Server {
	t.Helper()
	sock := fakeDocker(t, docker)
	ts := httptest.NewServer(NewHandler(Config{Token: testToken, Name: "n1", DockerSocket: sock}, quiet))
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, body string, tok string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("X-Node-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLoadConfig(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if _, err := LoadConfig(get); err == nil {
		t.Fatal("expected error without token")
	}
	env["NODE_TOKEN"] = "short"
	if _, err := LoadConfig(get); err == nil {
		t.Fatal("expected error for short token")
	}
	env["NODE_TOKEN"] = testToken
	c, err := LoadConfig(get)
	if err != nil || c.Listen != "0.0.0.0:2377" || c.DockerSocket != "/var/run/docker.sock" || c.RegistryListen != "127.0.0.1:5000" || c.Name == "" {
		t.Fatalf("bad defaults: %+v %v", c, err)
	}
}

func TestTokenRequired(t *testing.T) {
	ts := newAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "OK") }))
	for _, tok := range []string{"", "wrong", testToken[:len(testToken)-1], testToken + "x"} {
		resp := do(t, "GET", ts.URL+"/_ping", "", tok)
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		resp.Body.Close()
		if resp.StatusCode != 401 || e.Error.Code != "unauthorized" || e.Error.Message == "" {
			t.Fatalf("tok %q: %d %+v", tok, resp.StatusCode, e)
		}
	}
	resp := do(t, "GET", ts.URL+"/_ping", "", testToken)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "OK" {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
	// health needs no token
	resp = do(t, "GET", ts.URL+"/node/health", "", "")
	b, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"status":"ok"`) {
		t.Fatalf("health: %d %s", resp.StatusCode, b)
	}
	// info needs token
	if r := do(t, "GET", ts.URL+"/node/info", "", ""); r.StatusCode != 401 {
		t.Fatalf("info without token: %d", r.StatusCode)
	}
}

func TestAllowList(t *testing.T) {
	var gotTok, gotPath string
	ts := newAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTok, gotPath = r.Header.Get("X-Node-Token"), r.URL.Path
		io.WriteString(w, "ok")
	}))
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/_ping", 200}, {"GET", "/version", 200}, {"GET", "/info", 200},
		{"GET", "/v1.43/info", 200}, {"GET", "/v1.41/containers/json", 200},
		{"POST", "/images/create?fromImage=x", 200}, {"GET", "/networks/", 200},
		{"GET", "/volumes", 200}, {"GET", "/networks", 200},
		{"GET", "/volumes/x", 200}, {"POST", "/exec/abc/start", 200}, {"GET", "/events", 200},
		{"GET", "/system/df", 200}, {"GET", "/v1.43/system/df", 200},
		{"POST", "/build", 403}, {"POST", "/v1.43/build", 403}, {"GET", "/swarm", 403},
		{"GET", "/plugins", 403}, {"GET", "/secrets", 403}, {"GET", "/configs", 403},
		{"GET", "/services", 403}, {"GET", "/nodes", 403}, {"GET", "/session", 403},
		{"POST", "/commit", 403}, {"POST", "/auth", 403}, {"GET", "/system/events", 403},
		{"GET", "/containers/../build", 403}, {"GET", "/", 403}, {"GET", "/v1.43", 403},
		{"GET", "/infox", 403}, {"GET", "/v2.0/info", 403},
	}
	for _, c := range cases {
		resp := do(t, c.method, ts.URL+c.path, "", testToken)
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s: got %d want %d", c.method, c.path, resp.StatusCode, c.want)
		}
	}
	// forwarded request carries no token
	do(t, "GET", ts.URL+"/version", "", testToken).Body.Close()
	if gotTok != "" || gotPath != "/version" {
		t.Fatalf("token leaked or path wrong: %q %q", gotTok, gotPath)
	}
}

func TestCreateHardening(t *testing.T) {
	var gotBody string
	var gotCount int
	ts := newAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotCount++
		w.WriteHeader(201)
		io.WriteString(w, `{"Id":"abc"}`)
	}))
	bad := map[string]string{
		"privileged":       `{"HostConfig":{"Privileged":true}}`,
		"host net":         `{"HostConfig":{"NetworkMode":"host"}}`,
		"container net":    `{"HostConfig":{"NetworkMode":"container:abc"}}`,
		"host pid":         `{"HostConfig":{"PidMode":"host"}}`,
		"host ipc":         `{"HostConfig":{"IpcMode":"host"}}`,
		"host userns":      `{"HostConfig":{"UsernsMode":"host"}}`,
		"host uts":         `{"HostConfig":{"UTSMode":"host"}}`,
		"bind abs":         `{"HostConfig":{"Binds":["/etc:/mnt:ro"]}}`,
		"bind sock":        `{"HostConfig":{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}}`,
		"mount bind":       `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/srv","Target":"/x"}]}}`,
		"mount abs source": `{"HostConfig":{"Mounts":[{"Type":"volume","Source":"/srv","Target":"/x"}]}}`,
		"devices":          `{"HostConfig":{"Devices":[{"PathOnHost":"/dev/sda","PathInContainer":"/dev/sda"}]}}`,
		"cap sys_admin":    `{"HostConfig":{"CapAdd":["SYS_ADMIN"]}}`,
		"cap sys_ptrace":   `{"HostConfig":{"CapAdd":["sys_ptrace"]}}`,
		"cap net_admin":    `{"HostConfig":{"CapAdd":["CAP_NET_ADMIN"]}}`,
		"cap all":          `{"HostConfig":{"CapAdd":["ALL"]}}`,
		"unconfined":       `{"HostConfig":{"SecurityOpt":["seccomp=unconfined"]}}`,
	}
	for name, body := range bad {
		resp := do(t, "POST", ts.URL+"/v1.43/containers/create?name=x", body, testToken)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 403 || !strings.Contains(string(b), `"forbidden"`) {
			t.Errorf("%s: got %d %s", name, resp.StatusCode, b)
		}
	}
	if gotCount != 0 {
		t.Fatalf("docker reached %d times for forbidden bodies", gotCount)
	}
	clean := `{"Image":"127.0.0.1:5000/o/p:1","Env":["A=B"],"HostConfig":{"NetworkMode":"jp-net","Binds":["data:/var/lib/x","/data"],"Mounts":[{"Type":"volume","Source":"v","Target":"/y"}],"CapAdd":["CHOWN"],"Memory":1024}}`
	resp := do(t, "POST", ts.URL+"/containers/create", clean, testToken)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 || string(b) != `{"Id":"abc"}` || gotBody != clean {
		t.Fatalf("clean: %d %s body=%s", resp.StatusCode, b, gotBody)
	}
	// oversized body
	big := `{"Image":"` + strings.Repeat("a", 1<<20) + `"}`
	resp = do(t, "POST", ts.URL+"/containers/create", big, testToken)
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("big: %d", resp.StatusCode)
	}
	resp = do(t, "POST", ts.URL+"/containers/create", `{nope`, testToken)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid json: %d", resp.StatusCode)
	}
}

func TestUpgradeHijack(t *testing.T) {
	var sawTok string
	ts := newAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTok = r.Header.Get("X-Node-Token")
		if !strings.EqualFold(r.Header.Get("Upgrade"), "tcp") {
			http.Error(w, "no upgrade", 400)
			return
		}
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		brw.Flush()
		line, _ := brw.ReadString('\n')
		brw.WriteString("echo:" + line)
		brw.Flush()
	}))
	c, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "POST /v1.43/containers/abc/attach?stream=1&stdin=1 HTTP/1.1\r\nHost: x\r\nX-Node-Token: "+testToken+"\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: 0\r\n\r\n")
	br := bufio.NewReader(c)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("status %q err %v", status, err)
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if l == "\r\n" {
			break
		}
	}
	io.WriteString(c, "hello\n")
	got, err := br.ReadString('\n')
	if err != nil || got != "echo:hello\n" {
		t.Fatalf("got %q err %v", got, err)
	}
	if sawTok != "" {
		t.Fatal("token forwarded to docker")
	}
}

func TestStreaming(t *testing.T) {
	release := make(chan struct{})
	ts := newAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		io.WriteString(w, "line1\n")
		fl.Flush()
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		io.WriteString(w, "line2\n")
	}))
	resp := do(t, "GET", ts.URL+"/containers/abc/logs?follow=1", "", testToken)
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	done := make(chan string, 1)
	go func() { l, _ := br.ReadString('\n'); done <- l }()
	select {
	case l := <-done:
		if l != "line1\n" {
			t.Fatalf("first chunk %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk not streamed before upstream finished")
	}
	close(release)
	l, _ := br.ReadString('\n')
	if l != "line2\n" {
		t.Fatalf("second chunk %q", l)
	}
}

func TestRegistryForwarder(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, io.TeeReader(c, io.Discard)) }()
		}
	}()
	fl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ForwardRegistry(ctx, fl, up.Addr().String(), quiet)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.Dial("tcp", fl.Addr().String())
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			io.WriteString(c, "ping\n")
			l, err := bufio.NewReader(c).ReadString('\n')
			if err != nil || l != "ping\n" {
				t.Errorf("got %q %v", l, err)
			}
		}()
	}
	wg.Wait()

	// Unreachable upstream: connection is closed.
	fl2, _ := net.Listen("tcp", "127.0.0.1:0")
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()
	go ForwardRegistry(ctx, fl2, deadAddr, quiet)
	c, err := net.Dial("tcp", fl2.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected closed connection")
	}
}

func TestNodeInfo(t *testing.T) {
	ts := newAgent(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"ServerVersion":"27.1.1","NCPU":4,"MemTotal":8589934592,"ContainersRunning":2,"Containers":5}`)
	}))
	resp := do(t, "GET", ts.URL+"/node/info", "", testToken)
	defer resp.Body.Close()
	var ni nodeInfo
	if err := json.NewDecoder(resp.Body).Decode(&ni); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || ni.Name != "n1" || ni.DockerVersion != "27.1.1" || ni.CPUs != 4 ||
		ni.MemoryTotalBytes != 8589934592 || ni.ContainersRun != 2 || ni.ContainersTotal != 5 || ni.PrivateAddrs == nil {
		t.Fatalf("bad info: %d %+v", resp.StatusCode, ni)
	}
}

func TestNodeInfoDockerDown(t *testing.T) {
	ts := httptest.NewServer(NewHandler(Config{Token: testToken, Name: "n2", DockerSocket: filepath.Join(os.TempDir(), "nope.sock")}, quiet))
	defer ts.Close()
	resp := do(t, "GET", ts.URL+"/node/info", "", testToken)
	defer resp.Body.Close()
	var ni nodeInfo
	json.NewDecoder(resp.Body).Decode(&ni)
	if resp.StatusCode != 200 || ni.Name != "n2" || ni.DockerError == "" {
		t.Fatalf("got %d %+v", resp.StatusCode, ni)
	}
}
