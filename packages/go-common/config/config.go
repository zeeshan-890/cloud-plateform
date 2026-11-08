package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds shared environment configuration for jp services.
type Config struct {
	Env            string
	LogLevel       string
	HTTPPort       string
	DatabaseURL    string
	RedisURL       string
	JWTSecret      string
	JWTAccessTTL   time.Duration
	JWTRefreshTTL  time.Duration
	CORSOrigins    []string
	PublicBaseURL  string
	IdentityURL     string
	OrganizationURL string
	ProjectURL      string
	NotificationURL string
	RepositoryURL   string
	DeploymentURL   string
	BuildURL        string
	RegistryURL     string
	RuntimeURL      string
	SchedulerURL    string
	DomainURL       string
	CertificateURL  string
	SecretURL       string
	LoggingURL      string
	MetricsURL      string
	StorageURL      string
	DatabaseURLSvc  string
	AIURL           string
	AddonURL        string
	GitHubWebhookSecret string

	// TrustedProxies are the CIDRs/IPs (env TRUSTED_PROXIES) whose X-Forwarded-For is honoured.
	TrustedProxies []string
	// PublicDashboardURL is the externally reachable dashboard base URL (env PUBLIC_DASHBOARD_URL).
	PublicDashboardURL string
	// RegistrationMode is closed | invite | open (env REGISTRATION_MODE, default invite).
	RegistrationMode string
}

// Load reads configuration from environment variables with sensible defaults.
func Load(servicePortDefault string) (*Config, error) {
	cfg := &Config{
		Env:             getEnv("JP_ENV", "development"),
		LogLevel:        getEnv("JP_LOG_LEVEL", "info"),
		HTTPPort:        getEnv("PORT", servicePortDefault),
		DatabaseURL:     getEnv("DATABASE_URL", "postgres://jp:jp_dev_password@localhost:5432/jp?sslmode=disable"),
		RedisURL:        getEnv("REDIS_URL", "redis://localhost:6379/0"),
		JWTSecret:       getEnv("JWT_SECRET", "dev-secret-change-me"),
		CORSOrigins:     splitCSV(getEnv("CORS_ORIGINS", "http://localhost:3000,http://localhost:5173")),
		PublicBaseURL:   getEnv("PUBLIC_BASE_URL", "http://localhost:8000"),
		IdentityURL:         getEnv("IDENTITY_URL", "http://localhost:8001"),
		OrganizationURL:     getEnv("ORGANIZATION_URL", "http://localhost:8002"),
		ProjectURL:          getEnv("PROJECT_URL", "http://localhost:8003"),
		NotificationURL:     getEnv("NOTIFICATION_URL", "http://localhost:8004"),
		RepositoryURL:       getEnv("REPOSITORY_URL", "http://localhost:8005"),
		DeploymentURL:       getEnv("DEPLOYMENT_URL", "http://localhost:8006"),
		BuildURL:            getEnv("BUILD_URL", "http://localhost:8007"),
		RegistryURL:         getEnv("REGISTRY_URL", "http://localhost:8009"),
		RuntimeURL:          getEnv("RUNTIME_URL", "http://localhost:8010"),
		SchedulerURL:        getEnv("SCHEDULER_URL", "http://localhost:8011"),
		DomainURL:           getEnv("DOMAIN_URL", "http://localhost:8012"),
		CertificateURL:      getEnv("CERTIFICATE_URL", "http://localhost:8013"),
		SecretURL:           getEnv("SECRET_URL", "http://localhost:8014"),
		LoggingURL:          getEnv("LOGGING_URL", "http://localhost:8015"),
		MetricsURL:          getEnv("METRICS_URL", "http://localhost:8016"),
		StorageURL:          getEnv("STORAGE_URL", "http://localhost:8017"),
		DatabaseURLSvc:      getEnv("DATABASE_SVC_URL", "http://localhost:8018"),
		AIURL:               getEnv("AI_URL", "http://localhost:8019"),
		AddonURL:            getEnv("ADDON_SVC_URL", "http://localhost:8021"),
		// No built-in default: webhooks are accepted only with a per-repo or an explicit global secret.
		GitHubWebhookSecret: getEnv("GITHUB_WEBHOOK_SECRET", ""),
		TrustedProxies:      splitCSV(getEnv("TRUSTED_PROXIES", "")),
		PublicDashboardURL:  strings.TrimRight(getEnv("PUBLIC_DASHBOARD_URL", ""), "/"),
		RegistrationMode:    strings.ToLower(getEnv("REGISTRATION_MODE", "invite")),
	}

	var err error
	cfg.JWTAccessTTL, err = time.ParseDuration(getEnv("JWT_ACCESS_TTL", "15m"))
	if err != nil {
		return nil, fmt.Errorf("JWT_ACCESS_TTL: %w", err)
	}
	cfg.JWTRefreshTTL, err = time.ParseDuration(getEnv("JWT_REFRESH_TTL", "168h"))
	if err != nil {
		return nil, fmt.Errorf("JWT_REFRESH_TTL: %w", err)
	}

	switch cfg.RegistrationMode {
	case "closed", "invite", "open":
	default:
		return nil, fmt.Errorf("REGISTRATION_MODE must be closed, invite or open (got %q)", cfg.RegistrationMode)
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if strings.EqualFold(cfg.Env, "production") {
		if len(cfg.JWTSecret) < 32 || strings.Contains(cfg.JWTSecret, "change-me") || cfg.JWTSecret == "dev-secret-change-me" {
			return nil, fmt.Errorf("JWT_SECRET must be a random value of at least 32 characters in production")
		}
		if strings.TrimSpace(os.Getenv("INTERNAL_TOKEN")) == "" {
			return nil, fmt.Errorf("INTERNAL_TOKEN is required in production")
		}
	}
	return cfg, nil
}

// IsProduction reports whether JP_ENV is production.
func (c *Config) IsProduction() bool { return strings.EqualFold(c.Env, "production") }

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GetEnvBool reads a boolean env var.
func GetEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
