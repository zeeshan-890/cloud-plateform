// Package diagnostics reads pg_stat_statements from the database container (through the add-on
// service) and returns redacted, fingerprinted slow queries.
package diagnostics

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type SlowQuery struct {
	Fingerprint  string  `json:"fingerprint"`
	Query        string  `json:"query"`
	Calls        int64   `json:"calls"`
	TotalTimeMs  float64 `json:"total_time_ms"`
	MeanTimeMs   float64 `json:"mean_time_ms"`
	RowsExamined int64   `json:"rows_examined"`
}

type DatabaseStats struct {
	DatabaseID     string      `json:"database_id"`
	Connections    int         `json:"connections"`
	MaxConnections int         `json:"max_connections"`
	SlowQueries    []SlowQuery `json:"slow_queries"`
	CapturedAt     time.Time   `json:"captured_at"`
}

// Queries sent to the container. pg_stat_statements is preloaded and created at provisioning time.
const (
	EnableExtensionSQL = `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`
	// tab separated: calls, total_ms, mean_ms, rows, query (whitespace collapsed)
	SlowQueriesSQL = `SELECT calls, round(total_exec_time::numeric,2), round(mean_exec_time::numeric,2), rows, ` +
		`left(regexp_replace(query, '\s+', ' ', 'g'), 1000) FROM pg_stat_statements ` +
		`WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) ` +
		`AND query NOT ILIKE '%pg_stat_statements%' ORDER BY mean_exec_time DESC LIMIT 25`
	ConnectionsSQL = `SELECT (SELECT count(*) FROM pg_stat_activity), current_setting('max_connections')::int`
)

var (
	stringLiteralRegex  = regexp.MustCompile(`'[^']*'`)
	numericLiteralRegex = regexp.MustCompile(`\b\d+\b`)
	passwordRegex       = regexp.MustCompile(`(?i)(password|secret|token)\s*=\s*'[^']*'`)
	spaces              = regexp.MustCompile(`\s+`)
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
	return spaces.ReplaceAllString(strings.ToLower(RedactQuery(sql)), " ")
}

// ParseSlowQueries parses psql -At -F tab output of SlowQueriesSQL.
func ParseSlowQueries(out string) ([]SlowQuery, error) {
	res := []SlowQuery{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 5)
		if len(f) < 5 {
			return nil, fmt.Errorf("unexpected pg_stat_statements row: %q", line)
		}
		calls, e1 := strconv.ParseInt(f[0], 10, 64)
		total, e2 := strconv.ParseFloat(f[1], 64)
		mean, e3 := strconv.ParseFloat(f[2], 64)
		rows, e4 := strconv.ParseInt(f[3], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			return nil, fmt.Errorf("unparsable pg_stat_statements row: %q", line)
		}
		res = append(res, SlowQuery{Fingerprint: GenerateFingerprint(f[4]), Query: RedactQuery(f[4]), Calls: calls, TotalTimeMs: total, MeanTimeMs: mean, RowsExamined: rows})
	}
	return res, nil
}

// ParseConnections parses the output of ConnectionsSQL ("active<TAB>max").
func ParseConnections(out string) (active, max int, err error) {
	f := strings.Split(strings.TrimSpace(out), "\t")
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("unexpected connections row: %q", out)
	}
	if active, err = strconv.Atoi(f[0]); err != nil {
		return
	}
	max, err = strconv.Atoi(f[1])
	return
}
