package static

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticServingAndSPAFallback(t *testing.T) {
	tempDir := t.TempDir()

	// Write index.html
	if err := os.WriteFile(filepath.Join(tempDir, "index.html"), []byte("<h1>JP Dashboard</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write subpage /projects/index.html
	projDir := filepath.Join(tempDir, "projects")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "index.html"), []byte("<h1>Projects Page</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write 404.html
	if err := os.WriteFile(filepath.Join(tempDir, "404.html"), []byte("<h1>Page Not Found</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(tempDir)

	// 1. Root / serves index.html
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !testingContains(w.Body.String(), "JP Dashboard") {
		t.Errorf("expected 200 with 'JP Dashboard', got %d: %s", w.Code, w.Body.String())
	}

	// 2. /projects/ serves projects/index.html
	req = httptest.NewRequest(http.MethodGet, "/projects/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !testingContains(w.Body.String(), "Projects Page") {
		t.Errorf("expected 200 with 'Projects Page', got %d: %s", w.Code, w.Body.String())
	}

	// 3. Unknown route /unknown-route serves 404.html
	req = httptest.NewRequest(http.MethodGet, "/unknown-route", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !testingContains(w.Body.String(), "Page Not Found") {
		t.Errorf("expected fallback to 'Page Not Found', got: %s", w.Body.String())
	}
}

func testingContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && testingIndex(s, substr) >= 0))
}

func testingIndex(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
