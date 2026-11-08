package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	indexKey      = "jp:cron:index"
	keyPref       = "jp:cron:"
	projIndexPref = "jp:cron:proj:"
	firePref      = "jp:cron:fire:"

	MaxPerProject = 50
)

var (
	ErrNotFound = errors.New("not found")
	ErrLimit    = errors.New("schedule limit reached for project")
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:-]{0,99}$`)
)

// Schedule is a cron entry that runs a command in a one-off container.
type Schedule struct {
	ID             string     `json:"id"`
	OrgID          string     `json:"org_id"`
	ProjectID      string     `json:"project_id"`
	Name           string     `json:"name"`
	Cron           string     `json:"cron"`
	ImageRef       string     `json:"image_ref"`
	Command        []string   `json:"command"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Enabled        bool       `json:"enabled"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastRunID      string     `json:"last_run_id,omitempty"`
	NextRunAt      time.Time  `json:"next_run_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Validate checks user supplied fields (everything is untrusted).
func Validate(s *Schedule) error {
	s.Name = strings.TrimSpace(s.Name)
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("name must be 1-100 characters (letters, digits, space, _ . : -)")
	}
	if s.Cron == "" {
		s.Cron = "@hourly"
	}
	if _, err := Parse(s.Cron); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	if len(s.Command) > 32 {
		return fmt.Errorf("command has too many arguments")
	}
	for _, a := range s.Command {
		if len(a) > 2048 || strings.ContainsRune(a, 0) {
			return fmt.Errorf("invalid command argument")
		}
	}
	if s.TimeoutSeconds < 0 || s.TimeoutSeconds > 3600 {
		return fmt.Errorf("timeout_seconds must be 0-3600")
	}
	return nil
}

type Store struct{ rdb *redis.Client }

func NewStore(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

func (s *Store) Create(ctx context.Context, sch *Schedule) error {
	if err := Validate(sch); err != nil {
		return err
	}
	n, err := s.rdb.SCard(ctx, projIndexPref+sch.ProjectID).Result()
	if err != nil {
		return err
	}
	if n >= MaxPerProject {
		return ErrLimit
	}
	if sch.ID == "" {
		sch.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	sch.CreatedAt = now
	sch.Enabled = true
	sch.NextRunAt = NextRun(sch.Cron, now)
	raw, err := json.Marshal(sch)
	if err != nil {
		return err
	}
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, keyPref+sch.ID, raw, 0)
	pipe.SAdd(ctx, indexKey, sch.ID)
	pipe.SAdd(ctx, projIndexPref+sch.ProjectID, sch.ID)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Store) mget(ctx context.Context, ids []string) ([]Schedule, error) {
	out := []Schedule{}
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = keyPref + id
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue
		}
		var sch Schedule
		if json.Unmarshal([]byte(str), &sch) == nil {
			out = append(out, sch)
		}
	}
	return out, nil
}

// List returns the project's schedules.
func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Schedule, error) {
	ids, err := s.rdb.SMembers(ctx, projIndexPref+projectID).Result()
	if err != nil {
		return nil, err
	}
	all, err := s.mget(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, sch := range all {
		if sch.OrgID == orgID {
			out = append(out, sch)
		}
	}
	return out, nil
}

// Get returns a schedule by id.
func (s *Store) Get(ctx context.Context, id string) (*Schedule, error) {
	raw, err := s.rdb.Get(ctx, keyPref+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sch Schedule
	if err := json.Unmarshal(raw, &sch); err != nil {
		return nil, err
	}
	return &sch, nil
}

// Delete removes a schedule scoped to org+project.
func (s *Store) Delete(ctx context.Context, orgID, projectID, id string) error {
	sch, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if sch.OrgID != orgID || sch.ProjectID != projectID {
		return ErrNotFound
	}
	return s.remove(ctx, sch)
}

func (s *Store) remove(ctx context.Context, sch *Schedule) error {
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, keyPref+sch.ID)
	pipe.SRem(ctx, indexKey, sch.ID)
	pipe.SRem(ctx, projIndexPref+sch.ProjectID, sch.ID)
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteProject removes every schedule of a project (project delete cascade).
func (s *Store) DeleteProject(ctx context.Context, projectID string) (int, error) {
	ids, err := s.rdb.SMembers(ctx, projIndexPref+projectID).Result()
	if err != nil {
		return 0, err
	}
	list, err := s.mget(ctx, ids)
	if err != nil {
		return 0, err
	}
	for i := range list {
		if err := s.remove(ctx, &list[i]); err != nil {
			return i, err
		}
	}
	// also drop dangling index members
	if len(ids) > len(list) {
		pipe := s.rdb.TxPipeline()
		for _, id := range ids {
			pipe.SRem(ctx, indexKey, id)
		}
		pipe.Del(ctx, projIndexPref+projectID)
		_, _ = pipe.Exec(ctx)
	}
	return len(list), nil
}

// MarkRun records a firing and schedules the next activation.
func (s *Store) MarkRun(ctx context.Context, sch *Schedule, now time.Time, runID string) error {
	sch.LastRunAt = &now
	sch.LastRunID = runID
	sch.NextRunAt = NextRun(sch.Cron, now)
	raw, err := json.Marshal(sch)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, keyPref+sch.ID, raw, 0).Err()
}

// Claim returns true for exactly one scheduler replica per (schedule, activation).
func (s *Store) Claim(ctx context.Context, sch *Schedule) (bool, error) {
	key := fmt.Sprintf("%s%s:%d", firePref, sch.ID, sch.NextRunAt.Unix())
	return s.rdb.SetNX(ctx, key, "1", 6*time.Hour).Result()
}

// Due returns enabled schedules whose activation time has passed.
func (s *Store) Due(ctx context.Context, now time.Time) ([]Schedule, error) {
	ids, err := s.rdb.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	all, err := s.mget(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := []Schedule{}
	for _, sch := range all {
		if sch.Enabled && !sch.NextRunAt.IsZero() && !sch.NextRunAt.After(now) {
			out = append(out, sch)
		}
	}
	return out, nil
}
