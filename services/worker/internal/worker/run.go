package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jp-cloud/events"
	"github.com/redis/go-redis/v9"
)

// RunConfig controls the consumer loop.
type RunConfig struct {
	Redis        *redis.Client
	Consumer     string        // unique per replica (hostname)
	Concurrency  int           // parallel builds
	ClaimIdle    time.Duration // pending longer than this => reclaimed (default 10m)
	ClaimEvery   time.Duration
	MaxAttempts  int           // deliveries before the job is dead-lettered as failed (default 3)
	ReadBlock    time.Duration // XREADGROUP block (default 5s)
	DrainTimeout time.Duration // how long in-flight builds may finish after shutdown (default 10m)
}

// Run consumes build jobs until ctx is cancelled (SIGTERM), then drains: no new jobs are
// read, running builds get DrainTimeout to finish, afterwards they are cancelled and stay
// pending so another worker picks them up.
func (w *Worker) Run(ctx context.Context, rc RunConfig) error {
	w.defaults()
	if rc.Concurrency < 1 {
		rc.Concurrency = 1
	}
	if rc.ClaimIdle <= 0 {
		rc.ClaimIdle = 10 * time.Minute
	}
	if rc.ClaimEvery <= 0 {
		rc.ClaimEvery = 30 * time.Second
	}
	if rc.MaxAttempts <= 0 {
		rc.MaxAttempts = 3
	}
	if rc.ReadBlock <= 0 {
		rc.ReadBlock = 5 * time.Second
	}
	if rc.DrainTimeout <= 0 {
		rc.DrainTimeout = 10 * time.Minute
	}
	if err := events.EnsureGroup(ctx, rc.Redis, events.TopicBuild, events.BuildConsumerGroup); err != nil {
		return err
	}

	// jobCtx outlives ctx so in-flight builds can drain; it is cancelled after DrainTimeout.
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()
	sem := make(chan struct{}, rc.Concurrency)
	var wg sync.WaitGroup
	lastClaim := time.Time{}

	for ctx.Err() == nil {
		var msgs []redis.XMessage
		if time.Since(lastClaim) >= rc.ClaimEvery {
			lastClaim = time.Now()
			claimed, _, err := rc.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: events.TopicBuild, Group: events.BuildConsumerGroup, Consumer: rc.Consumer,
				MinIdle: rc.ClaimIdle, Start: "0-0", Count: int64(rc.Concurrency),
			}).Result()
			if err == nil {
				msgs = append(msgs, claimed...)
			}
		}
		// only read as many fresh jobs as there are free slots
		free := rc.Concurrency - len(sem)
		if free > 0 {
			fresh, err := events.ReadGroup(ctx, rc.Redis, events.TopicBuild, events.BuildConsumerGroup, rc.Consumer, int64(free), rc.ReadBlock)
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				w.Log.Error("read group", "err", err)
				sleep(ctx, 2*time.Second)
			}
			msgs = append(msgs, fresh...)
		} else {
			sleep(ctx, 500*time.Millisecond)
		}
		for _, msg := range msgs {
			sem <- struct{}{}
			wg.Add(1)
			go func(msg redis.XMessage) {
				defer wg.Done()
				defer func() { <-sem }()
				w.handle(jobCtx, rc, msg)
			}(msg)
		}
	}

	w.Log.Info("worker draining", "timeout", rc.DrainTimeout)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(rc.DrainTimeout):
		w.Log.Warn("drain timeout: cancelling running builds (they will be retried by another worker)")
		cancelJobs()
		<-done
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (w *Worker) handle(ctx context.Context, rc RunConfig, msg redis.XMessage) {
	w.defaults()
	ack := func() {
		_ = events.Ack(context.WithoutCancel(ctx), rc.Redis, events.TopicBuild, events.BuildConsumerGroup, msg.ID)
	}
	env, err := events.ParseEnvelope(msg)
	if err != nil {
		w.Log.Error("unparseable job", "id", msg.ID, "err", err)
		ack()
		return
	}
	if env.Type != events.TypeBuildQueued {
		ack() // not a job (defensive: outcomes are published to jp.deploy)
		return
	}
	job, err := ParseJob(env)
	if err != nil {
		w.Log.Error("invalid job payload", "id", msg.ID, "err", err)
		ack()
		return
	}
	key := fmt.Sprintf("jp:worker:attempts:%s", msg.ID)
	attempts, _ := rc.Redis.Incr(ctx, key).Result()
	_ = rc.Redis.Expire(ctx, key, 24*time.Hour).Err()
	if int(attempts) > rc.MaxAttempts {
		// dead letter: the job crashed or failed infrastructure-wise too often
		w.Log.Error("job dead-lettered", "build_id", job.BuildID, "attempts", attempts)
		w.deadLetter(ctx, job, "build abandoned after repeated worker failures")
		ack()
		return
	}
	if w.Process(ctx, job) == Done {
		ack()
	}
}

func (w *Worker) deadLetter(ctx context.Context, j Job, msg string) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = w.patchBuild(c, j.BuildID, map[string]any{"status": "failed", "error": msg, "logs": "BUILD FAILED: " + msg + "\n"})
	if j.DeploymentID != "" {
		_ = w.patchDeployment(c, j.DeploymentID, map[string]any{"status": "failed", "error": msg, "build_id": j.BuildID})
	}
}
