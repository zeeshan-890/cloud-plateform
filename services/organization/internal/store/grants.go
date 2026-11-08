package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jp-cloud/go-common/membership"
)

// Grant is one user's access to one project.
type Grant struct {
	OrgID         string                               `json:"org_id,omitempty"`
	ProjectID     string                               `json:"project_id"`
	UserID        string                               `json:"user_id,omitempty"`
	Preset        string                               `json:"preset"`
	Areas         map[membership.Area]membership.Level `json:"areas"`
	SecretsReveal bool                                 `json:"secrets_reveal"`
	GrantedBy     string                               `json:"granted_by,omitempty"`
	UpdatedAt     time.Time                            `json:"updated_at"`
}

// Access converts a grant to the access document served to other services.
func (g *Grant) Access(role string) *membership.Access {
	return &membership.Access{Role: membership.Role(role), Areas: g.Areas, SecretsReveal: g.SecretsReveal}
}

func scanGrant(row interface{ Scan(...any) error }) (*Grant, error) {
	g := &Grant{}
	var raw []byte
	var by *string
	if err := row.Scan(&g.OrgID, &g.ProjectID, &g.UserID, &g.Preset, &raw, &g.SecretsReveal, &by, &g.UpdatedAt); err != nil {
		return nil, err
	}
	g.Areas = map[membership.Area]membership.Level{}
	_ = json.Unmarshal(raw, &g.Areas)
	if by != nil {
		g.GrantedBy = *by
	}
	return g, nil
}

const grantCols = `org_id::text, project_id::text, user_id::text, preset, areas, secrets_reveal, granted_by::text, updated_at`

func (s *PG) GetGrant(ctx context.Context, orgID, projectID, userID string) (*Grant, error) {
	g, err := scanGrant(s.pool.QueryRow(ctx, `SELECT `+grantCols+` FROM project_grants WHERE org_id=$1 AND project_id=$2 AND user_id=$3`, orgID, projectID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (s *PG) ListGrantsForUser(ctx context.Context, orgID, userID string) ([]Grant, error) {
	return s.queryGrants(ctx, `SELECT `+grantCols+` FROM project_grants WHERE org_id=$1 AND user_id=$2 ORDER BY project_id`, orgID, userID)
}

func (s *PG) ListGrantsForOrg(ctx context.Context, orgID string) ([]Grant, error) {
	return s.queryGrants(ctx, `SELECT `+grantCols+` FROM project_grants WHERE org_id=$1 ORDER BY user_id, project_id LIMIT 20000`, orgID)
}

func (s *PG) queryGrants(ctx context.Context, q string, args ...any) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// ReplaceGrants atomically replaces ALL of a user's grants in the org.
func (s *PG) ReplaceGrants(ctx context.Context, orgID, userID, grantedBy string, grants []Grant) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM project_grants WHERE org_id=$1 AND user_id=$2`, orgID, userID); err != nil {
		return err
	}
	var by any
	if grantedBy != "" {
		by = grantedBy
	}
	for _, g := range grants {
		areas, _ := json.Marshal(g.Areas)
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_grants (org_id, project_id, user_id, preset, areas, secrets_reveal, granted_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, orgID, g.ProjectID, userID, g.Preset, areas, g.SecretsReveal, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PG) DeleteGrantsForProject(ctx context.Context, projectID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM project_grants WHERE project_id=$1`, projectID)
	return err
}

// AddMember inserts a member (no-op if already present).
func (s *PG) AddMember(ctx context.Context, orgID, userID, role string, managed bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role, managed, joined_at) VALUES ($1,$2,$3,$4,NOW()) ON CONFLICT DO NOTHING`, orgID, userID, role, managed)
	return err
}

func (s *PG) IsManagedMember(ctx context.Context, orgID, userID string) (bool, error) {
	var m bool
	err := s.pool.QueryRow(ctx, `SELECT managed FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&m)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return m, err
}
