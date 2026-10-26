package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalCreateValidation(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/builds", h.InternalCreate)

	// Missing org_id and project_id should fail
	req := httptest.NewRequest(http.MethodPost, "/internal/builds", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", w.Code)
	}
}
