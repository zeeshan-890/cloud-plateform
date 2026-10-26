package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type PlatformBackupManifest struct {
	ID                 string    `json:"id"`
	PlatformVersion    string    `json:"platform_version"`
	CreatedAt          time.Time `json:"created_at"`
	PostgresDumpSize   int64     `json:"postgres_dump_size_bytes"`
	ConfigFilesCount   int       `json:"config_files_count"`
	ChecksumSHA256     string    `json:"checksum_sha256"`
	DestinationURI     string    `json:"destination_uri"`
	EncryptionKeyID    string    `json:"encryption_key_id,omitempty"`
	Status             string    `json:"status"` // "ready", "corrupt", "restored"
}

// GenerateChecksum computes a SHA-256 hash of payload bytes.
func GenerateChecksum(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// CreateManifest builds a validated backup manifest with cryptographic checksum.
func CreateManifest(id, version string, payload []byte, configCount int, destURI string) (*PlatformBackupManifest, error) {
	if id == "" {
		return nil, errors.New("backup ID cannot be empty")
	}
	if len(payload) == 0 {
		return nil, errors.New("database payload cannot be empty")
	}

	return &PlatformBackupManifest{
		ID:               id,
		PlatformVersion:  version,
		CreatedAt:        time.Now().UTC(),
		PostgresDumpSize: int64(len(payload)),
		ConfigFilesCount: configCount,
		ChecksumSHA256:   GenerateChecksum(payload),
		DestinationURI:   destURI,
		Status:           "ready",
	}, nil
}

// VerifyRestore checks that the manifest checksum matches the restored payload before writing to disk/db.
func VerifyRestore(manifest *PlatformBackupManifest, restoredPayload []byte) error {
	if manifest == nil {
		return errors.New("manifest cannot be nil")
	}
	if len(restoredPayload) == 0 {
		return errors.New("restored payload is empty")
	}

	actualChecksum := GenerateChecksum(restoredPayload)
	if actualChecksum != manifest.ChecksumSHA256 {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", manifest.ChecksumSHA256, actualChecksum)
	}

	return nil
}
