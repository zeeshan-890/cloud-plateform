package config

import "testing"

func TestProductionRequiresSecrets(t *testing.T) {
	t.Setenv("JP_ENV", "production")
	t.Setenv("JWT_SECRET", "dev-secret-change-me")
	t.Setenv("INTERNAL_TOKEN", "x")
	if _, err := Load("1"); err == nil {
		t.Fatal("weak JWT secret must be refused in production")
	}
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef0123")
	t.Setenv("INTERNAL_TOKEN", "")
	if _, err := Load("1"); err == nil {
		t.Fatal("missing INTERNAL_TOKEN must be refused in production")
	}
	t.Setenv("INTERNAL_TOKEN", "x")
	if _, err := Load("1"); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationModeAndDefaults(t *testing.T) {
	t.Setenv("JP_ENV", "development")
	t.Setenv("REGISTRATION_MODE", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 172.16.0.0/12")
	c, err := Load("1")
	if err != nil {
		t.Fatal(err)
	}
	if c.RegistrationMode != "invite" || c.GitHubWebhookSecret != "" || len(c.TrustedProxies) != 2 {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("REGISTRATION_MODE", "anyone")
	if _, err := Load("1"); err == nil {
		t.Fatal("invalid registration mode must be refused")
	}
}
