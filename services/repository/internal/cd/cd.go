// Package cd holds the continuous-deployment rules of a repository connection: which folder of a
// (mono)repo a project builds, which pushes should deploy it, and how changed files are matched.
// It is pure (no I/O) so every rule is unit tested.
//
// The model mirrors what people know from GitHub Actions workflows:
//
//	on.push.branches      -> DeployBranch (production) + BranchPreviews (every other branch)
//	on.push.paths         -> WatchPaths (+ the root directory itself)
//	on.push.paths-ignore  -> IgnorePaths
//	[skip ci] in message  -> SkipMessage
//	needs: ci             -> RequireCI (deploy after the repository's CI succeeded)
package cd

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const (
	maxRootDir  = 200
	maxPatterns = 20
	maxPattern  = 200
)

// Settings are the CD options of one connection (one project <-> one repository).
type Settings struct {
	RootDir        string
	AutoDeploy     bool
	DeployBranch   string // empty = the repository's default branch
	BranchPreviews bool
	PRPreviews     bool
	WatchPaths     []string
	IgnorePaths    []string
	RequireCI      bool
}

// Defaults are the settings of a freshly connected repository: the behaviour before CD settings existed.
func Defaults() Settings {
	return Settings{AutoDeploy: true, BranchPreviews: true, PRPreviews: true, WatchPaths: []string{}, IgnorePaths: []string{}}
}

var segRe = regexp.MustCompile(`^[A-Za-z0-9._@+ ()-]{1,100}$`)

// CleanRootDir validates and normalises a root directory ("backend", "apps/web"). "" and "." mean the repository root.
func CleanRootDir(in string) (string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(in, "\\", "/"))
	for strings.HasPrefix(s, "./") {
		s = strings.TrimPrefix(s, "./")
	}
	s = strings.Trim(s, "/")
	if s == "" || s == "." {
		return "", nil
	}
	if len(s) > maxRootDir {
		return "", fmt.Errorf("root directory is longer than %d characters", maxRootDir)
	}
	for _, seg := range strings.Split(s, "/") {
		switch {
		case seg == "" || seg == ".":
			return "", fmt.Errorf("root directory %q has an empty segment", in)
		case seg == "..":
			return "", fmt.Errorf("root directory must not contain '..'")
		case strings.EqualFold(seg, ".git"):
			return "", fmt.Errorf("root directory must not point into .git")
		case !segRe.MatchString(seg):
			return "", fmt.Errorf("root directory segment %q has unsupported characters", seg)
		}
	}
	return s, nil
}

// CleanPatterns validates a list of path globs (relative to the repository root).
func CleanPatterns(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if p == "" {
			continue
		}
		p = strings.TrimLeft(strings.TrimPrefix(p, "./"), "/") // "/backend/**" is read as repository-root relative
		if len(p) > maxPattern || strings.ContainsAny(p, "\x00\r\n") {
			return nil, fmt.Errorf("path pattern %q is invalid", raw)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return nil, fmt.Errorf("path pattern %q must not contain '..'", raw)
			}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
		if len(out) > maxPatterns {
			return nil, fmt.Errorf("at most %d path patterns are allowed", maxPatterns)
		}
	}
	return out, nil
}

func hasGlob(p string) bool { return strings.ContainsAny(p, "*?") }

// Match reports whether path (repo-relative, slash separated) matches pattern.
//
//	backend           the directory and everything below it
//	backend/**        the same
//	**/*.md           any .md file at any depth
//	docs/*.md         .md files directly in docs/
//	?                 one character except '/'
func Match(pattern, path string) bool {
	pattern = strings.Trim(pattern, "/")
	path = strings.Trim(path, "/")
	if pattern == "" {
		return true
	}
	if !hasGlob(pattern) {
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	re := compile(pattern)
	return re != nil && re.MatchString(path)
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*regexp.Regexp{}
)

func compile(pattern string) *regexp.Regexp {
	cacheMu.Lock()
	re, ok := cache[pattern]
	cacheMu.Unlock()
	if ok {
		return re
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			i += 2
			if i < len(pattern) && pattern[i] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
			continue
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
		i++
	}
	// a directory pattern such as "docs/*" also covers everything below the matched entry
	b.WriteString("(?:/.*)?$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		re = nil
	}
	cacheMu.Lock()
	if len(cache) > 512 {
		cache = map[string]*regexp.Regexp{}
	}
	cache[pattern] = re
	cacheMu.Unlock()
	return re
}

// Includes are the patterns a changed file must match to be relevant: the root directory plus WatchPaths.
// No includes at all means "everything is relevant".
func (s Settings) Includes() []string {
	var inc []string
	if s.RootDir != "" {
		inc = append(inc, s.RootDir)
	}
	return append(inc, s.WatchPaths...)
}

// Relevant reports whether a push that changed these files should deploy this connection.
// An unknown change set (nil: no commit list, or GitHub's 20 commit cap was reached) is always relevant,
// because skipping a deploy that was needed is worse than doing one that was not.
func (s Settings) Relevant(changed []string) bool {
	if changed == nil {
		return true
	}
	inc := s.Includes()
	for _, f := range changed {
		f = strings.TrimPrefix(strings.ReplaceAll(f, "\\", "/"), "./")
		if len(inc) > 0 && !matchAny(inc, f) {
			continue
		}
		if matchAny(s.IgnorePaths, f) {
			continue
		}
		return true
	}
	return false
}

func matchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if Match(p, path) {
			return true
		}
	}
	return false
}

var skipRe = regexp.MustCompile(`(?i)\[(?:skip[ -]?(?:ci|cd|deploy|deployment)|(?:ci|cd)[ -]?skip|no[ -]?deploy)\]|\*\*\*NO_CI\*\*\*`)

// SkipMessage reports whether a commit message asks not to deploy ("[skip ci]", "[skip deploy]", "[no deploy]", ...).
func SkipMessage(msg string) bool { return skipRe.MatchString(msg) }

// Branch returns the branch that deploys to production for this connection.
func (s Settings) Branch(defaultBranch string) string {
	if s.DeployBranch != "" {
		return s.DeployBranch
	}
	if defaultBranch != "" {
		return defaultBranch
	}
	return "main"
}

// Decision is what a push should do for one connection.
type Decision struct {
	Deploy  bool
	Preview bool   // deploy as a preview (not production)
	Wait    bool   // hold until CI succeeds
	Reason  string // why not / why waiting (stable machine-readable text)
}

// Decide applies every rule to a push of branch (message msg, changed files) and says what to do.
func (s Settings) Decide(branch, defaultBranch, msg string, changed []string) Decision {
	if SkipMessage(msg) {
		return Decision{Reason: "skipped by commit message"}
	}
	prod := strings.EqualFold(branch, s.Branch(defaultBranch))
	switch {
	case prod && !s.AutoDeploy:
		return Decision{Reason: "auto-deploy is off"}
	case !prod && !s.BranchPreviews:
		return Decision{Reason: "branch previews are off"}
	}
	if !s.Relevant(changed) {
		return Decision{Reason: "no changed file matches this project's folder or watch paths"}
	}
	if s.RequireCI {
		return Decision{Deploy: true, Preview: !prod, Wait: true, Reason: "waiting for CI"}
	}
	return Decision{Deploy: true, Preview: !prod}
}
