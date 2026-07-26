package events

import (
	"time"

	"github.com/google/uuid"
)

// Redis Streams topic names.
const (
	TopicAuth         = "jp.auth"
	TopicOrg          = "jp.org"
	TopicProject      = "jp.project"
	TopicNotification = "jp.notification"
	TopicAudit        = "jp.audit"
	TopicDeploy       = "jp.deploy"
	TopicBuild        = "jp.build"
	TopicRuntime      = "jp.runtime"
	TopicDomain       = "jp.domain"
	TopicSecret       = "jp.secret"
	TopicLogging      = "jp.logging"
	TopicMetrics      = "jp.metrics"
	TopicStorage      = "jp.storage"
	TopicDatabase     = "jp.database"
	TopicCleanup      = "jp.cleanup"
	TopicJobs         = "jp.jobs"
)

// Envelope is the standard event wrapper for Redis Streams payloads.
type Envelope struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Topic     string         `json:"topic"`
	Timestamp time.Time      `json:"timestamp"`
	ActorID   string         `json:"actor_id,omitempty"`
	OrgID     string         `json:"org_id,omitempty"`
	Payload   map[string]any `json:"payload"`
}

// New creates an envelope with a fresh ID and UTC timestamp.
func New(topic, eventType, actorID, orgID string, payload map[string]any) Envelope {
	if payload == nil {
		payload = map[string]any{}
	}
	return Envelope{
		ID:        uuid.NewString(),
		Type:      eventType,
		Topic:     topic,
		Timestamp: time.Now().UTC(),
		ActorID:   actorID,
		OrgID:     orgID,
		Payload:   payload,
	}
}

// Common event type constants.
const (
	TypeUserRegistered   = "user.registered"
	TypeUserLoggedIn     = "user.logged_in"
	TypeUserLoggedOut    = "user.logged_out"
	TypeInviteCreated    = "org.invite_created"
	TypeInviteAccepted   = "org.invite_accepted"
	TypeProjectCreated   = "project.created"
	TypeProjectUpdated   = "project.updated"
	TypeProjectDeleted   = "project.deleted"
	TypeNotificationSend = "notification.send"
	TypeRepoConnected    = "repo.connected"
	TypeGitPush          = "git.push"
	TypeDeployCreated    = "deploy.created"
	TypeDeployUpdated    = "deploy.updated"
	TypeBuildQueued      = "build.queued"
	TypeBuildStarted     = "build.started"
	TypeBuildSucceeded   = "build.succeeded"
	TypeBuildFailed      = "build.failed"
	TypeRuntimeStarted   = "runtime.started"
	TypeRuntimeStopped   = "runtime.stopped"
	TypeRuntimeFailed    = "runtime.failed"
	TypeDomainAdded      = "domain.added"
	TypeDomainVerified   = "domain.verified"
	TypeCertIssued       = "cert.issued"
	TypeCertRenewed      = "cert.renewed"
	TypeSecretCreated    = "secret.created"
	TypeSecretRotated    = "secret.rotated"
	TypeSecretDeleted    = "secret.deleted"
	TypeStorageUploaded  = "storage.uploaded"
	TypeStorageDeleted   = "storage.deleted"
	TypeDatabaseCreated  = "database.created"
	TypeDatabaseDeleted  = "database.deleted"
	TypeCleanupOrphanImages = "cleanup.orphaned_images"
	TypeCleanupPreview      = "cleanup.preview_deploys"
	TypeCronTriggered       = "cron.triggered"
	TypeJobQueued           = "job.queued"
)

// Consumer group for build workers (scale with WORKER_REPLICAS × WORKER_CONCURRENCY).
const BuildConsumerGroup = "jp-workers"

// Consumer group for the Phase-4 scheduler (single-node slot assignment).
const SchedulerConsumerGroup = "jp-scheduler"

// Consumer group for Phase-6 cleanup jobs on jp.cleanup.
const CleanupConsumerGroup = "jp-cleanup"

// Consumer group for Phase-6 background / cron jobs on jp.jobs.
const JobsConsumerGroup = "jp-jobs"

// Consumer group for Phase-7 billing usage metering.
const BillingConsumerGroup = "jp-billing"
