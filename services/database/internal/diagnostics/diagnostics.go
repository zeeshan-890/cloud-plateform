package diagnostics

import (
	"regexp"
	"strings"
	"time"
)

type SlowQuery struct {
	Fingerprint   string    `json:"fingerprint"`
	Query         string    `json:"query"`
	Calls         int64     `json:"calls"`
	TotalTimeMs   float64   `json:"total_time_ms"`
	MeanTimeMs    float64   `json:"mean_time_ms"`
	RowsExamined  int64     `json:"rows_examined"`
	LastSeen      time.Time `json:"last_seen"`
}

type DatabaseStats struct {
	DatabaseID    string      `json:"database_id"`
	Connections   int         `json:"connections"`
	MaxConnections int        `json:"max_connections"`
	SlowQueries   []SlowQuery `json:"slow_queries"`
	CapturedAt    time.Time   `json:"captured_at"`
}

type BackupSnapshot struct {
	ID          string    `json:"id"`
	DatabaseID  string    `json:"database_id"`
	OrgID       string    `json:"org_id"`
	ProjectID   string    `json:"project_id"`
	SizeBytes   int64     `json:"size_bytes"`
	Status      string    `json:"status"` // "completed", "failed", "in_progress"
	StorageURI  string    `json:"storage_uri"`
	CreatedAt   time.Time `json:"created_at"`
}

var (
	// Redact string literals: 'secret' -> '?'
	stringLiteralRegex = regexp.MustCompile(`'[^']*'`)
	// Redact numeric literals: = 12345 -> = ?
	numericLiteralRegex = regexp.MustCompile(`\b\d+\b`)
	// Redact password assignments
	passwordRegex = regexp.MustCompile(`(?i)(password|secret|token)\s*=\s*'[^']*'`)
)

// RedactQuery cleans credentials and literal values from SQL for safe observability.
func RedactQuery(sql string) string {
	cleaned := passwordRegex.ReplaceAllString(sql, `$1 = '***'`)
	cleaned = stringLiteralRegex.ReplaceAllString(cleaned, `?`)
	cleaned = numericLiteralRegex.ReplaceAllString(cleaned, `?`)
	return strings.TrimSpace(cleaned)
}

// GenerateFingerprint creates a normalized fingerprint for slow query grouping.
func GenerateFingerprint(sql string) string {
	normalized := strings.ToLower(RedactQuery(sql))
	// Collapse multiple spaces
	spaces := regexp.MustCompile(`\s+`)
	return spaces.ReplaceAllString(normalized, " ")
}
