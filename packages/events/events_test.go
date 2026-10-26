package events

import (
	"encoding/json"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestNewEnvelope(t *testing.T) {
	payload := map[string]any{"project_id": "p-100", "status": "active"}
	env := New(TopicProject, TypeProjectCreated, "actor-1", "org-1", payload)

	if env.ID == "" {
		t.Fatal("expected non-empty envelope ID")
	}
	if env.Topic != TopicProject {
		t.Errorf("expected topic %s, got %s", TopicProject, env.Topic)
	}
	if env.Type != TypeProjectCreated {
		t.Errorf("expected type %s, got %s", TypeProjectCreated, env.Type)
	}
	if env.ActorID != "actor-1" || env.OrgID != "org-1" {
		t.Errorf("actor/org mismatch: %s / %s", env.ActorID, env.OrgID)
	}
	if env.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if env.Payload["project_id"] != "p-100" {
		t.Errorf("payload mismatch: %v", env.Payload)
	}
}

func TestParseEnvelope(t *testing.T) {
	env := New(TopicAddon, TypeAddonCreated, "user-42", "org-99", map[string]any{"addon": "valkey"})
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	msg := redis.XMessage{
		ID: "1670000000000-0",
		Values: map[string]any{
			"payload": string(raw),
		},
	}

	parsed, err := ParseEnvelope(msg)
	if err != nil {
		t.Fatalf("ParseEnvelope failed: %v", err)
	}
	if parsed.ID != env.ID {
		t.Errorf("expected ID %s, got %s", env.ID, parsed.ID)
	}
	if parsed.Topic != TopicAddon {
		t.Errorf("expected topic %s, got %s", TopicAddon, parsed.Topic)
	}
	if parsed.Payload["addon"] != "valkey" {
		t.Errorf("expected valkey, got %v", parsed.Payload["addon"])
	}

	// Test missing payload
	invalidMsg := redis.XMessage{
		ID:     "1670000000000-1",
		Values: map[string]any{"other": "data"},
	}
	if _, err := ParseEnvelope(invalidMsg); err == nil {
		t.Error("expected error for missing payload field")
	}
}
