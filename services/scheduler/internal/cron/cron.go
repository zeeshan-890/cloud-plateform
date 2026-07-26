package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	indexKey = "jp:cron:index"
	keyPref  = "jp:cron:"
)

type Schedule struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Cron        string    `json:"cron"` // simplified: "@hourly" | "@every <duration>" | "minute hour * * *"
	ImageRef    string    `json:"image_ref"`
	Enabled     bool      `json:"enabled"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	NextRunAt   time.Time `json:"next_run_at"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct{ rdb *redis.Client }

func NewStore(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

func (s *Store) Create(ctx context.Context, sch *Schedule) error {
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
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, keyPref+sch.ID, raw, 0)
	pipe.SAdd(ctx, indexKey, sch.ID)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Schedule, error) {
	ids, err := s.rdb.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	out := []Schedule{}
	for _, id := range ids {
		sch, err := s.Get(ctx, id)
		if err != nil || sch == nil {
			continue
		}
		if sch.OrgID == orgID && sch.ProjectID == projectID {
			out = append(out, *sch)
		}
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, id string) (*Schedule, error) {
	raw, err := s.rdb.Get(ctx, keyPref+id).Bytes()
	if err == redis.Nil {
		return nil, nil
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

func (s *Store) Delete(ctx context.Context, orgID, projectID, id string) error {
	sch, err := s.Get(ctx, id)
	if err != nil || sch == nil {
		return fmt.Errorf("not found")
	}
	if sch.OrgID != orgID || sch.ProjectID != projectID {
		return fmt.Errorf("not found")
	}
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, keyPref+id)
	pipe.SRem(ctx, indexKey, id)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Store) MarkRun(ctx context.Context, sch *Schedule, now time.Time) error {
	sch.LastRunAt = &now
	sch.NextRunAt = NextRun(sch.Cron, now)
	raw, err := json.Marshal(sch)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, keyPref+sch.ID, raw, 0).Err()
}

func (s *Store) Due(ctx context.Context, now time.Time) ([]Schedule, error) {
	ids, err := s.rdb.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	out := []Schedule{}
	for _, id := range ids {
		sch, err := s.Get(ctx, id)
		if err != nil || sch == nil || !sch.Enabled {
			continue
		}
		if !sch.NextRunAt.After(now) {
			out = append(out, *sch)
		}
	}
	return out, nil
}

// NextRun supports: @hourly, @daily, @every 5m / 1h, or "M H * * *" (minute hour).
func NextRun(expr string, from time.Time) time.Time {
	expr = strings.TrimSpace(strings.ToLower(expr))
	if expr == "" {
		expr = "@hourly"
	}
	switch {
	case expr == "@hourly":
		return from.Truncate(time.Hour).Add(time.Hour)
	case expr == "@daily":
		y, m, d := from.Date()
		return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	case strings.HasPrefix(expr, "@every "):
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every ")))
		if err != nil || d <= 0 {
			d = time.Hour
		}
		return from.Add(d)
	default:
		// "minute hour * * *"
		parts := strings.Fields(expr)
		if len(parts) >= 2 {
			min, _ := strconv.Atoi(parts[0])
			hour, _ := strconv.Atoi(parts[1])
			if min < 0 || min > 59 {
				min = 0
			}
			if hour < 0 || hour > 23 {
				hour = 0
			}
			cand := time.Date(from.Year(), from.Month(), from.Day(), hour, min, 0, 0, time.UTC)
			if !cand.After(from) {
				cand = cand.Add(24 * time.Hour)
			}
			return cand
		}
		return from.Add(time.Hour)
	}
}
