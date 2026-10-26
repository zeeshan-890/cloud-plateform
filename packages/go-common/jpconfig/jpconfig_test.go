package jpconfig

import (
	"testing"
)

func TestValidateManifest(t *testing.T) {
	valid := &Manifest{
		Name:    "my-app",
		Runtime: "nodejs",
		Deploy: &Deploy{
			Replicas: 2,
			Port:     3000,
			Strategy: "rolling",
		},
	}
	if err := Validate(valid); err != nil {
		t.Fatalf("expected valid manifest, got error: %v", err)
	}

	// Test invalid runtime
	invalidRT := &Manifest{
		Name:    "my-app",
		Runtime: "ruby_on_rails",
	}
	if err := Validate(invalidRT); err == nil {
		t.Fatal("expected error for unsupported runtime")
	}

	// Test invalid slug (uppercase or special chars)
	invalidSlug := &Manifest{
		Name:    "-invalid_slug-",
		Runtime: "python",
	}
	if err := Validate(invalidSlug); err == nil {
		t.Fatal("expected error for invalid slug")
	}

	// Test invalid replica count
	invalidReplicas := &Manifest{
		Name:    "my-app",
		Runtime: "go",
		Deploy: &Deploy{
			Replicas: 100, // max is 10
		},
	}
	if err := Validate(invalidReplicas); err == nil {
		t.Fatal("expected error for replicas > 10")
	}
}

func TestDriftDetection(t *testing.T) {
	desired := map[string]any{
		"name":    "web-app",
		"runtime": "nodejs",
		"deploy":  map[string]any{"replicas": 3},
	}
	applied := map[string]any{
		"name":    "web-app",
		"runtime": "nodejs",
		"deploy":  map[string]any{"replicas": 1},
	}

	drift, details := DriftStub(desired, applied)
	if !drift {
		t.Fatal("expected drift to be detected")
	}
	if len(details) == 0 {
		t.Fatal("expected drift details")
	}

	// Identical states
	drift2, _ := DriftStub(desired, desired)
	if drift2 {
		t.Fatal("expected no drift between identical states")
	}
}
