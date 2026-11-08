package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Node is another server of the installation that the runtime can place whole projects on. The runtime reaches
// that server's Docker daemon through the node agent at Addr (host:port on the private network).
type Node struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Addr        string    `json:"addr"`
	PrivateIP   string    `json:"private_ip"`
	TokenSealed string    `json:"-"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const nodeCols = `id, name, addr, private_ip, token_sealed, enabled, created_at, updated_at`

func scanNode(row pgx.Row) (*Node, error) {
	n := &Node{}
	err := row.Scan(&n.ID, &n.Name, &n.Addr, &n.PrivateIP, &n.TokenSealed, &n.Enabled, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// UpsertNode creates or updates a node by name and returns it.
func (s *Store) UpsertNode(ctx context.Context, n *Node) (*Node, error) {
	return scanNode(s.pool.QueryRow(ctx, `
		INSERT INTO runtime_nodes (id, name, addr, private_ip, token_sealed, enabled)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (name) DO UPDATE SET addr=EXCLUDED.addr, private_ip=EXCLUDED.private_ip,
			token_sealed=EXCLUDED.token_sealed, enabled=EXCLUDED.enabled, updated_at=now()
		RETURNING `+nodeCols, uuid.NewString(), n.Name, n.Addr, n.PrivateIP, n.TokenSealed, n.Enabled))
}

func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	return scanNode(s.pool.QueryRow(ctx, `SELECT `+nodeCols+` FROM runtime_nodes WHERE id=$1`, id))
}

func (s *Store) GetNodeByName(ctx context.Context, name string) (*Node, error) {
	return scanNode(s.pool.QueryRow(ctx, `SELECT `+nodeCols+` FROM runtime_nodes WHERE name=$1`, name))
}

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+nodeCols+` FROM runtime_nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// DeleteNode removes a node. The caller must have verified nothing runs there or is placed there.
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM runtime_nodes WHERE id=$1`, id)
	return err
}

// CountInstancesOnNode counts instance rows that run on a node.
func (s *Store) CountInstancesOnNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM runtime_instances WHERE node_id=$1`, nodeID).Scan(&n)
	return n, err
}

// CountPlacementsOnNode counts projects placed on a node.
func (s *Store) CountPlacementsOnNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM runtime_project_placement WHERE node_id=$1`, nodeID).Scan(&n)
	return n, err
}

// Placement returns the node a project is placed on ("" = the control plane).
func (s *Store) Placement(ctx context.Context, projectID string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT node_id::text FROM runtime_project_placement WHERE project_id=$1`, projectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// SetPlacement places a project on a node; nodeID "" puts it back on the control plane.
func (s *Store) SetPlacement(ctx context.Context, projectID, nodeID string) error {
	if nodeID == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM runtime_project_placement WHERE project_id=$1`, projectID)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_project_placement (project_id, node_id) VALUES ($1,$2)
		ON CONFLICT (project_id) DO UPDATE SET node_id=EXCLUDED.node_id, updated_at=now()`, projectID, nodeID)
	return err
}

// ListPlacements returns project_id -> node_id for every placed project.
func (s *Store) ListPlacements(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT project_id::text, node_id::text FROM runtime_project_placement`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, n string
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

// DeletePlacement drops a project's placement (project deletion).
func (s *Store) DeletePlacement(ctx context.Context, projectID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM runtime_project_placement WHERE project_id=$1`, projectID)
	return err
}
