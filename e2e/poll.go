package e2e

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Poller hits an edge URL every 100ms in the background and records everything that is not a 2xx answer.
type Poller struct {
	stop     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	Total    int
	Bad      []string
	Versions map[string]int
}

// StartPoller begins polling http://edge{path} with the given Host header.
func StartPoller(e *Env, host, path string) *Poller {
	p := &Poller{stop: make(chan struct{}), done: make(chan struct{}), Versions: map[string]int{}}
	go func() {
		defer close(p.done)
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-tk.C:
				code, body, err := e.EdgeGet(host, path)
				p.mu.Lock()
				p.Total++
				switch {
				case err != nil:
					p.Bad = append(p.Bad, "error: "+err.Error())
				case code < 200 || code > 299:
					p.Bad = append(p.Bad, fmt.Sprintf("status %d at %s", code, time.Now().Format("15:04:05.000")))
				default:
					if i := strings.Index(body, `"version":"`); i >= 0 {
						v := body[i+11:]
						if j := strings.IndexByte(v, '"'); j >= 0 {
							p.Versions[v[:j]]++
						}
					}
				}
				p.mu.Unlock()
			}
		}
	}()
	return p
}

// Stop ends polling and returns the number of requests and the non-2xx observations.
func (p *Poller) Stop() (int, []string) {
	close(p.stop)
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Total, append([]string(nil), p.Bad...)
}

// Seen returns how many successful answers carried the given app version.
func (p *Poller) Seen(version string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Versions[version]
}
