package scan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func fake(out string, err error, got *[]string) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		*got = append([]string{name}, args...)
		return []byte(out), err
	}
}

func TestSummaryCountsAndTop20(t *testing.T) {
	var vulns []string
	for i := 0; i < 30; i++ {
		sev := "HIGH"
		if i%10 == 0 {
			sev = "CRITICAL"
		}
		vulns = append(vulns, fmt.Sprintf(`{"VulnerabilityID":"CVE-%d","PkgName":"p","InstalledVersion":"1","Severity":"%s"}`, i, sev))
	}
	out := `{"Results":[{"Vulnerabilities":[` + strings.Join(vulns, ",") + `]}]}`
	var args []string
	s := Run(context.Background(), "127.0.0.1:5000/a/b:1", Options{Enabled: true, Exec: fake(out, nil, &args), CacheDir: "/cache"})
	if s.Status != StatusVulnerable || s.Critical != 3 || s.High != 27 || len(s.Findings) != 20 {
		t.Fatalf("%+v", s)
	}
	for i := 0; i < 3; i++ {
		if s.Findings[i].Severity != "CRITICAL" {
			t.Fatalf("criticals must come first: %+v", s.Findings[:4])
		}
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--severity HIGH,CRITICAL") || !strings.HasSuffix(joined, "-- 127.0.0.1:5000/a/b:1") || !strings.Contains(joined, "--cache-dir /cache") {
		t.Fatalf("args = %s", joined)
	}
}

func TestCleanOnlyWhenScanRanAndFoundNothing(t *testing.T) {
	var a []string
	if s := Run(context.Background(), "x/y:1", Options{Enabled: true, Exec: fake(`{"Results":[{"Vulnerabilities":null}]}`, nil, &a)}); s.Status != StatusClean {
		t.Fatalf("%+v", s)
	}
}

func TestFailuresAreNeverClean(t *testing.T) {
	var a []string
	for name, o := range map[string]Options{
		"exec error": {Enabled: true, Exec: fake("", errors.New("db download failed"), &a)},
		"bad json":   {Enabled: true, Exec: fake("<html>", nil, &a)},
		"no binary":  {Enabled: true, Binary: "definitely-not-trivy-xyz"},
	} {
		s := Run(context.Background(), "x/y:1", o)
		if s.Status != StatusUnavailable || s.Reason == "" || s.Status == StatusClean {
			t.Errorf("%s: %+v", name, s)
		}
	}
	if s := Run(context.Background(), "x/y:1", Options{Enabled: false}); s.Status != StatusDisabled {
		t.Fatalf("%+v", s)
	}
}
