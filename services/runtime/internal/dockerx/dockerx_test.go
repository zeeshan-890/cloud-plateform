package dockerx

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidImageRef(t *testing.T) {
	good := []string{
		"nginx", "nginx:1.25", "library/nginx:latest", "registry:5000/abcd1234/efgh5678:deadbeef",
		"127.0.0.1:5000/a/b:v1.2.3", "ghcr.io/org/app:sha-abc", "registry.local/app@sha256:" + strings.Repeat("a", 64),
		"postgres:16-alpine",
	}
	for _, g := range good {
		if err := ValidImageRef(g); err != nil {
			t.Errorf("%q should be valid: %v", g, err)
		}
	}
	bad := []string{
		"", "-v", "--privileged", "a b", "a;rm -rf /", "a/b:", "a/b:-x", "$(id)", "a\nb", "A/B", "repo/UPPER", "a//b",
		"a/b:tag with space", "../../etc/passwd", "a/b@sha256:xyz", strings.Repeat("a", 300),
	}
	for _, b := range bad {
		if err := ValidImageRef(b); err == nil {
			t.Errorf("%q should be rejected", b)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, n := range []string{"jp-abc-123", "jp_p_12345678", "a.b-c_d"} {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range []string{"", "-x", "a/b", "a b", "a?force=true", "../x", "a%2Fb", strings.Repeat("a", 200)} {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestSpecValidateAndBody(t *testing.T) {
	ok := ContainerSpec{Name: "jp-x", Image: "a/b:1", Network: "jp_p_1", Env: []string{"A=1"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]ContainerSpec{
		"bad name":    {Name: "x/../y", Image: "a/b:1", Network: "n"},
		"bad image":   {Name: "x", Image: "--rm", Network: "n"},
		"bad env":     {Name: "x", Image: "a/b:1", Network: "n", Env: []string{"A B=1"}},
		"bad label":   {Name: "x", Image: "a/b:1", Network: "n", Labels: map[string]string{"a b": "1"}},
		"host volume": {Name: "x", Image: "a/b:1", Network: "n", Volume: &Volume{Name: "../etc", Target: "/data"}},
		"bad target":  {Name: "x", Image: "a/b:1", Network: "n", Volume: &Volume{Name: "v", Target: "/data:/host"}},
		"bad cap":     {Name: "x", Image: "a/b:1", Network: "n", ExtraCaps: []string{"sys admin"}},
	}
	for n, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", n)
		}
	}
	body := ok.CreateBody()
	host := body["HostConfig"].(map[string]any)
	if host["Privileged"] != false || host["PidsLimit"] != PidsLimit {
		t.Errorf("hardening missing: %v", host)
	}
	if _, has := host["Binds"]; has {
		t.Error("Binds must never be set")
	}
	auto := ContainerSpec{Name: "j", Image: "a/b:1", Network: "n", AutoRemove: true, RestartPolicy: "always"}.CreateBody()["HostConfig"].(map[string]any)
	if auto["AutoRemove"] != true || auto["RestartPolicy"].(map[string]any)["Name"] != "no" {
		t.Errorf("auto-remove containers must not restart: %v", auto["RestartPolicy"])
	}
}

// Requests built from hostile identifiers must be rejected before any HTTP call and
// legitimate identifiers must be path-escaped.
func TestEngineNeverInterpolatesRawInput(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	e := NewWithClient(srv.URL, srv.Client())
	ctx := context.Background()
	for _, evil := range []string{"x/../../etc", "x?force=true", "x y", "a/b", "../x", "x%2f..%2fy", ""} {
		if err := e.Start(ctx, evil); err == nil {
			t.Errorf("Start(%q) should be rejected", evil)
		}
		if _, err := e.Inspect(ctx, evil); err == nil {
			t.Errorf("Inspect(%q) should be rejected", evil)
		}
		if err := e.Remove(ctx, evil, true); err == nil {
			t.Errorf("Remove(%q) should be rejected", evil)
		}
		if _, err := e.Logs(ctx, evil, LogOptions{}); err == nil {
			t.Errorf("Logs(%q) should be rejected", evil)
		}
		if _, err := e.EnsureNetwork(ctx, evil, nil); err == nil {
			t.Errorf("EnsureNetwork(%q) should be rejected", evil)
		}
	}
	if _, err := e.InspectImage(ctx, "a/b:1; rm -rf /"); err == nil {
		t.Error("InspectImage must validate the reference")
	}
	if len(paths) != 0 {
		t.Fatalf("hostile input reached the daemon: %v", paths)
	}
	if err := e.Start(ctx, "good-container_1"); err != nil {
		t.Fatal(err)
	}
	if paths[0] != "POST /containers/good-container_1/start?" {
		t.Errorf("path = %s", paths[0])
	}
}

func TestInspectImageEscapesReference(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"Config":{"ExposedPorts":{"8080/tcp":{},"3000/tcp":{}},"Env":["PORT=1"]}}`))
	}))
	defer srv.Close()
	e := NewWithClient(srv.URL, srv.Client())
	info, err := e.InspectImage(context.Background(), "registry:5000/a/b:v1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/images/registry:5000%2Fa%2Fb:v1/json" && got != "/images/registry%3A5000%2Fa%2Fb%3Av1/json" {
		t.Errorf("escaped path = %s", got)
	}
	if len(info.ExposedPorts) != 2 || info.ExposedPorts[0] != 3000 {
		t.Errorf("ports = %v", info.ExposedPorts)
	}
}

func TestUnavailableIsAnErrorNotASimulation(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	e := NewWithClient(srv.URL, srv.Client())
	srv.Close()
	err := e.Ping(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.Create(context.Background(), ContainerSpec{Name: "x", Image: "a/b:1", Network: "n"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("create err = %v", err)
	}
	if e.Mode() != ModeDocker {
		t.Fatalf("mode = %s", e.Mode())
	}
}

func frame(stream byte, size uint32, payload string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], size)
	return append(h, payload...)
}

func TestReadFramesHostileSizes(t *testing.T) {
	// A frame claiming ~4 GiB with a tiny body must not allocate 4 GiB or hang.
	var buf bytes.Buffer
	buf.Write(frame(1, 0xFFFFFFF0, "2024-01-01T00:00:00Z hello\n"))
	var got []LogEvent
	err := ReadFrames(&buf, MaxLogBytes, func(ev LogEvent) bool { got = append(got, ev); return true })
	if err != nil {
		t.Fatal(err)
	}
	// payload shorter than declared => stream ends; nothing may panic or OOM.
	if len(got) > 1 {
		t.Fatalf("got %d events", len(got))
	}

	// Large-but-real frame is truncated to MaxFramePayload and the rest skipped.
	big := strings.Repeat("x", MaxFramePayload*3)
	buf.Reset()
	buf.Write(frame(1, uint32(len(big)), big))
	buf.Write(frame(2, 5, "boom\n"))
	got = nil
	if err := ReadFrames(&buf, 0, func(ev LogEvent) bool { got = append(got, ev); return true }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[0].Message) > MaxFramePayload || got[1].Message != "boom" || got[1].Level != "warn" {
		t.Fatalf("events = %d, first len=%d", len(got), len(got[0].Message))
	}

	// total byte cap stops reading
	buf.Reset()
	for i := 0; i < 10; i++ {
		buf.Write(frame(1, 6, "line\n\n"))
	}
	got = nil
	_ = ReadFrames(&buf, 18, func(ev LogEvent) bool { got = append(got, ev); return true })
	if len(got) != 3 {
		t.Fatalf("cap not honoured: %d events", len(got))
	}
}

func TestLogTimestampsAndLevels(t *testing.T) {
	ev := parseLine(2, "2024-05-01T10:00:00.123456789Z Error: failed to bind")
	if ev.LoggedAt != "2024-05-01T10:00:00.123456789Z" || ev.Message != "Error: failed to bind" || ev.Level != "error" {
		t.Fatalf("ev = %+v", ev)
	}
	if ev := parseLine(1, "no timestamp here"); ev.Level != "info" || ev.Message != "no timestamp here" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestLogsTailCapped(t *testing.T) {
	var q string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.RawQuery
		_, _ = w.Write(frame(1, 6, "hello\n"))
	}))
	defer srv.Close()
	e := NewWithClient(srv.URL, srv.Client())
	evs, err := e.Logs(context.Background(), "c1", LogOptions{Tail: 99999999})
	if err != nil || len(evs) != 1 {
		t.Fatalf("evs=%v err=%v", evs, err)
	}
	if !strings.Contains(q, "tail=2000") {
		t.Errorf("tail not capped: %s", q)
	}
}

func TestSimulateIsExplicit(t *testing.T) {
	e := New("simulate")
	if !e.Simulated() || e.Mode() != ModeSimulate {
		t.Fatal("simulate must be reported")
	}
	if d := New(""); d.Mode() != ModeDocker {
		t.Fatalf("default mode must be docker, got %s", d.Mode())
	}
}
