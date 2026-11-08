// Package gitx fetches source code for a build safely: validated URLs (no option
// injection, no private-network SSRF), credentials only through GIT_ASKPASS / a deploy key
// with pinned host keys, bounded time and output, and exact-SHA verification.
package gitx

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed known_hosts_github
var knownHostsGitHub []byte

var (
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	shaRe    = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	scpRe    = regexp.MustCompile(`^git@([A-Za-z0-9.-]+):([A-Za-z0-9._-]+/[A-Za-z0-9._-]+?)(?:\.git)?$`)
)

// Options describe one checkout.
type Options struct {
	CloneURL string
	Branch   string
	SHA      string // "", "HEAD" or a (short) commit id
	PRNumber int    // > 0: fetch refs/pull/<n>/head
	Dir      string // empty directory to populate
	// Credentials: at most one.
	Token  string // https: sent via GIT_ASKPASS, never in the URL
	SSHKey string // PEM private key for git@github.com:owner/repo.git

	Timeout      time.Duration // default 3m
	MaxRepoBytes int64         // default 512 MiB
	Out          io.Writer     // receives (redacted by the caller's writer) git progress; may be nil
	AllowedHosts []string      // optional allow-list (exact host names)

	// Test-only knobs. They are set by tests, never by the worker's main.
	AllowFile bool
	Resolver  func(ctx context.Context, host string) ([]net.IP, error)
}

// Result of a successful checkout.
type Result struct {
	SHA string // full commit id of HEAD
}

// ValidBranch reports whether b is an acceptable branch name.
func ValidBranch(b string) bool {
	return branchRe.MatchString(b) && !strings.HasPrefix(b, "-") && !strings.Contains(b, "..") && !strings.HasSuffix(b, "/") && !strings.Contains(b, "//")
}

// ValidSHA reports whether s is a hex commit id (7-64 chars).
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// Kind of a validated URL.
type Kind string

const (
	KindHTTPS Kind = "https"
	KindSSH   Kind = "ssh"
	KindFile  Kind = "file"
)

func isPublic(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1]&0xC0 == 64: // 100.64.0.0/10 CGNAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0, v4[0] == 198 && (v4[1] == 18 || v4[1] == 19), v4[0] >= 240:
			return false
		}
	}
	return true
}

// ValidateURL checks a clone URL and (for network URLs) that its host resolves only to
// public addresses. It returns the URL kind and, for ssh, the host.
func ValidateURL(ctx context.Context, o Options) (Kind, string, error) {
	raw := o.CloneURL
	if raw == "" || len(raw) > 2048 {
		return "", "", errors.New("clone url empty or too long")
	}
	if strings.HasPrefix(raw, "-") {
		return "", "", errors.New("clone url must not start with '-'")
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return "", "", errors.New("clone url contains whitespace or control characters")
		}
	}
	if m := scpRe.FindStringSubmatch(raw); m != nil {
		if !strings.EqualFold(m[1], "github.com") {
			return "", "", errors.New("ssh clone is only supported for github.com (pinned host keys)")
		}
		return KindSSH, "github.com", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", errors.New("invalid clone url")
	}
	if u.Scheme == "file" || (u.Scheme == "" && filepath.IsAbs(raw)) {
		if !o.AllowFile {
			return "", "", errors.New("file clone urls are not allowed")
		}
		return KindFile, "", nil
	}
	if u.Scheme != "https" {
		return "", "", fmt.Errorf("clone url scheme %q not allowed (https or git@github.com: only)", u.Scheme)
	}
	if u.User != nil {
		return "", "", errors.New("clone url must not embed credentials")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasPrefix(host, "-") {
		return "", "", errors.New("clone url has no valid host")
	}
	if p := u.Port(); p != "" && p != "443" {
		return "", "", errors.New("clone url port not allowed")
	}
	if len(o.AllowedHosts) > 0 {
		ok := false
		for _, h := range o.AllowedHosts {
			if strings.EqualFold(h, host) {
				ok = true
			}
		}
		if !ok {
			return "", "", fmt.Errorf("host %q is not in the allowed git hosts", host)
		}
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolve := o.Resolver
		if resolve == nil {
			resolve = func(ctx context.Context, h string) ([]net.IP, error) {
				rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				addrs, err := net.DefaultResolver.LookupIPAddr(rctx, h)
				out := make([]net.IP, 0, len(addrs))
				for _, a := range addrs {
					out = append(out, a.IP)
				}
				return out, err
			}
		}
		if ips, err = resolve(ctx, host); err != nil || len(ips) == 0 {
			return "", "", fmt.Errorf("cannot resolve host %q", host)
		}
	}
	for _, ip := range ips {
		if !isPublic(ip) {
			return "", "", fmt.Errorf("host %q resolves to a non-public address", host)
		}
	}
	return KindHTTPS, host, nil
}

