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
	BillingURL      string
	GitHubWebhookSecret string
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
		BillingURL:          getEnv("BILLING_URL", "http://localhost:8020"),
		GitHubWebhookSecret: getEnv("GITHUB_WEBHOOK_SECRET", "dev-webhook-secret"),
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

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	return cfg, nil
}

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
