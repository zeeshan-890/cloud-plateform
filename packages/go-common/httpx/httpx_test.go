package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := map[string]string{"foo": "bar"}
	JSON(rec, http.StatusCreated, payload)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
	var res map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res["foo"] != "bar" {
		t.Errorf("expected bar, got %s", res["foo"])
	}
}

func TestError(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusBadRequest, "invalid_param", "missing name field")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
	var errBody ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("failed to decode error body: %v", err)
	}
	if errBody.Error.Code != "invalid_param" {
		t.Errorf("expected code invalid_param, got %s", errBody.Error.Code)
	}
	if errBody.Error.Message != "missing name field" {
		t.Errorf("expected message 'missing name field', got %s", errBody.Error.Message)
	}
}

func TestDecode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hello":"world"}`))
	var target struct {
		Hello string `json:"hello"`
	}
	if err := Decode(req, &target); err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if target.Hello != "world" {
		t.Errorf("expected world, got %s", target.Hello)
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	Healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("expected status: ok in body, got %s", rec.Body.String())
	}
}
