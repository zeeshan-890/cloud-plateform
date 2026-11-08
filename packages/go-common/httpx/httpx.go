package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorBody matches the platform error contract.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// MaxBody is the default request body limit (1 MiB).
const MaxBody = 1 << 20

// Decode reads JSON with a 1 MiB cap.
func Decode(r *http.Request, dst any) error {
	return DecodeLimit(r, dst, MaxBody)
}

// DecodeLimit reads JSON with a custom byte cap.
func DecodeLimit(r *http.Request, dst any, max int64) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, max))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func Healthz(w http.ResponseWriter, _ *http.Request) {
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
