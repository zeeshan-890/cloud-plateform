// Package scan runs the Trivy vulnerability scanner against a freshly built image.
// A scan that could not run is reported as "scan_unavailable" - never as "clean".
package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	StatusClean       = "clean"
	StatusVulnerable  = "vulnerabilities"
	StatusUnavailable = "scan_unavailable"
	StatusDisabled    = "disabled"

	MaxFindings = 20
	maxOutput   = 64 << 20
)

// Finding is one vulnerability.
type Finding struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	Installed string `json:"installed_version"`
	Fixed     string `json:"fixed_version,omitempty"`
	Severity  string `json:"severity"`
	Title     string `json:"title,omitempty"`
}

// Summary is stored on the build record.
type Summary struct {
	Scanner   string         `json:"scanner"`
	Status    string         `json:"status"`
	Reason    string         `json:"reason,omitempty"`
	Counts    map[string]int `json:"counts"`
	Critical  int            `json:"critical"`
	High      int            `json:"high"`
	Findings  []Finding      `json:"findings"`
	ScannedAt time.Time      `json:"scanned_at"`
}

// Runner executes a command and returns stdout.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Options configure Run.
type Options struct {
	Enabled  bool
	Binary   string // default "trivy"
	CacheDir string
	Timeout  time.Duration // default 5m
	Exec     Runner        // tests inject a fake
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &limited{b: &out, max: maxOutput}
	cmd.Stderr = &limited{b: &errb, max: 8 << 10}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return nil, fmt.Errorf("%v: %s", err, msg)
	}
	return out.Bytes(), nil
}

type limited struct {
	b   *bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.b.Len()+len(p) > l.max {
		return len(p), nil // drop the excess; the JSON parse will then fail loudly
	}
	return l.b.Write(p)
}

// Run scans imageRef.
func Run(ctx context.Context, imageRef string, o Options) *Summary {
	s := &Summary{Scanner: "trivy", Counts: map[string]int{}, Findings: []Finding{}, ScannedAt: time.Now().UTC()}
	if !o.Enabled {
		s.Status, s.Reason = StatusDisabled, "TRIVY_ENABLED is false"
		return s
	}
	if o.Binary == "" {
		o.Binary = "trivy"
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.Exec == nil {
		o.Exec = defaultExec
		if _, err := exec.LookPath(o.Binary); err != nil {
			s.Status, s.Reason = StatusUnavailable, "trivy binary not found"
			return s
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	args := []string{"image", "--quiet", "--format", "json", "--severity", "HIGH,CRITICAL", "--scanners", "vuln",
		"--timeout", o.Timeout.String(), "--no-progress"}
	if o.CacheDir != "" {
		args = append(args, "--cache-dir", o.CacheDir)
	}
	args = append(args, "--", imageRef)
	out, err := o.Exec(ctx, o.Binary, args...)
	if err != nil {
		s.Status, s.Reason = StatusUnavailable, truncate(err.Error(), 300)
		return s
	}
	if err := fill(s, out); err != nil {
		s.Status, s.Reason = StatusUnavailable, "cannot parse trivy output: "+truncate(err.Error(), 200)
		return s
	}
	return s
}

func rank(sev string) int {
	switch strings.ToUpper(sev) {
	case "CRITICAL":
		return 0
	case "HIGH":
		return 1
	}
	return 2
}

func fill(s *Summary, raw []byte) error {
	var rep struct {
		Results []struct {
			Vulnerabilities []struct {
				ID        string `json:"VulnerabilityID"`
				Pkg       string `json:"PkgName"`
				Installed string `json:"InstalledVersion"`
				Fixed     string `json:"FixedVersion"`
				Severity  string `json:"Severity"`
				Title     string `json:"Title"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return err
	}
	var all []Finding
	for _, r := range rep.Results {
		for _, v := range r.Vulnerabilities {
			sev := strings.ToUpper(v.Severity)
			s.Counts[sev]++
			all = append(all, Finding{ID: v.ID, Package: v.Pkg, Installed: v.Installed, Fixed: v.Fixed, Severity: sev, Title: truncate(v.Title, 160)})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if ri, rj := rank(all[i].Severity), rank(all[j].Severity); ri != rj {
			return ri < rj
		}
		return all[i].Fixed != "" && all[j].Fixed == ""
	})
	if len(all) > MaxFindings {
		all = all[:MaxFindings]
	}
	s.Findings = all
	s.Critical, s.High = s.Counts["CRITICAL"], s.Counts["HIGH"]
	if s.Critical+s.High == 0 {
		s.Status = StatusClean
	} else {
		s.Status = StatusVulnerable
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
