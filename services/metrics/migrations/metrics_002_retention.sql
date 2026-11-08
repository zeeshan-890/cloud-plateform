-- metrics migration 002: retention index; scrape targets were never scraped and are removed.
CREATE INDEX IF NOT EXISTS idx_metric_samples_recorded ON metric_samples(recorded_at);
DROP TABLE IF EXISTS scrape_targets;
