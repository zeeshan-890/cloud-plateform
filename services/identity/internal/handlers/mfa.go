package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/identity/internal/store"
	"github.com/jp-cloud/identity/internal/totp"
)

func subtleEq(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// HashBackupCode returns the keyed hash under which a backup code is stored. HMAC with a
// server-side key means a database leak alone cannot be brute-forced offline.
func HashBackupCode(key []byte, userID, code string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(userID))
	m.Write([]byte{0})
	m.Write([]byte(totp.NormalizeBackupCode(code)))
	return hex.EncodeToString(m.Sum(nil))
}

func (h *Handler) hashBackup(userID, code string) string {
	return HashBackupCode(h.BackupKey, userID, code)
}

func (h *Handler) sealSecret(userID, secret string) (string, error) {
	return secretbox.Seal(h.EncKey, secret, "totp:"+userID)
}

// SealForUpgrade / HashForUpgrade are used by the startup upgrade of legacy plaintext rows.
func (h *Handler) SealForUpgrade(userID, secret string) (string, error) {
	return h.sealSecret(userID, secret)
}
func (h *Handler) HashForUpgrade(userID, code string) string { return h.hashBackup(userID, code) }

func (h *Handler) openSecret(userID, stored string) (string, error) {
	if !secretbox.IsSealed(stored) {
		return stored, nil // legacy plaintext awaiting upgrade
	}
	return secretbox.Open(h.EncKey, stored, "totp:"+userID)
}

func isTOTPCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totp.Digits {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// verifySecondFactor accepts a current TOTP code (each code usable once) or an unused backup
// code (consumed atomically).
func (h *Handler) verifySecondFactor(ctx context.Context, user *store.User, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	if isTOTPCode(code) {
		if user.TOTPSecret == nil {
			return false
		}
		secret, err := h.openSecret(user.ID, *user.TOTPSecret)
		if err != nil {
			h.log().Error("totp secret cannot be opened", "user", user.ID, "err", err)
			return false
		}
		step, ok := totp.ValidateStep(code, secret, time.Now().UTC())
		if !ok {
			return false
		}
		// Replay protection: a given time step's code works once.
		set, err := h.Redis.SetNX(ctx, fmt.Sprintf("jp:totp:used:%s:%d", user.ID, step), "1", 2*time.Minute).Result()
		return err == nil && set
	}
	ok, err := h.Store.ConsumeBackupCode(ctx, user.ID, h.hashBackup(user.ID, code))
	return err == nil && ok
}

// SetupMFA starts (or restarts) enrolment. When MFA is already enabled a valid current code is
// required, and the new secret only replaces the old one once confirmed via /auth/mfa/enable.
func (h *Handler) SetupMFA(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	ctx := r.Context()
	user, err := h.Store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if r.ContentLength != 0 {
		_ = httpx.Decode(r, &req)
	}
	if h.failCount(ctx, "mfa-mgmt:"+user.ID) >= 10 {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many failed attempts; try again later")
		return
	}
	if user.TOTPEnabled {
		if strings.TrimSpace(req.Code) == "" {
			httpx.Error(w, http.StatusConflict, "mfa_already_enabled", "MFA is already enabled; provide a current code to re-enrol")
			return
		}
		if !h.verifySecondFactor(ctx, user, req.Code) {
			h.recordFail(ctx, "mfa-mgmt:"+user.ID)
			httpx.Error(w, http.StatusForbidden, "invalid_code", "code is incorrect")
			return
		}
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to generate TOTP secret")
		return
	}
	sealed, err := h.sealSecret(user.ID, secret)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to protect TOTP secret")
		return
	}
	if err := h.Store.SetPendingTOTP(ctx, user.ID, sealed); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to save TOTP secret")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "mfa.setup_started", Resource: "user", ResourceID: user.ID})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"secret":      secret,
		"otpauth_uri": totp.KeyURI(user.Email, "JP Cloud", secret),
	})
}

func (h *Handler) EnableMFA(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	ctx := r.Context()
	user, err := h.Store.GetUserByID(ctx, claims.UserID)
	if err != nil || user.TOTPPendingSecret == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "MFA setup not initialized")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Code) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "verification code required")
		return
	}
	if h.failCount(ctx, "mfa-mgmt:"+user.ID) >= 10 {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many failed attempts; try again later")
		return
	}
	secret, err := h.openSecret(user.ID, *user.TOTPPendingSecret)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "MFA setup must be restarted")
		return
	}
	if _, ok := totp.ValidateStep(req.Code, secret, time.Now().UTC()); !ok {
		h.recordFail(ctx, "mfa-mgmt:"+user.ID)
		httpx.Error(w, http.StatusBadRequest, "invalid_code", "verification code is incorrect")
		return
	}
	backupCodes, err := totp.GenerateBackupCodes(8)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to generate backup codes")
		return
	}
	hashes := make([]string, len(backupCodes))
	for i, c := range backupCodes {
		hashes[i] = h.hashBackup(user.ID, c)
	}
	if err := h.Store.EnableTOTP(ctx, user.ID, hashes); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to enable MFA")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "mfa.enabled", Resource: "user", ResourceID: user.ID})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"enabled":      true,
		"backup_codes": backupCodes, // shown exactly once; only keyed hashes are stored
	})
}

func (h *Handler) DisableMFA(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.ClaimsFrom(r.Context())
	if !requireInteractive(w, claims) {
		return
	}
	ctx := r.Context()
	user, err := h.Store.GetUserByID(ctx, claims.UserID)
	if err != nil || !user.TOTPEnabled {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "MFA is not enabled")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Code) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "verification or backup code required")
		return
	}
	if h.failCount(ctx, "mfa-mgmt:"+user.ID) >= 10 {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many failed attempts; try again later")
		return
	}
	if !h.verifySecondFactor(ctx, user, req.Code) {
		h.recordFail(ctx, "mfa-mgmt:"+user.ID)
		httpx.Error(w, http.StatusBadRequest, "invalid_code", "code is incorrect")
		return
	}
	if err := h.Store.DisableTOTP(ctx, user.ID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to disable MFA")
		return
	}
	h.audit(r, audit.Event{ActorID: user.ID, Action: "mfa.disabled", Resource: "user", ResourceID: user.ID})
	httpx.JSON(w, http.StatusOK, map[string]any{"enabled": false})
}
