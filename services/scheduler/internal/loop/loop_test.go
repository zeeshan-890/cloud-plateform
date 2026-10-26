package loop

import (
	"testing"
)

func TestBuildPreviewHostname(t *testing.T) {
	tests := []struct {
		branch  string
		pid     string
		base    string
		wantSub string
	}{
		{"preview/feature-login", "proj-uuid-1234", "preview.example.com", "b-feature-login-projuuid.preview.example.com"},
		{"preview/pr-42", "12345678-abcd", "preview.example.com", "pr-42-12345678.preview.example.com"},
		{"main", "abcdef12-3456", "preview.example.com", "b-main-abcdef12.preview.example.com"},
	}

	for _, tt := range tests {
		got := buildPreviewHostname(tt.branch, tt.pid, tt.base)
		if got != tt.wantSub {
			t.Errorf("buildPreviewHostname(%q, %q, %q) = %q, want %q", tt.branch, tt.pid, tt.base, got, tt.wantSub)
		}
	}
}

func TestSanitizeHostLabel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"feature_branch", "feature-branch"},
		{"UPPER-case-123", "upper-case-123"},
		{"--multiple---hyphens--", "multiple-hyphens"},
		{"", "x"},
	}

	for _, tt := range tests {
		got := sanitizeHostLabel(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeHostLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsPreviewBranch(t *testing.T) {
	if !isPreviewBranch("preview/ui-v2") {
		t.Error("expected preview/ui-v2 to be recognized as preview branch")
	}
	if !isPreviewBranch("feature-preview") {
		t.Error("expected feature-preview to be recognized as preview branch")
	}
	if isPreviewBranch("main") {
		t.Error("expected main not to be preview branch")
	}
}

func TestPayloadBool(t *testing.T) {
	if !payloadBool(true) {
		t.Error("expected bool true")
	}
	if !payloadBool("true") {
		t.Error("expected string true")
	}
	if !payloadBool("1") {
		t.Error("expected string 1")
	}
	if payloadBool(false) || payloadBool("false") || payloadBool("0") {
		t.Error("expected false results")
	}
}

func TestInferKind(t *testing.T) {
	if k := inferKind("my-image", map[string]any{"kind": "worker"}); k != "worker" {
		t.Errorf("expected worker, got %s", k)
	}
	if k := inferKind("my-image", map[string]any{"framework": "nodejs"}); k != "node" {
		t.Errorf("expected node, got %s", k)
	}
	if k := inferKind("my-image:latest", nil); k != "container" {
		t.Errorf("expected container, got %s", k)
	}
}
