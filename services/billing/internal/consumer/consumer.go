package consumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jp-cloud/billing/internal/store"
	"github.com/jp-cloud/events"
	"github.com/redis/go-redis/v9"
)

type Runner struct {
	Redis *redis.Client
	Store *store.Store
	Log   *slog.Logger
}

func (r *Runner) Run(ctx context.Context) {
	for _, topic := range []string{events.TopicDeploy, events.TopicBuild} {
		_ = events.EnsureGroup(ctx, r.Redis, topic, events.BillingConsumerGroup)
	}
	consumer := "billing-1"
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		for _, topic := range []string{events.TopicDeploy, events.TopicBuild} {
			msgs, err := events.ReadGroup(ctx, r.Redis, topic, events.BillingConsumerGroup, consumer, 5, 2*time.Second)
			if err != nil {
				continue
			}
			for _, msg := range msgs {
				_ = r.handle(ctx, topic, msg)
				_ = events.Ack(ctx, r.Redis, topic, events.BillingConsumerGroup, msg.ID)
			}
		}
	}
}

func (r *Runner) handle(ctx context.Context, topic string, msg redis.XMessage) error {
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		return err
	}
	orgID := env.OrgID
	if orgID == "" {
		if v, ok := env.Payload["org_id"].(string); ok {
			orgID = v
		}
	}
	if orgID == "" {
		return nil
	}
	projectID, _ := env.Payload["project_id"].(string)
	meta, _ := json.Marshal(env.Payload)

	switch {
	case topic == events.TopicBuild && (env.Type == events.TypeBuildSucceeded || env.Type == events.TypeBuildFailed || env.Type == events.TypeBuildStarted):
		// Stub: 2 build minutes per build event
		e := &store.UsageEvent{
			OrgID: orgID, Metric: "build_minutes", Quantity: 2, Unit: "minutes",
			Source: "stream:" + topic, Meta: meta,
		}
		if projectID != "" {
			e.ProjectID = &projectID
		}
		return r.Store.Insert(ctx, e)
	case topic == events.TopicDeploy && env.Type == events.TypeDeployUpdated:
		status, _ := env.Payload["status"].(string)
		if strings.ToLower(status) != "ready" {
			return nil
		}
		// Stub: 0.1 runtime hours per successful deploy
		e := &store.UsageEvent{
			OrgID: orgID, Metric: "runtime_hours", Quantity: 0.1, Unit: "hours",
			Source: "stream:" + topic, Meta: meta,
		}
		if projectID != "" {
			e.ProjectID = &projectID
		}
		return r.Store.Insert(ctx, e)
	}
	return nil
}
