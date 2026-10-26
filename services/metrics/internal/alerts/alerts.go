package alerts

import (
	"fmt"
	"time"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type AlertRule struct {
	ID          string   `json:"id"`
	OrgID       string   `json:"org_id"`
	ProjectID   string   `json:"project_id"`
	Name        string   `json:"name"`
	MetricName  string   `json:"metric_name"`
	Operator    string   `json:"operator"` // ">", ">=", "<", "<="
	Threshold   float64  `json:"threshold"`
	Severity    Severity `json:"severity"`
	Description string   `json:"description"`
}

type AlertStatus string

const (
	AlertStatusFiring   AlertStatus = "firing"
	AlertStatusResolved AlertStatus = "resolved"
)

type Alert struct {
	RuleID      string      `json:"rule_id"`
	RuleName    string      `json:"rule_name"`
	OrgID       string      `json:"org_id"`
	ProjectID   string      `json:"project_id"`
	MetricName  string      `json:"metric_name"`
	CurrentVal  float64     `json:"current_value"`
	Threshold   float64     `json:"threshold"`
	Severity    Severity    `json:"severity"`
	Status      AlertStatus `json:"status"`
	TriggeredAt time.Time   `json:"triggered_at"`
	Message     string      `json:"message"`
}

// Evaluate evaluates a metric sample against an AlertRule.
func (r *AlertRule) Evaluate(value float64) *Alert {
	firing := false
	switch r.Operator {
	case ">":
		firing = value > r.Threshold
	case ">=":
		firing = value >= r.Threshold
	case "<":
		firing = value < r.Threshold
	case "<=":
		firing = value <= r.Threshold
	default:
		firing = value > r.Threshold
	}

	if firing {
		return &Alert{
			RuleID:      r.ID,
			RuleName:    r.Name,
			OrgID:       r.OrgID,
			ProjectID:   r.ProjectID,
			MetricName:  r.MetricName,
			CurrentVal:  value,
			Threshold:   r.Threshold,
			Severity:    r.Severity,
			Status:      AlertStatusFiring,
			TriggeredAt: time.Now().UTC(),
			Message:     fmt.Sprintf("%s breached threshold: %.2f %s %.2f", r.Name, value, r.Operator, r.Threshold),
		}
	}
	return nil
}

// Quota defines resource limits for a project or organization.
type Quota struct {
	MaxMemoryMB    int64 `json:"max_memory_mb"`
	MaxCPUMillicores int64 `json:"max_cpu_millicores"`
	MaxReplicas     int   `json:"max_replicas"`
	MaxStorageGB    int64 `json:"max_storage_gb"`
}

type QuotaUsage struct {
	UsedMemoryMB     int64 `json:"used_memory_mb"`
	UsedCPUMillicores int64 `json:"used_cpu_millicores"`
	UsedReplicas     int   `json:"used_replicas"`
	UsedStorageGB    int64 `json:"used_storage_gb"`
}

type QuotaCheckResult struct {
	Allowed bool     `json:"allowed"`
	Reason  string   `json:"reason,omitempty"`
	Violations []string `json:"violations,omitempty"`
}

// Check enforces resource limits against requested additions.
func (q *Quota) Check(usage QuotaUsage, requestedMemoryMB int64, requestedCPUMillicores int64, requestedReplicas int) QuotaCheckResult {
	var violations []string

	if q.MaxMemoryMB > 0 && (usage.UsedMemoryMB+requestedMemoryMB) > q.MaxMemoryMB {
		violations = append(violations, fmt.Sprintf("memory limit exceeded: requesting %dMB + current %dMB exceeds quota %dMB", requestedMemoryMB, usage.UsedMemoryMB, q.MaxMemoryMB))
	}
	if q.MaxCPUMillicores > 0 && (usage.UsedCPUMillicores+requestedCPUMillicores) > q.MaxCPUMillicores {
		violations = append(violations, fmt.Sprintf("CPU limit exceeded: requesting %dm + current %dm exceeds quota %dm", requestedCPUMillicores, usage.UsedCPUMillicores, q.MaxCPUMillicores))
	}
	if q.MaxReplicas > 0 && (usage.UsedReplicas+requestedReplicas) > q.MaxReplicas {
		violations = append(violations, fmt.Sprintf("replica limit exceeded: requesting %d + current %d exceeds quota %d", requestedReplicas, usage.UsedReplicas, q.MaxReplicas))
	}

	if len(violations) > 0 {
		return QuotaCheckResult{
			Allowed:    false,
			Reason:     "resource quota exceeded",
			Violations: violations,
		}
	}

	return QuotaCheckResult{Allowed: true}
}
