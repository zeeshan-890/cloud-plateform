package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Instance struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	DeploymentID  *string    `json:"deployment_id,omitempty"`
	Kind          string     `json:"kind"`
	ImageRef      string     `json:"image_ref"`
	Status        string     `json:"status"`
	DesiredState  string     `json:"desired_state"`
	ContainerID   string     `json:"container_id"`
	ContainerName string     `json:"container_name"`
	Slot          string     `json:"slot"`
	Port          int        `json:"port"`
	RestartPolicy string     `json:"restart_policy"`
	Mode          string     `json:"mode"`
	IsPreview     bool       `json:"is_preview"`
	Error         string     `json:"error,omitempty"`
	HealthStatus  string     `json:"health_status"`
	LastHealthAt  *time.Time `json:"last_health_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const instCols = `id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
		       container_id, container_name, slot, port, restart_policy, mode, COALESCE(is_preview,false), error, health_status,
		       last_health_at, created_at, updated_at`

func scanInst(row pgx.Row) (*Instance, error) {
	in := &Instance{}
	err := row.Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
		&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.IsPreview, &in.Error, &in.HealthStatus,
		&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func scanInstRows(rows pgx.Rows) ([]Instance, error) {
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
			&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.IsPreview, &in.Error, &in.HealthStatus,
			&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	if out == nil {
		out = []Instance{}
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, in *Instance) error {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.Kind == "" {
		in.Kind = "container"
	}
	if in.Status == "" {
		in.Status = "desired"
	}
	if in.DesiredState == "" {
		in.DesiredState = "running"
	}
	if in.Slot == "" {
		in.Slot = "node-1"
	}
	if in.Port == 0 {
		in.Port = 8080
	}
	if in.RestartPolicy == "" {
		in.RestartPolicy = "on-failure"
	}
	if in.HealthStatus == "" {
		in.HealthStatus = "unknown"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_instances (
			id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
			container_id, container_name, slot, port, restart_policy, mode, is_preview, error, health_status,
			last_health_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`, in.ID, in.OrgID, in.ProjectID, in.DeploymentID, in.Kind, in.ImageRef, in.Status, in.DesiredState,
		in.ContainerID, in.ContainerName, in.Slot, in.Port, in.RestartPolicy, in.Mode, in.IsPreview, in.Error, in.HealthStatus,
		in.LastHealthAt, in.CreatedAt, in.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+instCols+`
		FROM runtime_instances WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Instance, error) {
	return scanInst(s.pool.QueryRow(ctx, `
		SELECT `+instCols+`
		FROM runtime_instances WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID))
}

func (s *Store) GetByID(ctx context.Context, id string) (*Instance, error) {
	return scanInst(s.pool.QueryRow(ctx, `
		SELECT `+instCols+`
		FROM runtime_instances WHERE id=$1
	`, id))
}

// FindActiveByProject returns the latest non-preview running instance (for production rolling).
func (s *Store) FindActiveByProject(ctx context.Context, orgID, projectID string) (*Instance, error) {
	return scanInst(s.pool.QueryRow(ctx, `
		SELECT `+instCols+`
		FROM runtime_instances
		WHERE org_id=$1 AND project_id=$2 AND desired_state='running' AND COALESCE(is_preview,false)=false
		ORDER BY created_at DESC LIMIT 1
	`, orgID, projectID))
}

func (s *Store) FindByDeploymentID(ctx context.Context, deploymentID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+instCols+`
		FROM runtime_instances WHERE deployment_id=$1::uuid ORDER BY created_at DESC
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

func (s *Store) ListDesiredRunning(ctx context.Context) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+instCols+`
		FROM runtime_instances WHERE desired_state='running'
	`)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

func (s *Store) Update(ctx context.Context, in *Instance) error {
	in.UpdatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE runtime_instances SET
			status=$2, desired_state=$3, container_id=$4, container_name=$5, image_ref=$6,
			deployment_id=$7, error=$8, health_status=$9, last_health_at=$10, mode=$11,
			restart_policy=$12, slot=$13, port=$14, is_preview=$15, updated_at=$16
		WHERE id=$1
	`, in.ID, in.Status, in.DesiredState, in.ContainerID, in.ContainerName, in.ImageRef,
		in.DeploymentID, in.Error, in.HealthStatus, in.LastHealthAt, in.Mode,
		in.RestartPolicy, in.Slot, in.Port, in.IsPreview, in.UpdatedAt)
	return err
}

