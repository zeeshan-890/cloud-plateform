// Package retention periodically deletes old log entries so the table cannot grow unbounded.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/jp-cloud/logging/internal/store"
)

// Run sweeps every interval until ctx is cancelled. Deletes happen in bounded batches.
func Run(ctx context.Context, st store.Repo, days int, interval time.Duration, log *slog.Logger) {
	if days <= 0 {
		days = 14
	}
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		Sweep(ctx, st, time.Now().UTC().AddDate(0, 0, -days), log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sweep deletes everything older than cutoff in batches of 5000 and returns the total removed.
func Sweep(ctx context.Context, st store.Repo, cutoff time.Time, log *slog.Logger) int64 {
	var total int64
	for ctx.Err() == nil {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		n, err := st.DeleteOlderThan(cctx, cutoff, 5000)
		cancel()
		if err != nil {
			log.Warn("log retention sweep failed", "err", err)
			break
		}
		total += n
		if n < 5000 {
			break
		}
	}
	if total > 0 {
		log.Info("log retention", "deleted", total, "before", cutoff.Format(time.RFC3339))
	}
	return total
}
