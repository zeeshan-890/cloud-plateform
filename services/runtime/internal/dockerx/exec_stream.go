package dockerx

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

const maxStderrTail = 4096

type closeWriter interface{ CloseWrite() error }

// ExecStream runs cmd in a running container, streaming stdin to the process and its
// stdout to stdout (stderr is returned as a bounded tail). It returns the exit code.
func (e *Engine) ExecStream(ctx context.Context, container string, cmd []string, env map[string]string, stdin io.Reader, stdout io.Writer) (exit int, stderrTail string, err error) {
	if !ValidName(container) {
		return -1, "", fmt.Errorf("invalid container name")
	}
	if len(cmd) == 0 {
		return -1, "", errors.New("empty command")
	}
	if e.Simulated() || e.dial == nil {
		return -1, "", errors.New("exec streaming unavailable")
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		if !ValidEnvKey(k) || strings.ContainsRune(v, 0) {
			return -1, "", fmt.Errorf("invalid exec environment")
		}
		envList = append(envList, k+"="+v)
	}
	body := map[string]any{"AttachStdin": stdin != nil, "AttachStdout": true, "AttachStderr": true, "Cmd": cmd, "Env": envList}
	raw, _, err := e.call(ctx, http.MethodPost, "/containers/"+container+"/exec", nil, body)
	if err != nil {
		return -1, "", err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || !ValidName(created.ID) {
		return -1, "", errors.New("docker exec: bad create response")
	}

	conn, err := e.dial(ctx)
	if err != nil {
		return -1, "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	payload, _ := json.Marshal(map[string]any{"Detach": false, "Tty": false})
	auth := ""
	if e.authHeader[0] != "" {
		auth = e.authHeader[0] + ": " + e.authHeader[1] + "\r\n"
	}
	req := fmt.Sprintf("POST /exec/%s/start HTTP/1.1\r\nHost: docker\r\n%sContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n%s",
		created.ID, auth, len(payload), payload)
	if _, err := io.WriteString(conn, req); err != nil {
		return -1, "", err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return -1, "", err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return -1, "", &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	if stdin != nil {
		go func() {
			_, _ = io.Copy(conn, stdin)
			if cw, ok := conn.(closeWriter); ok {
				_ = cw.CloseWrite()
			}
		}()
	}
	var tail []byte
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			break
		}
		size := int64(binary.BigEndian.Uint32(hdr[4:8]))
		if hdr[0] == 1 {
			if _, err := io.CopyN(stdout, br, size); err != nil {
				return -1, string(tail), err
			}
			continue
		}
		keep := size
		if keep > maxStderrTail {
			keep = maxStderrTail
		}
		buf := make([]byte, keep)
		if _, err := io.ReadFull(br, buf); err != nil {
			break
		}
		if size > keep {
			if _, err := io.CopyN(io.Discard, br, size-keep); err != nil {
				break
			}
		}
		tail = append(tail, buf...)
		if len(tail) > maxStderrTail {
			tail = tail[len(tail)-maxStderrTail:]
		}
	}
	if ctx.Err() != nil {
		return -1, string(tail), ctx.Err()
	}
	insp, _, err := e.call(context.WithoutCancel(ctx), http.MethodGet, "/exec/"+created.ID+"/json", nil, nil)
	if err != nil {
		return -1, string(tail), err
	}
	var st struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	_ = json.Unmarshal(insp, &st)
	return st.ExitCode, string(tail), nil
}

var _ net.Conn