// GetProjectHostnames returns the public hostnames for a project based on slug, name, and registered domains.
func (s *Store) GetProjectHostnames(ctx context.Context, projectID string, rootDomain string) ([]string, error) {
	var hostnames []string
	seen := make(map[string]bool)

	var slug, name string
	if err := s.pool.QueryRow(ctx, `SELECT slug, name FROM projects WHERE id = $1`, projectID).Scan(&slug, &name); err == nil {
		if rootDomain != "" {
			if slug != "" {
				h := fmt.Sprintf("%s.%s", strings.ToLower(slug), rootDomain)
				if !seen[h] {
					seen[h] = true
					hostnames = append(hostnames, h)
				}
			}
			cleanName := strings.ToLower(name)
			cleanName = strings.ReplaceAll(cleanName, " ", "-")
			cleanName = strings.ReplaceAll(cleanName, "_", "-")
			if cleanName != "" && cleanName != slug {
				h := fmt.Sprintf("%s.%s", cleanName, rootDomain)
				if !seen[h] {
					seen[h] = true
					hostnames = append(hostnames, h)
				}
			}
		}
	}

	if rows, err := s.pool.Query(ctx, `SELECT hostname FROM domains WHERE project_id = $1`, projectID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err == nil && h != "" {
				h = strings.ToLower(strings.TrimSpace(h))
				if !strings.Contains(h, ".") && rootDomain != "" {
					h = fmt.Sprintf("%s.%s", h, rootDomain)
				}
				if !seen[h] {
					seen[h] = true
					hostnames = append(hostnames, h)
				}
			}
		}
	}

	return hostnames, nil
}

