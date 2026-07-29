package catalog

// Item describes a one-click add-on engine in the marketplace catalog.
type Item struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Category        string   `json:"category"` // database | cache | queue
	Description     string   `json:"description"`
	SecretKeys      []string `json:"secret_keys"`
	ModesSupported  []string `json:"modes_supported"`
}

// All returns the v1 engine catalog.
func All() []Item {
	return []Item{
		{
			ID: "postgres", Name: "PostgreSQL", Category: "database",
			Description: "Managed Postgres schema/role on the platform cluster (or simulate).",
			SecretKeys: []string{"POSTGRES_URL_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
		{
			ID: "mysql", Name: "MySQL", Category: "database",
			Description: "MySQL database + user on a shared MySQL instance (or simulate).",
			SecretKeys: []string{"MYSQL_URL_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
		{
			ID: "mongodb", Name: "MongoDB", Category: "database",
			Description: "MongoDB database credentials (shared cluster namespace or simulate).",
			SecretKeys: []string{"MONGODB_URI_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
		{
			ID: "redis", Name: "Redis", Category: "cache",
			Description: "Tenant Redis DB index on the add-on Redis (not platform bus Redis).",
			SecretKeys: []string{"REDIS_URL_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
		{
			ID: "rabbitmq", Name: "RabbitMQ", Category: "queue",
			Description: "AMQP vhost + user on shared RabbitMQ (or simulate).",
			SecretKeys: []string{"AMQP_URL_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
		{
			ID: "kafka", Name: "Kafka", Category: "queue",
			Description: "Kafka brokers + topic prefix (Redpanda-compatible; ACL stub in shared mode).",
			SecretKeys: []string{"KAFKA_BROKERS_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
		{
			ID: "sqlite", Name: "SQLite", Category: "database",
			Description: "File-backed SQLite path under ADDON_SQLITE_DIR (or simulate path).",
			SecretKeys: []string{"SQLITE_PATH_{NAME}"}, ModesSupported: []string{"simulate", "shared"},
		},
	}
}

// ValidEngine reports whether engine is in the catalog.
func ValidEngine(engine string) bool {
	for _, it := range All() {
		if it.ID == engine {
			return true
		}
	}
	return false
}

// SecretName builds the secret key for an engine + instance name.
func SecretName(engine, name string) string {
	safe := sanitizeName(name)
	switch engine {
	case "postgres":
		return "POSTGRES_URL_" + safe
	case "mysql":
		return "MYSQL_URL_" + safe
	case "mongodb":
		return "MONGODB_URI_" + safe
	case "redis":
		return "REDIS_URL_" + safe
	case "rabbitmq":
		return "AMQP_URL_" + safe
	case "kafka":
		return "KAFKA_BROKERS_" + safe
	case "sqlite":
		return "SQLITE_PATH_" + safe
	default:
		return "ADDON_URL_" + safe
	}
}

func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, '_')
		}
	}
	s := string(out)
	if s == "" {
		s = "APP"
	}
	// upper
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
