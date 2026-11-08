package dockerx

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const maxExecOutput = 256 << 10

// Exec runs a command inside a running container and returns its demultiplexed output and exit code.
// Output is capped at 256 KiB per stream. The container name is validated (no path injection).
func (e *Engine) Exec(ctx context.Context, container string, cmd []string) (stdout, stderr string, exit int, err error) {
	if !ValidName(container) {
		return "", "", -1, fmt.Errorf("invalid container name %q", truncate(container, 60))
	}
	if len(cmd) == 0 {
		return "", "", -1, errors.New("empty command")
	}
	if e.Simulated() {
		return "", "", -1, errors.New("exec is not available in simulate mode")
	}
	raw, _, err := e.call(ctx, http.MethodPost, "/containers/"+container+"/exec", nil,
		map[string]any{"AttachStdout": true, "AttachStderr": true, "Cmd": cmd})
	if err != nil {
		return "", "", -1, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" || !ValidName(created.ID) {
		return "", "", -1, errors.New("docker exec: bad create response")
	}
	out, _, err := e.call(ctx, http.MethodPost, "/exec/"+created.ID+"/start", nil,
		map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return "", "", -1, err
	}
	so, se := demux(out)
	insp, _, err := e.call(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, nil)
	if err != nil {
		return so, se, -1, err
	}
	var st struct {
		ExitCode int `json:"ExitCode"`
	}
	_ = json.Unmarshal(insp, &st)
	return so, se, st.ExitCode, nil
}

// demux splits Docker's multiplexed stream (8-byte header per frame) into stdout and stderr.
func demux(b []byte) (stdout, stderr string) {
	var so, se []byte
	for len(b) >= 8 {
		size := int(binary.BigEndian.Uint32(b[4:8]))
		stream := b[0]
		b = b[8:]
		if size < 0 || size > len(b) {
			size = len(b)
		}
		chunk := b[:size]
		b = b[size:]
		switch stream {
		case 1:
			if len(so) < maxExecOutput {
				so = append(so, chunk...)
			}
		case 2:
			if len(se) < maxExecOutput {
				se = append(se, chunk...)
			}
		}
	}
	return string(so), string(se)
}
