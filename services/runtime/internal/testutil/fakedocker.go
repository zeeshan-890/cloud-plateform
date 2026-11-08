// Package testutil provides a fake Docker Engine (httptest) and an in-memory instance
// repository for the runtime tests. It is never imported by production code.
package testutil

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/runtime/internal/dockerx"
)

// FakeContainer is the daemon-side state of one container.
type FakeContainer struct {
	ID       string
	Name     string
	Body     map[string]any
	Running  bool
	Removed  bool
	IP       string
	Created  time.Time
	Image    string
	Labels   map[string]string
	ExitCode int
	Logs     []byte // raw multiplexed frames
}

// FakeDocker is a minimal Docker Engine API implementation.
type FakeDocker struct {
	Srv *httptest.Server

	mu           sync.Mutex
	Containers   map[string]*FakeContainer // by id
	Networks     map[string][]string       // name -> connected containers (names or ids)
	NetLabels    map[string]map[string]string
	Volumes      map[string]map[string]string
	Events       []string // ordered "action target" records
	Requests     []string // "METHOD path?query"
	ExposedPorts []int
	// Tokens are the distinct X-Node-Token header values seen (remote-node tests).
	Tokens []string
	// StartErrors are returned (one per start request, oldest first) before starts succeed again.
	StartErrors []string
	nextIP      int
	nextID      int
	// CrashImages: images whose containers exit immediately.
	CrashImages map[string]bool
	// ExitCodeForJob is returned by wait.
	ExitCodeForJob int
	execStdin      bool
}

// NewFakeDocker starts the fake.
func NewFakeDocker() *FakeDocker {
	f := &FakeDocker{
		Containers: map[string]*FakeContainer{}, Networks: map[string][]string{}, NetLabels: map[string]map[string]string{},
		Volumes: map[string]map[string]string{}, CrashImages: map[string]bool{}, ExposedPorts: []int{8080}, nextIP: 1,
	}
	f.Srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Engine returns a docker-mode engine bound to the fake.
func (f *FakeDocker) Engine() *dockerx.Engine {
	return dockerx.NewWithClient(f.Srv.URL, f.Srv.Client())
}

// Close stops the server.
func (f *FakeDocker) Close() { f.Srv.Close() }

func (f *FakeDocker) record(ev string) { f.Events = append(f.Events, ev) }

// EventsSnapshot returns a copy of the event log.
func (f *FakeDocker) EventsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Events...)
}

// Create returns the container body recorded for name.
func (f *FakeDocker) ByName(name string) *FakeContainer {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Containers {
		if c.Name == name && !c.Removed {
			return c
		}
	}
	return nil
}

