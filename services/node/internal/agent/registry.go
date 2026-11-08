package agent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// ForwardRegistry accepts connections on l and pipes each to upstream until ctx is done.
func ForwardRegistry(ctx context.Context, l net.Listener, upstream string, log *slog.Logger) {
	go func() { <-ctx.Done(); _ = l.Close() }()
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			d := net.Dialer{Timeout: 5 * time.Second}
			u, err := d.DialContext(ctx, "tcp", upstream)
			if err != nil {
				log.Warn("registry upstream dial failed", "upstream", upstream, "err", err)
				_ = c.Close()
				return
			}
			var wg sync.WaitGroup
			wg.Add(2)
			cp := func(dst, src net.Conn) {
				defer wg.Done()
				_, _ = io.Copy(dst, src)
				if cw, ok := dst.(interface{ CloseWrite() error }); ok {
					_ = cw.CloseWrite()
				}
			}
			go cp(u, c)
			go cp(c, u)
			wg.Wait()
			_ = c.Close()
			_ = u.Close()
		}()
	}
}
