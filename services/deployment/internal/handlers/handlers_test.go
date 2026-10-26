package handlers

import (
	"testing"
)

func TestNormalizeStrategy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"blue_green", "blue_green"},
		{"BLUE-GREEN", "blue_green"},
		{"bluegreen", "blue_green"},
		{"rolling", "rolling"},
		{"", "rolling"},
		{"invalid_strategy", "rolling"},
	}

	for _, tt := range tests {
		got := normalizeStrategy(tt.input)
		if got != tt.want {
			t.Errorf("normalizeStrategy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
