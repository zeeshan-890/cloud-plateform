package api

import (
	"net/http"

	"github.com/jp-cloud/jp/internal/config"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	// Second-factor challenge: set when the account has MFA enabled.
	MFARequired bool   `json:"mfa_required"`
	MFAToken    string `json:"mfa_token"`
	// MustChangePassword: the token is limited to changing the password.
	MustChangePassword bool `json:"must_change_password"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type MeResponse struct {
	User
	UserNested *User `json:"user"`
}

func (m *MeResponse) Effective() User {
	if m.UserNested != nil && (m.UserNested.ID != "" || m.UserNested.Email != "") {
		return *m.UserNested
	}
	return m.User
}

type LoginRequest struct {
	Login    string `json:"login,omitempty"`
	Password string `json:"password,omitempty"`
	MFAToken string `json:"mfa_token,omitempty"`
	TOTPCode string `json:"totp_code,omitempty"`
}

func (c *Client) Login(email, password string) (*TokenResponse, error) {
	var res TokenResponse
	if err := c.do(http.MethodPost, "/auth/login", LoginRequest{
		Login:    email,
		Password: password,
	}, false, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// LoginMFA completes a two-step login with the mfa_token and a TOTP (or backup) code.
func (c *Client) LoginMFA(mfaToken, code string) (*TokenResponse, error) {
	var res TokenResponse
	if err := c.do(http.MethodPost, "/auth/login", LoginRequest{MFAToken: mfaToken, TOTPCode: code}, false, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Logout revokes the access token and the session behind the stored refresh token on the server.
func (c *Client) Logout() error {
	body := map[string]string{}
	if c.cfg.RefreshToken != "" {
		body["refresh_token"] = c.cfg.RefreshToken
	}
	return c.do(http.MethodPost, "/auth/logout", body, true, nil)
}

func (c *Client) Me() (*User, error) {
	var res MeResponse
	if err := c.do(http.MethodGet, "/auth/me", nil, true, &res); err != nil {
		return nil, err
	}
	u := res.Effective()
	return &u, nil
}

func ApplyLogin(cfg *config.Config, tokens *TokenResponse) {
	applyTokens(cfg, tokens)
}

func ApplyToken(cfg *config.Config, token string) {
	cfg.AccessToken = token
	cfg.RefreshToken = ""
}

func applyTokens(cfg *config.Config, tokens *TokenResponse) {
	if tokens.AccessToken != "" {
		cfg.AccessToken = tokens.AccessToken
	}
	if tokens.RefreshToken != "" {
		cfg.RefreshToken = tokens.RefreshToken
	}
}

// ChangePassword changes the caller's password (works with the limited must-change token).
// The server revokes every session afterwards: log in again.
func (c *Client) ChangePassword(current, next string) error {
	return c.do(http.MethodPost, "/auth/password", map[string]string{"current_password": current, "new_password": next}, true, nil)
}
