package proxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Reverse proxies requests to an upstream, preserving method/body/headers.
type Reverse struct {
	Target *url.URL
	Client *http.Client
}

func New(target string) (*Reverse, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	return &Reverse{
		Target: u,
		Client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *Reverse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	outURL := *p.Target
	outURL.Path = singleJoin(p.Target.Path, r.URL.Path)
	outURL.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		http.Error(w, `{"error":{"code":"bad_gateway","message":"proxy request failed"}}`, http.StatusBadGateway)
		return
	}
	copyHeaders(req.Header, r.Header)
	// Inject identity context for downstream services
	if uid := r.Header.Get("X-User-ID"); uid != "" {
		req.Header.Set("X-User-ID", uid)
	}
	if org := r.Header.Get("X-Org-ID"); org != "" {
		req.Header.Set("X-Org-ID", org)
	}
	if role := r.Header.Get("X-Org-Role"); role != "" {
		req.Header.Set("X-Org-Role", role)
	}
	if rid := r.Header.Get("X-Request-ID"); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	if tid := r.Header.Get("X-Trace-ID"); tid != "" {
		req.Header.Set("X-Trace-ID", tid)
	}
	if tp := r.Header.Get("traceparent"); tp != "" {
		req.Header.Set("traceparent", tp)
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_gateway","message":"upstream unavailable"}}`))
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func singleJoin(base, path string) string {
	base = strings.TrimSuffix(base, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		// Hop-by-hop headers
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
			"te", "trailers", "transfer-encoding", "upgrade", "content-length":
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
