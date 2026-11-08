package service

import (
	"io"

	"github.com/google/uuid"
)

func newID() string { return uuid.NewString() }

type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, io.ErrShortWrite
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

// LimitWriter fails writes beyond n bytes.
func LimitWriter(w io.Writer, n int64) io.Writer { return &limitWriter{w: w, n: n} }
