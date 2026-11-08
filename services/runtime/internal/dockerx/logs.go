package dockerx

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Log limits protect the runtime from hostile / runaway container output.
const (
	MaxFramePayload = 64 << 10 // bytes kept per frame; the rest of a larger frame is discarded
	MaxLogBytes     = 4 << 20  // total bytes returned by one Logs call
	MaxLogEntries   = 5000
	MaxTail         = 2000
)

// LogEvent is one decoded log line.
type LogEvent struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Level    string `json:"level"`
	Message  string `json:"message"`
	LoggedAt string `json:"logged_at"`
}

// LogOptions controls a log request.
type LogOptions struct {
	Tail  int
	Since time.Time
}

func (o LogOptions) query(follow bool) url.Values {
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("timestamps", "1")
	t := o.Tail
	if t <= 0 {
		t = 200
	}
	if t > MaxTail {
		t = MaxTail
	}
	q.Set("tail", fmt.Sprint(t))
	if !o.Since.IsZero() {
		q.Set("since", fmt.Sprint(o.Since.Unix()))
	}
	if follow {
		q.Set("follow", "1")
	}
	return q
}

// Logs returns up to opts.Tail decoded log lines.
func (e *Engine) Logs(ctx context.Context, container string, opts LogOptions) ([]LogEvent, error) {
	seg, err := segment(container)
	if err != nil {
		return nil, err
	}
	if e.Simulated() {
		return []LogEvent{{ID: "sim-1", Source: "runtime", Level: "info", Message: "simulated runtime container " + container,
			LoggedAt: time.Now().UTC().Format(time.RFC3339)}}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, e.base+"/containers/"+seg+"/logs?"+opts.query(false).Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	out := []LogEvent{}
	_ = ReadFrames(resp.Body, MaxLogBytes, func(ev LogEvent) bool {
		ev.ID = fmt.Sprintf("log-%d", len(out)+1)
		out = append(out, ev)
		return len(out) < MaxLogEntries
	})
	return out, nil
}

// FollowLogs streams log lines to fn until ctx ends, the container exits, fn returns
// false or maxBytes were read.
func (e *Engine) FollowLogs(ctx context.Context, container string, opts LogOptions, maxBytes int64, fn func(LogEvent) bool) error {
	seg, err := segment(container)
	if err != nil {
		return err
	}
	if e.Simulated() {
		fn(LogEvent{Source: "runtime", Level: "info", Message: "simulated", LoggedAt: time.Now().UTC().Format(time.RFC3339)})
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+"/containers/"+seg+"/logs?"+opts.query(true).Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Message: "logs"}
	}
	return ReadFrames(resp.Body, maxBytes, fn)
}

// ReadFrames decodes Docker's multiplexed stream. The 4-byte frame size comes from an
// untrusted source: at most MaxFramePayload bytes of each frame are buffered and the
// remainder is discarded, so a hostile size can never cause a huge allocation.
func ReadFrames(r io.Reader, maxTotal int64, fn func(LogEvent) bool) error {
	br := bufio.NewReaderSize(r, 32<<10)
	hdr := make([]byte, 8)
	var total int64
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		stream := hdr[0]
		size := int64(binary.BigEndian.Uint32(hdr[4:8]))
		keep := size
		if keep > MaxFramePayload {
			keep = MaxFramePayload
		}
		payload := make([]byte, keep)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil
		}
		if size > keep {
			if _, err := io.CopyN(io.Discard, br, size-keep); err != nil {
				return nil
			}
		}
		total += size
		for _, line := range strings.Split(strings.TrimRight(string(payload), "\r\n"), "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			if !fn(parseLine(stream, line)) {
				return nil
			}
		}
		if maxTotal > 0 && total >= maxTotal {
			return nil
		}
	}
}

func parseLine(stream byte, raw string) LogEvent {
	level := "info"
	if stream == 2 {
		level = "warn"
		low := strings.ToLower(raw)
		if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "exception") || strings.Contains(low, "panic") {
			level = "error"
		}
	}
	at := time.Now().UTC().Format(time.RFC3339)
	msg := raw
	if i := strings.IndexByte(raw, ' '); i > 0 {
		if _, err := time.Parse(time.RFC3339Nano, raw[:i]); err == nil {
			at = raw[:i]
			msg = raw[i+1:]
		}
	}
	return LogEvent{Source: "app", Level: level, Message: msg, LoggedAt: at}
}
