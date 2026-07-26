package otelx

import (
	"context"
	"net/http"
	"os"

	"github.com/jp-cloud/go-common/middleware"
	"github.com/google/uuid"
)

// Config describes optional OTLP export (Tempo under monitoring profile).
type Config struct {
	Endpoint string // OTEL_EXPORTER_OTLP_ENDPOINT e.g. http://tempo:4318
	Service  string
	Enabled  bool
}

// FromEnv reads OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_SERVICE_NAME.
func FromEnv(defaultService string) Config {
	ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	svc := os.Getenv("OTEL_SERVICE_NAME")
	if svc == "" {
		svc = defaultService
	}
	return Config{Endpoint: ep, Service: svc, Enabled: ep != ""}
}

type ctxKey int

const traceIDKey ctxKey = 1

// TraceIDFrom returns the trace / request correlation id.
func TraceIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return middleware.RequestIDFrom(ctx)
}

// Propagate ensures X-Request-ID (and optional traceparent) on inbound/outbound requests.
// Full OTLP export is optional; when Tempo is down the middleware still correlates via request id.
func Propagate(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := r.Header.Get("X-Request-ID")
			if rid == "" {
				rid = uuid.NewString()
			}
			w.Header().Set("X-Request-ID", rid)
			r.Header.Set("X-Request-ID", rid)

			traceID := r.Header.Get("X-Trace-ID")
			if traceID == "" {
				traceID = rid
			}
			w.Header().Set("X-Trace-ID", traceID)
			if cfg.Enabled {
				w.Header().Set("X-Otel-Service", cfg.Service)
				// Lightweight W3C-ish header for Tempo-compatible collectors when present.
				if r.Header.Get("traceparent") == "" {
					// version-traceid-spanid-flags (traceid = 32 hex from uuid without dashes)
					hex := stripDashes(traceID)
					if len(hex) >= 32 {
						r.Header.Set("traceparent", "00-"+hex[:32]+"-"+hex[:16]+"-01")
					}
				}
			}
			ctx := context.WithValue(r.Context(), middleware.RequestIDKey, rid)
			ctx = context.WithValue(ctx, traceIDKey, traceID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// InjectOutbound copies correlation headers onto an outbound request.
func InjectOutbound(ctx context.Context, req *http.Request) {
	if rid := middleware.RequestIDFrom(ctx); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	if tid := TraceIDFrom(ctx); tid != "" {
		req.Header.Set("X-Trace-ID", tid)
	}
}

func stripDashes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			out = append(out, s[i])
		}
	}
	// pad if needed
	for len(out) < 32 {
		out = append(out, '0')
	}
	return string(out)
}
