package handlers

import (
	"log/slog"
	"net/http"

	"github.com/jp-cloud/go-common/httpx"
)

type Handler struct {
	Log *slog.Logger
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.HandleFunc("POST /notify", h.Notify)
}

type notifyReq struct {
	Type  string `json:"type"`
	Email string `json:"email"`
	Token string `json:"token"`
	OrgID string `json:"org_id"`
	Role  string `json:"role"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	var req notifyReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Phase 1: log only (MailHog / SMTP in later phase)
	h.Log.Info("notification",
		"type", req.Type,
		"email", req.Email,
		"org_id", req.OrgID,
		"role", req.Role,
		"token", req.Token,
		"subject", req.Subject,
	)
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"status":  "accepted",
		"channel": "log",
	})
}
