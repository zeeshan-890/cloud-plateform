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
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c *Client) Login(email, password string) (*TokenResponse, error) {
	var res TokenResponse
	if err := c.do(http.MethodPost, "/auth/login", LoginRequest{
		Email:    email,
		Password: password,
	}, false, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) Logout() error {
	return c.do(http.MethodPost, "/auth/logout", nil, true, nil)
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