// GetContainerEnvs retrieves and decrypts all environment variables and add-on connection strings for a project.
func (s *Store) GetContainerEnvs(ctx context.Context, projectID, env string) ([]string, error) {
	envMap := make(map[string]string)

	// 1. Fetch decrypted secrets from secrets + secret_versions tables
	query := `
		SELECT s.name, sv.ciphertext, sv.nonce
		FROM secrets s
		JOIN secret_versions sv ON sv.secret_id = s.id AND sv.version = s.current_version
		WHERE s.project_id = $1 AND (s.environment = $2 OR s.environment = 'all' OR s.environment = 'production' OR s.environment = 'development')
		ORDER BY CASE
			WHEN s.environment = $2 THEN 1
			WHEN s.environment = 'all' THEN 2
			WHEN s.environment = 'production' THEN 3
			ELSE 4
		END
	`
	rows, err := s.pool.Query(ctx, query, projectID, env)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var ct, nonce []byte
			if err := rows.Scan(&name, &ct, &nonce); err == nil {
				// Don't overwrite if already set by a more specific environment priority
				if _, exists := envMap[name]; !exists {
					if plain, err := decryptSecret(ct, nonce); err == nil {
						// Clean up: skip redundant _INTERNAL / _PUBLIC keys from container env
						if !strings.HasSuffix(name, "_INTERNAL") && !strings.HasSuffix(name, "_PUBLIC") {
							// If value points to localhost (legacy simulate), fix it to the internal Docker service host
							val := strings.TrimSpace(plain)
							val = strings.ReplaceAll(val, "@localhost:5432", "@postgres:5432")
							val = strings.ReplaceAll(val, "@127.0.0.1:5432", "@postgres:5432")
							val = strings.ReplaceAll(val, "@localhost:6379", "@redis:6379")
							val = strings.ReplaceAll(val, "@127.0.0.1:6379", "@redis:6379")
							val = strings.ReplaceAll(val, "@localhost:6380", "@redis:6379")
							val = strings.ReplaceAll(val, "@localhost:27017", "@mongodb:27017")
							val = strings.ReplaceAll(val, "@127.0.0.1:27017", "@mongodb:27017")
							val = strings.ReplaceAll(val, "@localhost:3306", "@mysql:3306")
							envMap[name] = val
						}
					}
				}
			}
		}
	}

	// 2. Fetch and guarantee add-on connection strings from managed_addons
	addonRows, err := s.pool.Query(ctx, `
		SELECT engine, name, metadata
		FROM managed_addons
		WHERE project_id = $1 AND status = 'ready'
	`, projectID)
	if err == nil {
		defer addonRows.Close()
		for addonRows.Next() {
			var engine, addonName string
			var metaJSON []byte
			if err := addonRows.Scan(&engine, &addonName, &metaJSON); err == nil {
				var meta map[string]any
				_ = json.Unmarshal(metaJSON, &meta)

				intUrl := ""
				if u, ok := meta["connection_url"].(string); ok && u != "" {
					intUrl = u
				} else if u, ok := meta["internal_url"].(string); ok && u != "" {
					intUrl = u
				}
				// Fix any localhost reference to container network hostname
				intUrl = strings.ReplaceAll(intUrl, "@localhost:5432", "@postgres:5432")
				intUrl = strings.ReplaceAll(intUrl, "@127.0.0.1:5432", "@postgres:5432")
				intUrl = strings.ReplaceAll(intUrl, "@localhost:6379", "@redis:6379")
				intUrl = strings.ReplaceAll(intUrl, "@127.0.0.1:6379", "@redis:6379")
				intUrl = strings.ReplaceAll(intUrl, "@localhost:6380", "@redis:6379")
				intUrl = strings.ReplaceAll(intUrl, "@localhost:27017", "@mongodb:27017")
				intUrl = strings.ReplaceAll(intUrl, "@127.0.0.1:27017", "@mongodb:27017")
				intUrl = strings.ReplaceAll(intUrl, "@localhost:3306", "@mysql:3306")

				if intUrl != "" {
					switch engine {
					case "postgres", "postgresql":
						if _, ok := envMap["DATABASE_URL"]; !ok || strings.Contains(envMap["DATABASE_URL"], "localhost") {
							envMap["DATABASE_URL"] = intUrl
						}
						if _, ok := envMap["POSTGRES_URL"]; !ok || strings.Contains(envMap["POSTGRES_URL"], "localhost") {
							envMap["POSTGRES_URL"] = intUrl
						}
					case "redis", "valkey", "dragonfly":
						if _, ok := envMap["REDIS_URL"]; !ok || strings.Contains(envMap["REDIS_URL"], "localhost") {
							envMap["REDIS_URL"] = intUrl
						}
					case "mysql", "mariadb":
						if _, ok := envMap["DATABASE_URL"]; !ok || strings.Contains(envMap["DATABASE_URL"], "localhost") {
							envMap["DATABASE_URL"] = intUrl
						}
						if _, ok := envMap["MYSQL_URL"]; !ok || strings.Contains(envMap["MYSQL_URL"], "localhost") {
							envMap["MYSQL_URL"] = intUrl
						}
					case "mongodb", "mongo":
						if _, ok := envMap["MONGODB_URI"]; !ok || strings.Contains(envMap["MONGODB_URI"], "localhost") {
							envMap["MONGODB_URI"] = intUrl
						}
						if _, ok := envMap["MONGO_URL"]; !ok || strings.Contains(envMap["MONGO_URL"], "localhost") {
							envMap["MONGO_URL"] = intUrl
						}
					case "rabbitmq":
						if _, ok := envMap["RABBITMQ_URL"]; !ok || strings.Contains(envMap["RABBITMQ_URL"], "localhost") {
							envMap["RABBITMQ_URL"] = intUrl
						}
					}
				}
			}
		}
	}

	var envList []string
	for k, v := range envMap {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	return envList, nil
}

func decryptSecret(ciphertext, nonce []byte) (string, error) {
	raw := strings.TrimSpace(os.Getenv("SECRETS_MASTER_KEY"))
	if raw == "" {
		raw = "jp-dev-secrets-master-key-32b!!"
	}
	sum := sha256.Sum256([]byte(raw))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
