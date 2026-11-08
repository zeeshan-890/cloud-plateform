// Package proxy is a streaming reverse proxy to one upstream service.
//
// It never fabricates responses: when the upstream is unreachable the caller gets a real 502
// (or 504) in the platform error shape.
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/clientip"
	"github.com/jp-cloud/go-common/internalauth"
)

type ctxKey struct{}

// WithClientIP records the resolved client address for the proxy to forward.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ip)
}

// ClientIPFrom returns the address stored by WithClientIP.
func ClientIPFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// Reverse proxies requests to an upstream.
type Reverse struct {
	Target *url.URL
	// SignInternal adds INTERNAL_TOKEN and the trusted client-IP header (use for services that
	// honour them, i.e. identity).
	SignInternal bool
	rp           *httputil.ReverseProxy
}

// Option customises a Reverse.
type Option func(*Reverse)

// Signed makes the proxy present INTERNAL_TOKEN and X-JP-Client-IP upstream.
func Signed() Option { return func(r *Reverse) { r.SignInternal = true } }

// New builds a proxy for target.
func New(target string, opts ...Option) (*Reverse, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, errors.New("proxy: target must be an absolute URL: " + target)
	}
	p := &Reverse{Target: u}
	for _, o := range opts {
		o(p)
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Minute, // per-request deadlines are set by the route wrapper
		ExpectContinueTimeout: 1 * time.Second,
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite:       p.rewrite,
		Transport:     tr,
		FlushInterval: -1, // flush immediately: log streams and SSE are not buffered
		ModifyResponse: func(resp *http.Response) error {
			// The gateway owns CORS; never let an upstream widen it.
			for k := range resp.Header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					resp.Header.Del(k)
				}
			}
			return nil
		},
		ErrorHandler: errorHandler,
	}
	return p, nil
}

func (p *Reverse) rewrite(pr *httputil.ProxyRequest) {
	in, out := pr.In, pr.Out
	out.URL.Scheme = p.Target.Scheme
	out.URL.Host = p.Target.Host
	out.URL.Path = singleJoin(p.Target.Path, in.URL.Path)
	out.URL.RawPath = ""
	out.URL.RawQuery = in.URL.RawQuery
	out.Host = p.Target.Host
	// Rewrite drops inbound X-Forwarded-* already; set our own, trusted value.
	if ip := ClientIPFrom(in.Context()); ip != "" {
		out.Header.Set("X-Forwarded-For", ip)
	}
	if p.SignInternal {
		internalauth.Sign(out)
		if ip := ClientIPFrom(in.Context()); ip != "" {
			out.Header.Set(clientip.Header, ip)
		}
	} else {
		out.Header.Del(internalauth.Header)
		out.Header.Del(clientip.Header)
	}
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(r.Context().Err(), context.Canceled) {
		return // client went away
	}
	slog.Warn("upstream error", "path", r.URL.Path, "err", err)
	w.Header().Set("Content-Type", "application/json")
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"error":{"code":"gateway_timeout","message":"the upstream service did not respond in time"}}`))
		return
	}
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(`{"error":{"code":"bad_gateway","message":"upstream service unavailable"}}`))
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (p *Reverse) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.rp.ServeHTTP(w, r) }

func singleJoin(base, path string) string {
	base = strings.TrimSuffix(base, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}