// Live returns non-removed containers sorted by name.
func (f *FakeDocker) Live() []*FakeContainer {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*FakeContainer
	for _, c := range f.Containers {
		if !c.Removed {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StatusForIP resolves the container by IP (used by fake probers).
func (f *FakeDocker) ByIP(ip string) *FakeContainer {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Containers {
		if c.IP == ip && !c.Removed {
			return c
		}
	}
	return nil
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func msg(w http.ResponseWriter, code int, m string) {
	jsonOut(w, code, map[string]string{"message": m})
}

func (f *FakeDocker) find(ref string) *FakeContainer {
	if c, ok := f.Containers[ref]; ok && !c.Removed {
		return c
	}
	for _, c := range f.Containers {
		if !c.Removed && (c.Name == ref || strings.HasPrefix(c.ID, ref)) {
			return c
		}
	}
	return nil
}

// wait blocks until the container was started (like the real daemon), then reports the
// exit code and removes it (AutoRemove).
func (f *FakeDocker) wait(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	f.mu.Lock()
	c := f.find(parts[1])
	f.Requests = append(f.Requests, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if c == nil {
		msg(w, 404, "no such container")
		return
	}
	w.WriteHeader(200)
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	for i := 0; i < 400; i++ {
		f.mu.Lock()
		started := c.Running || c.ExitCode != 0
		f.mu.Unlock()
		if started {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.mu.Lock()
	code := f.ExitCodeForJob
	f.record("wait " + c.Name)
	c.Running, c.Removed = false, true
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"StatusCode": code})
}

// execStart hijacks the connection like the real daemon: it echoes stdin to stdout (prefixed)
// and writes one stderr frame.
func (f *FakeDocker) execStart(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(500)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	if strings.EqualFold(r.Header.Get("Upgrade"), "tcp") {
		_, _ = io.WriteString(conn, "HTTP/1.1 101 UPGRADED"+crlf+"Content-Type: application/vnd.docker.raw-stream"+crlf+"Connection: Upgrade"+crlf+"Upgrade: tcp"+crlf+crlf)
	} else {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK"+crlf+"Content-Type: application/vnd.docker.raw-stream"+crlf+"Connection: close"+crlf+crlf)
	}
	_, _ = io.CopyN(io.Discard, buf.Reader, r.ContentLength) // the JSON start request body
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	in, _ := io.ReadAll(io.LimitReader(buf.Reader, 1<<20))
	_, _ = conn.Write(Frame(1, "out:"+string(in)))
	_, _ = conn.Write(Frame(2, "warn"))
}

func (f *FakeDocker) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/start") {
		f.execStart(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/wait") {
		f.wait(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	if t := r.Header.Get("X-Node-Token"); t != "" {
		seen := false
		for _, x := range f.Tokens {
			seen = seen || x == t
		}
		if !seen {
			f.Tokens = append(f.Tokens, t)
		}
	}
	f.Requests = append(f.Requests, r.Method+" "+r.URL.RequestURI())
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/_ping":
		_, _ = io.WriteString(w, "OK")
	case path == "/info":
		jsonOut(w, 200, map[string]any{"ServerVersion": "29.0-fake", "NCPU": 2, "MemTotal": 11 << 30, "Containers": len(f.Containers), "ContainersRunning": 1, "Images": 3})
	case parts[0] == "images" && len(parts) >= 3 && parts[len(parts)-1] == "json":
		ports := map[string]any{}
		for _, p := range f.ExposedPorts {
			ports[fmt.Sprintf("%d/tcp", p)] = map[string]any{}
		}
		jsonOut(w, 200, map[string]any{"Config": map[string]any{"ExposedPorts": ports, "Env": []string{"PATH=/bin"}}})
	case path == "/images/create":
		jsonOut(w, 200, map[string]any{"status": "ok"})
	case path == "/images/prune":
		jsonOut(w, 200, map[string]any{"ImagesDeleted": []any{1, 2}, "SpaceReclaimed": 100})
	case path == "/containers/prune":
		jsonOut(w, 200, map[string]any{"ContainersDeleted": []string{"a"}, "SpaceReclaimed": 50})
	case path == "/networks/prune":
		jsonOut(w, 200, map[string]any{"NetworksDeleted": []string{"n"}})
	case path == "/networks/create" && r.Method == "POST":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		name, _ := b["Name"].(string)
		if _, ok := f.Networks[name]; ok {
			msg(w, 409, "network exists")
			return
		}
		f.Networks[name] = nil
		lbl := map[string]string{}
		if m, ok := b["Labels"].(map[string]any); ok {
			for k, v := range m {
				lbl[k], _ = v.(string)
			}
		}
		f.NetLabels[name] = lbl
		f.record("network.create " + name)
		jsonOut(w, 201, map[string]string{"Id": "net-" + name})
	case parts[0] == "networks" && len(parts) == 2 && r.Method == "GET":
		if _, ok := f.Networks[parts[1]]; !ok {
			msg(w, 404, "no such network")
			return
		}
		jsonOut(w, 200, map[string]string{"Id": "net-" + parts[1]})
	case parts[0] == "networks" && len(parts) == 2 && r.Method == "DELETE":
		if _, ok := f.Networks[parts[1]]; !ok {
			msg(w, 404, "no such network")
			return
		}
		delete(f.Networks, parts[1])
		f.record("network.remove " + parts[1])
		w.WriteHeader(204)
	case parts[0] == "networks" && len(parts) == 3 && parts[2] == "connect":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		c, _ := b["Container"].(string)
		for _, e := range f.Networks[parts[1]] {
			if e == c {
				msg(w, 403, "endpoint with name "+c+" already exists in network "+parts[1])
				return
			}
		}
		f.Networks[parts[1]] = append(f.Networks[parts[1]], c)
		f.record("network.connect " + parts[1] + " " + c)
		w.WriteHeader(200)
	case parts[0] == "networks" && len(parts) == 3 && parts[2] == "disconnect":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		c, _ := b["Container"].(string)
		list := f.Networks[parts[1]]
		out := list[:0]
		for _, e := range list {
			if e != c {
				out = append(out, e)
			}
		}
		f.Networks[parts[1]] = out
		f.record("network.disconnect " + parts[1] + " " + c)
		w.WriteHeader(200)
	case path == "/volumes" && r.Method == "GET":
		var flt map[string][]string
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &flt)
		vols := []map[string]string{}
		for name, labels := range f.Volumes {
			if matchLabels(labels, flt["label"]) {
				vols = append(vols, map[string]string{"Name": name})
			}
		}
		jsonOut(w, 200, map[string]any{"Volumes": vols})
	case parts[0] == "volumes" && len(parts) == 2 && r.Method == "DELETE":
		delete(f.Volumes, parts[1])
		f.record("volume.remove " + parts[1])
		w.WriteHeader(204)
	case path == "/containers/json":
		var flt map[string][]string
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &flt)
		out := []map[string]any{}
		for _, c := range f.Containers {
			if c.Removed || !matchLabels(c.Labels, flt["label"]) {
				continue
			}
			state := "exited"
			if c.Running {
				state = "running"
			}
			out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}, "Image": c.Image, "State": state, "Status": state, "Labels": c.Labels, "Created": c.Created.Unix()})
		}
		jsonOut(w, 200, out)
	case path == "/containers/create" && r.Method == "POST":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		name := r.URL.Query().Get("name")
		for _, c := range f.Containers {
			if !c.Removed && c.Name == name {
				msg(w, 409, "name in use")
				return
			}
		}
		f.nextID++
		f.nextIP++
		id := fmt.Sprintf("c%03d%s", f.nextID, strings.Repeat("0", 8))
		img, _ := b["Image"].(string)
		lbl := map[string]string{}
		if m, ok := b["Labels"].(map[string]any); ok {
			for k, v := range m {
				lbl[k], _ = v.(string)
			}
		}
		c := &FakeContainer{ID: id, Name: name, Body: b, IP: fmt.Sprintf("10.9.0.%d", f.nextIP), Created: time.Now().Add(-time.Hour), Image: img, Labels: lbl}
		f.Containers[id] = c
		f.record("create " + name)
		jsonOut(w, 201, map[string]string{"Id": id})
	case parts[0] == "containers" && len(parts) == 3 && parts[2] == "exec":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.execStdin, _ = b["AttachStdin"].(bool)
		jsonOut(w, 201, map[string]string{"Id": "exec1"})
	case parts[0] == "exec" && len(parts) == 3 && parts[2] == "json":
		code := 0
		if f.execStdin {
			code = 7 // streamed execs report 7 so tests can see the trailer
		}
		jsonOut(w, 200, map[string]any{"ExitCode": code, "Running": false})
	case parts[0] == "containers" && len(parts) == 3:
		c := f.find(parts[1])
		if c == nil {
			msg(w, 404, "no such container")
			return
		}
		switch parts[2] {
		case "start":
			if len(f.StartErrors) > 0 {
				m := f.StartErrors[0]
				f.StartErrors = f.StartErrors[1:]
				msg(w, 500, m)
				return
			}
			if f.CrashImages[c.Image] {
				c.Running = false
				c.ExitCode = 1
			} else {
				c.Running = true
			}
			f.record("start " + c.Name)
			w.WriteHeader(204)
		case "stop":
			c.Running = false
			f.record("stop " + c.Name)
			w.WriteHeader(204)
		case "restart":
			c.Running = true
			f.record("restart " + c.Name)
			w.WriteHeader(204)
		case "json":
			net := ""
			ports := map[string]any{}
			if hc, ok := c.Body["HostConfig"].(map[string]any); ok {
				net, _ = hc["NetworkMode"].(string)
				if pb, ok := hc["PortBindings"].(map[string]any); ok {
					n := 0
					for k, v := range pb { // the daemon picks the host port: 32768 + a per-container counter
						binds, _ := v.([]any)
						out := []any{}
						for _, b := range binds {
							bm, _ := b.(map[string]any)
							hp, _ := bm["HostPort"].(string)
							if hp == "" {
								hp = fmt.Sprint(32768 + f.hostPortOffset(c.ID) + n)
							}
							out = append(out, map[string]any{"HostIp": bm["HostIp"], "HostPort": hp})
						}
						ports[k] = out
						n++
					}
				}
			}
			jsonOut(w, 200, map[string]any{
				"Id":              c.ID,
				"State":           map[string]any{"Running": c.Running, "Status": map[bool]string{true: "running", false: "exited"}[c.Running], "ExitCode": c.ExitCode, "StartedAt": time.Now().Format(time.RFC3339Nano)},
				"Config":          map[string]any{"Labels": c.Labels},
				"NetworkSettings": map[string]any{"Networks": map[string]any{net: map[string]any{"IPAddress": c.IP}}, "Ports": ports},
			})
		case "stats":
			jsonOut(w, 200, map[string]any{
				"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": 2000000}, "system_cpu_usage": 100000000, "online_cpus": 2},
				"precpu_stats": map[string]any{"cpu_usage": map[string]any{"total_usage": 1000000}, "system_cpu_usage": 50000000},
				"memory_stats": map[string]any{"usage": 100 << 20, "stats": map[string]any{"inactive_file": 20 << 20}},
			})
		case "logs":
			w.WriteHeader(200)
			_, _ = w.Write(c.Logs)
		default:
			msg(w, 404, "unknown")
		}
	case parts[0] == "containers" && len(parts) == 2 && r.Method == "DELETE":
		c := f.find(parts[1])
		if c == nil {
			msg(w, 404, "no such container")
			return
		}
		c.Removed, c.Running = true, false
		f.record("remove " + c.Name)
		w.WriteHeader(204)
	default:
		msg(w, 404, "not implemented: "+r.Method+" "+path)
	}
}

