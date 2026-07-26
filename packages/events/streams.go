package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// PublishJSON XADDs a JSON envelope onto a Redis Stream topic.
func PublishJSON(ctx context.Context, rdb *redis.Client, topic string, env Envelope) (string, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	id, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: topic,
		Values: map[string]any{"payload": string(raw)},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("xadd %s: %w", topic, err)
	}
	return id, nil
}

// EnsureGroup creates a consumer group if missing (MKSTREAM).
func EnsureGroup(ctx context.Context, rdb *redis.Client, topic, group string) error {
	err := rdb.XGroupCreateMkStream(ctx, topic, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

// ReadGroup blocks for the next message(s) for a consumer (count usually 1).
func ReadGroup(ctx context.Context, rdb *redis.Client, topic, group, consumer string, count int64, block time.Duration) ([]redis.XMessage, error) {
	if count <= 0 {
		count = 1
	}
	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{topic, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return streams[0].Messages, nil
}

// Ack acknowledges a processed stream message.
func Ack(ctx context.Context, rdb *redis.Client, topic, group, id string) error {
	return rdb.XAck(ctx, topic, group, id).Err()
}

// ParseEnvelope extracts Envelope from stream message values.
func ParseEnvelope(msg redis.XMessage) (Envelope, error) {
	var env Envelope
	raw, ok := msg.Values["payload"].(string)
	if !ok {
		return env, fmt.Errorf("missing payload field")
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return env, err
	}
	return env, nil
}
