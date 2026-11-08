// Package redact strips secrets from text before it is sent to a third-party LLM (or echoed back).
package redact

import (
	"regexp"
	"strings"
)

const mask = "[REDACTED]"

type rule struct {
	re   *regexp.Regexp
	repl string
}

var rules = []rule{
	// PEM blocks
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), mask},
	// URLs carrying credentials: scheme://user:pass@host
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^\s/:@'"<>]+:[^\s/@'"<>]*@`), `${1}` + mask + `@`},
	// Authorization / cookie headers
	{regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|x-api-key|x-auth-token|x-internal-token|cookie|set-cookie)\b(\s*[:=]\s*)[^\r\n]+`), `${1}${2}` + mask},
	// bearer / basic tokens anywhere
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9\-._~+/]{8,}=*`), `${1} ` + mask},
	// JWTs
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]*`), mask},
	// well known token formats
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), mask},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`), mask},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), mask},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{30,}\b`), mask},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}\b`), mask},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}\b`), mask},
	// docker inspect style env entries: "NAME=value"
	{regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)=([^"]*)"`), `"${1}=` + mask + `"`},
	// key=value / key: value / "key": "value" where the key looks sensitive
	{regexp.MustCompile(`(?i)("?[A-Za-z0-9_.\-]*(?:pass(?:word|wd)?|secret|token|api[_\-]?key|apikey|private[_\-]?key|access[_\-]?key|credential|auth|dsn|connection[_\-]?string|database[_\-]?url|signing[_\-]?key|session)[A-Za-z0-9_.\-]*"?)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&}]+)`), `${1}${2}` + mask},
	// KEY=value lines of an env dump (upper snake case, 3+ chars)
	{regexp.MustCompile(`(?m)^(\s*(?:export\s+)?[A-Z][A-Z0-9_]{2,}=)[^\r\n]+$`), `${1}` + mask},
	// long hex / base64 blobs (hashes are fine, but 40+ char tokens usually are not)
	{regexp.MustCompile(`\b[A-Fa-f0-9]{40,}\b`), mask},
	{regexp.MustCompile(`\b[A-Za-z0-9+/_\-]{48,}={0,2}`), mask},
}

// Text redacts secrets from s.
func Text(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Truncate keeps the LAST n bytes (newest log lines) on a UTF-8 boundary.
func Truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut++
	}
	return "...[truncated]\n" + strings.TrimLeft(s[cut:], "\n")
}

// Prepare redacts and caps text in one step; the order matters (redact first so a secret cut in
// half by truncation cannot slip through).
func Prepare(s string, n int) string { return Truncate(Text(s), n) }