type limitedBuf struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuf) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func (o *Options) git(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = o.Dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	out := &limitedBuf{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	s := strings.TrimSpace(out.buf.String())
	if o.Out != nil && s != "" {
		fmt.Fprintln(o.Out, s)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return s, fmt.Errorf("git %s timed out", args[0])
		}
		return s, fmt.Errorf("git %s failed: %s", args[0], firstLines(s, 6))
	}
	return s, nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

// Clone populates o.Dir with the requested revision and verifies HEAD.
func Clone(ctx context.Context, o Options) (*Result, error) {
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Minute
	}
	if o.MaxRepoBytes <= 0 {
		o.MaxRepoBytes = 512 << 20
	}
	if o.Dir == "" {
		return nil, errors.New("no work dir")
	}
	if o.Branch != "" && !ValidBranch(o.Branch) {
		return nil, fmt.Errorf("invalid branch name")
	}
	sha := strings.TrimSpace(o.SHA)
	if sha == "HEAD" || strings.EqualFold(sha, "latest") {
		sha = ""
	}
	if sha != "" && !ValidSHA(sha) {
		return nil, fmt.Errorf("invalid commit sha")
	}
	if o.PRNumber < 0 {
		return nil, fmt.Errorf("invalid pull request number")
	}
	if o.Branch == "" && o.PRNumber == 0 && sha == "" {
		return nil, errors.New("no branch, pull request or sha to build")
	}
	if o.Token != "" && o.SSHKey != "" {
		return nil, errors.New("only one credential may be used")
	}
	kind, _, err := ValidateURL(ctx, o)
	if err != nil {
		return nil, err
	}
	if kind == KindSSH && o.SSHKey == "" {
		return nil, errors.New("ssh clone url requires a deploy key")
	}

	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	// private scratch dir for askpass / key / known_hosts / HOME (never inside the build context)
	scratch, err := os.MkdirTemp("", "jp-git-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)

	proto := "https:ssh"
	if o.AllowFile {
		proto = "https:ssh:file"
	}
	env := []string{
		"HOME=" + scratch, "LANG=C", "LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=" + proto,
	}
	for _, kv := range os.Environ() { // only PATH is inherited
		if strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	if o.Token != "" {
		askpass := filepath.Join(scratch, "askpass.sh")
		script := "#!/bin/sh\ncase \"$1\" in\n*sername*) echo x-access-token ;;\n*) printf '%s\\n' \"$JP_GIT_TOKEN\" ;;\nesac\n"
		if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
			return nil, err
		}
		env = append(env, "GIT_ASKPASS="+askpass, "JP_GIT_TOKEN="+o.Token, "SSH_ASKPASS=/bin/false")
	}
	if o.SSHKey != "" {
		keyFile := filepath.Join(scratch, "id_key")
		key := strings.TrimSpace(o.SSHKey) + "\n"
		if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
			return nil, err
		}
		kh := filepath.Join(scratch, "known_hosts")
		if err := os.WriteFile(kh, knownHostsGitHub, 0o600); err != nil {
			return nil, err
		}
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+keyFile+" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes"+
			" -o UserKnownHostsFile="+kh+" -o GlobalKnownHostsFile=/dev/null -o HostKeyAlgorithms=ssh-ed25519,ecdsa-sha2-nistp256 -o ConnectTimeout=20")
	}

	base := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "protocol.version=2",
		"-c", "http.lowSpeedLimit=1000", "-c", "http.lowSpeedTime=60", "-c", "advice.detachedHead=false"}
	if o.AllowFile {
		base = append(base, "-c", "protocol.file.allow=always")
	}
	run := func(args ...string) (string, error) {
		return o.git(ctx, env, append(append([]string{}, base...), args...)...)
	}

	if _, err := run("init", "-q"); err != nil {
		return nil, err
	}
	if _, err := run("remote", "add", "--", "origin", o.CloneURL); err != nil {
		return nil, err
	}
	var refspec string
	switch {
	case o.PRNumber > 0:
		refspec = fmt.Sprintf("+refs/pull/%d/head:refs/jp/target", o.PRNumber)
	case o.Branch != "":
		refspec = "+refs/heads/" + o.Branch + ":refs/jp/target"
	default:
		refspec = "+" + sha + ":refs/jp/target"
	}
	if _, err := run("fetch", "-q", "--depth=1", "--no-tags", "--no-recurse-submodules", "origin", "--", refspec); err != nil {
		if o.PRNumber > 0 {
			return nil, fmt.Errorf("cannot fetch pull request #%d: %w", o.PRNumber, err)
		}
		return nil, fmt.Errorf("cannot fetch branch %q: %w", o.Branch, err)
	}
	tip, err := run("rev-parse", "--verify", "refs/jp/target^{commit}")
	if err != nil {
		return nil, err
	}
	target := tip
	if sha != "" && !strings.HasPrefix(strings.ToLower(tip), strings.ToLower(sha)) {
		// the branch moved on (or the SHA is older): fetch that exact commit
		if _, err := run("fetch", "-q", "--depth=1", "--no-tags", "origin", "--", sha); err != nil {
			return nil, fmt.Errorf("commit %s is not available in the repository: %w", sha, err)
		}
		if target, err = run("rev-parse", "--verify", "FETCH_HEAD^{commit}"); err != nil {
			return nil, err
		}
	}
	if _, err := run("checkout", "-q", "--detach", "--force", target); err != nil {
		return nil, err
	}
	head, err := run("rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(head, target) || (sha != "" && !strings.HasPrefix(strings.ToLower(head), strings.ToLower(sha))) {
		return nil, fmt.Errorf("checked out %s but %s was requested", short(head), sha)
	}
	if err := checkSize(o.Dir, o.MaxRepoBytes); err != nil {
		return nil, err
	}
	return &Result{SHA: head}, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

var errTooBig = errors.New("repository exceeds the size limit")

func checkSize(dir string, max int64) error {
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
				if total > max {
					return errTooBig
				}
			}
		}
		return nil
	})
	if errors.Is(err, errTooBig) {
		return fmt.Errorf("repository exceeds the %d MiB limit", max>>20)
	}
	return err
}