func matchLabels(labels map[string]string, want []string) bool {
	for _, w := range want {
		k, v, has := strings.Cut(w, "=")
		got, ok := labels[k]
		if !ok || (has && got != v) {
			return false
		}
	}
	return true
}

// Frame builds one multiplexed docker log frame.
func Frame(stream byte, payload string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

// RequestsSnapshot copies the raw request log.
func (f *FakeDocker) RequestsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Requests...)
}

// Query decodes a recorded request's query string.
func Query(req string) url.Values {
	_, q, _ := strings.Cut(req, "?")
	v, _ := url.ParseQuery(q)
	return v
}

// Record appends an external event (e.g. a route switch) to the ordered event log.
func (f *FakeDocker) Record(ev string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(ev)
}

// Mu exposes the fake's mutex for tests that inspect Containers directly.
func (f *FakeDocker) Mu() *sync.Mutex { return &f.mu }

// ByNameLocked finds a live container by name; the caller must hold Mu().
func (f *FakeDocker) ByNameLocked(name string) *FakeContainer {
	for _, c := range f.Containers {
		if c.Name == name && !c.Removed {
			return c
		}
	}
	return nil
}

const crlf = "\r\n"

// hostPortOffset gives every container its own host-port block (deterministic, caller holds f.mu).
func (f *FakeDocker) hostPortOffset(id string) int {
	i := 0
	for cid := range f.Containers {
		if cid < id {
			i++
		}
	}
	return i * 10
}
