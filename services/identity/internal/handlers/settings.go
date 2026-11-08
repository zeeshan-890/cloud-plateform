package handlers

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/identity/internal/store"
)

// settingSpec describes one editable platform setting. Only settings that another service
// really consumes are listed (SMTP -> notification, AI keys -> ai).
type settingSpec struct {
	Secret   bool
	Validate func(string) bool
	Hint     string
}

var settingSpecs = map[string]settingSpec{
	"smtp_host":             {Validate: func(v string) bool { return len(v) <= 253 && !strings.ContainsAny(v, " /\\\r\n") }, Hint: "SMTP server hostname"},
	"smtp_port":             {Validate: func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n > 0 && n < 65536 }, Hint: "1-65535"},
	"smtp_username":         {Validate: func(v string) bool { return len(v) <= 254 && !strings.ContainsAny(v, "\r\n") }},
	"smtp_password":         {Secret: true, Validate: func(v string) bool { return len(v) <= 512 && !strings.ContainsAny(v, "\r\n") }},
	"smtp_from":             {Validate: func(v string) bool { _, err := mail.ParseAddress(v); return err == nil && len(v) <= 254 }, Hint: "sender address, e.g. JP Cloud <noreply@example.com>"},
	"smtp_tls":              {Validate: func(v string) bool { return v == "starttls" || v == "tls" || v == "none" }, Hint: "starttls | tls | none"},
	"ai_openai_api_key":     {Secret: true, Validate: func(v string) bool { return len(v) <= 512 && !strings.ContainsAny(v, " \r\n") }},
	"ai_openrouter_api_key": {Secret: true, Validate: func(v string) bool { return len(v) <= 512 && !strings.ContainsAny(v, " \r\n") }},
	"ai_gemini_api_key":     {Secret: true, Validate: func(v string) bool { return len(v) <= 512 && !strings.ContainsAny(v, " \r\n") }},
	"ai_base_url": {Validate: func(v string) bool {
		u, err := url.Parse(v)
		return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && len(v) <= 300
	}},
	"ai_model": {Validate: func(v string) bool { return len(v) <= 100 && !strings.ContainsAny(v, "\r\n") }},
}

// SettingKeys returns the editable keys (sorted).
func SettingKeys() []string {
	keys := make([]string, 0, len(settingSpecs))
	for k := range settingSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (h *Handler) settingsKey() []byte { return h.EncKey }

// requireAdmin verifies the caller is a platform admin: the `adm` claim AND the database flag.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims, ok := middleware.ClaimsFrom(r.Context())
	if !ok || !claims.Admin {
		httpx.Error(w, http.StatusForbidden, "forbidden", "platform administrator required")
		return "", false
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil || !user.IsAdmin {
		httpx.Error(w, http.StatusForbidden, "forbidden", "platform administrator required")
		return "", false
	}
	return user.ID, true
}

func (h *Handler) loadSettings(ctx context.Context) (map[string]store.SettingRow, error) {
	rows, err := h.Store.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]store.SettingRow, len(rows))
	for _, r := range rows {
		m[r.Key] = r
	}
	return m, nil
}

// GetSettings returns the persisted settings with secret values masked.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	rows, err := h.loadSettings(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load settings")
		return
	}
	out := map[string]any{}
	for key, spec := range settingSpecs {
		entry := map[string]any{"secret": spec.Secret, "set": false, "hint": spec.Hint}
		if row, ok := rows[key]; ok {
			entry["set"] = true
			entry["updated_at"] = row.UpdatedAt
			if !spec.Secret {
				entry["value"] = row.Value
			}
		}
		out[key] = entry
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"settings": out, "editable_keys": SettingKeys()})
}

// PatchSettings updates settings. A key mapped to "" (or null) is removed (falls back to env).
func (h *Handler) PatchSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	var req map[string]*string
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "body must be a JSON object of setting keys to string values")
		return
	}
	var upserts []store.SettingRow
	var deletes []string
	for key, val := range req {
		spec, known := settingSpecs[key]
		if !known {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "unknown or read-only setting: "+key)
			return
		}
		if val == nil || strings.TrimSpace(*val) == "" {
			deletes = append(deletes, key)
			continue
		}
		v := strings.TrimSpace(*val)
		if !spec.Validate(v) {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid value for "+key)
			return
		}
		stored := v
		if spec.Secret {
			sealed, err := secretbox.Seal(h.settingsKey(), v, "setting:"+key)
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal", "failed to protect secret")
				return
			}
			stored = sealed
		}
		upserts = append(upserts, store.SettingRow{Key: key, Value: stored, IsSecret: spec.Secret})
	}
	if len(upserts)+len(deletes) == 0 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "no settings provided")
		return
	}
	if err := h.Store.UpsertSettings(r.Context(), upserts, actor); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to save settings")
		return
	}
	if err := h.Store.DeleteSettings(r.Context(), deletes); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to save settings")
		return
	}
	changed := make([]string, 0, len(req))
	for k := range req {
		changed = append(changed, k)
	}
	sort.Strings(changed)
	h.audit(r, audit.Event{ActorID: actor, Action: "system.settings_updated", Resource: "system", ResourceID: "settings",
		Metadata: map[string]any{"keys": changed}}) // never log values
	h.GetSettings(w, r)
}

// InternalSettings returns all settings decrypted for consuming services (notification, ai).
func (h *Handler) InternalSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.loadSettings(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load settings")
		return
	}
	out := map[string]string{}
	for key, row := range rows {
		spec, known := settingSpecs[key]
		if !known {
			continue
		}
		v := row.Value
		if spec.Secret {
			plain, err := secretbox.Open(h.settingsKey(), row.Value, "setting:"+key)
			if err != nil {
				h.log().Error("setting cannot be decrypted (wrong IDENTITY_ENC_KEY?)", "key", key)
				continue
			}
			v = plain
		}
		out[key] = v
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"settings": out})
}

var defaultPasswords = []string{"admin", "password", "changeme", "admin123", "12345678", "password123", "jp-cloud", "jpcloud123"}

// InternalSecurityStats feeds the gateway's /system/security report with real account state.
func (h *Handler) InternalSecurityStats(w http.ResponseWriter, r *http.Request) {
	st, err := h.Store.Stats(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to compute stats")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	admins, _ := h.Store.ListAdmins(ctx, 5)
	weak := []string{}
	for _, a := range admins {
		candidates := append([]string{a.Email, strings.SplitN(a.Email, "@", 2)[0]}, defaultPasswords...)
		for _, c := range candidates {
			if ctx.Err() != nil {
				break
			}
			if len(c) >= 1 && len(c) <= maxPasswordBytes && bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(c)) == nil {
				weak = append(weak, a.Email)
				break
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"users":                   st.Users,
		"admins":                  st.Admins,
		"mfa_enabled":             st.MFAEnabled,
		"admins_mfa_enabled":      st.AdminsMFAEnabled,
		"bootstrap_initialized":   st.BootstrapDone,
		"registration_mode":       h.mode(),
		"legacy_plaintext_totp":   st.LegacyPlainMFA,
		"admins_default_password": weak,
	})
}
