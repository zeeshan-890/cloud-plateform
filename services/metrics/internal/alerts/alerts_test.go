package alerts

import (
	"testing"
)

func TestAlertRuleEvaluation(t *testing.T) {
	rule := AlertRule{
		ID:         "rule-cpu-high",
		Name:       "High CPU Usage",
		MetricName: "container_cpu_percent",
		Operator:   ">",
		Threshold:  85.0,
		Severity:   SeverityWarning,
	}

	// Under threshold
	alert := rule.Evaluate(75.0)
	if alert != nil {
		t.Fatalf("expected nil alert for value 75.0, got %+v", alert)
	}

	// Over threshold
	alert = rule.Evaluate(92.5)
	if alert == nil {
		t.Fatalf("expected alert for value 92.5, got nil")
	}
	if alert.Severity != SeverityWarning {
		t.Errorf("expected SeverityWarning, got %v", alert.Severity)
	}
	if alert.Status != AlertStatusFiring {
		t.Errorf("expected AlertStatusFiring, got %v", alert.Status)
	}
}

func TestQuotaCheck(t *testing.T) {
	quota := Quota{
		MaxMemoryMB:      2048,
		MaxCPUMillicores: 2000,
		MaxReplicas:      4,
	}

	usage := QuotaUsage{
		UsedMemoryMB:      1024,
		UsedCPUMillicores: 1000,
		UsedReplicas:      2,
	}

	// Allowed request
	res := quota.Check(usage, 512, 500, 1)
	if !res.Allowed {
		t.Fatalf("expected request to be allowed, got violations: %v", res.Violations)
	}

	// Exceeds memory
	res = quota.Check(usage, 1500, 500, 1)
	if res.Allowed {
		t.Fatalf("expected request to be denied due to memory quota")
	}
	if len(res.Violations) == 0 {
		t.Errorf("expected violation details")
	}

	// Exceeds replicas
	res = quota.Check(usage, 256, 200, 3)
	if res.Allowed {
		t.Fatalf("expected request to be denied due to replica count")
	}
}
