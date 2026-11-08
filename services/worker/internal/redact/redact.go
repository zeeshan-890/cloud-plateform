// Package redact scrubs credentials from every line the worker logs, stores or returns.
package redact

import (
	"io"
	"regexp"
	"strings"
	"sync"
)

const mask = "***"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(https?|ssh|git)://[^/\s:@]+:[^@\s/]+@`),                           // scheme://user:pass@
	regexp.MustCompile(`(?i)(https?|ssh|git)://[A-Za-z0-9_\-.]{20,}@`),                         // scheme://token@
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),                                       // GitHub tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),                                     // fine-grained PAT
	regexp.MustCompile(`(?i)x-access-token:[^@\s]+`),                                           // app installation token form
	regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic|token)\s+)[A-Za-z0-9._~+/=\-]+`), // auth headers
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)(["']?\s*[:=]\s*["']?)[^\s"',;]{8,}`),
}

// Redactor replaces known secrets and credential-shaped strings.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// New creates a redactor that always masks the given literal secrets.
func New(secrets ...string) *Redactor {
	r := &Redactor{}
	r.Add(secrets...)
	return r
}

// Add registers more literal secrets (ignored when shorter than 4 chars).
func (r *Redactor) Add(secrets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range secrets {
		if len(strings.TrimSpace(s)) >= 4 {
			r.secrets = append(r.secrets, s)
		}
	}
}

// String returns s with every secret masked.
func (r *Redactor) String(s string) string {
	if r != nil {
		r.mu.RLock()
		for _, sec := range r.secrets {
			s = strings.ReplaceAll(s, sec, mask)
		}
		r.mu.RUnlock()
	}
	for i, p := range patterns {
		switch i {
		case 0, 1:
			s = p.ReplaceAllStringFunc(s, func(m string) string { return m[:strings.Index(m, "://")+3] + mask + "@" })
		case 5:
			s = p.ReplaceAllString(s, "${1}"+mask)
		case 7:
			s = p.ReplaceAllString(s, "${1}${2}"+mask)
		default:
			s = p.ReplaceAllString(s, mask)
		}
	}
	return s
}

// Writer wraps w so everything written is redacted line by line (a partial line is held
// until its newline arrives or Flush is called, so a secret split across writes is caught).
func (r *Redactor) Writer(w io.Writer) *LineWriter { return &LineWriter{r: r, w: w} }

// LineWriter is an io.WriteCloser that redacts complete lines.
type LineWriter struct {
	r   *Redactor
	w   io.Writer
	buf []byte
}

const maxLine = 64 << 10

// Write implements io.Writer. It always reports len(p) consumed.
func (l *LineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := strings.IndexByte(string(l.buf), '\n')
		if i < 0 {
			if len(l.buf) > maxLine { // never hold unbounded data
				if err := l.emit(l.buf); err != nil {
					return len(p), err
				}
				l.buf = l.buf[:0]
			}
			return len(p), nil
		}
		if err := l.emit(l.buf[:i+1]); err != nil {
			return len(p), err
		}
		l.buf = l.buf[i+1:]
	}
}

func (l *LineWriter) emit(b []byte) error {
	_, err := io.WriteString(l.w, l.r.String(string(b)))
	return err
}

// Close flushes a trailing partial line.
func (l *LineWriter) Close() error {
	if len(l.buf) > 0 {
		err := l.emit(l.buf)
		l.buf = nil
		return err
	}
	return nil
}
