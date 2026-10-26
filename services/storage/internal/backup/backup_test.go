package backup

import (
	"testing"
)

func TestCreateAndVerifyManifest(t *testing.T) {
	data := []byte("-- JP PostgreSQL Platform Database Dump --\nCREATE TABLE test ();")
	manifest, err := CreateManifest("backup-001", "v1.0.0", data, 5, "s3://backups/jp-platform-001.sql.gz")
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}

	if manifest.PostgresDumpSize != int64(len(data)) {
		t.Errorf("expected size %d, got %d", len(data), manifest.PostgresDumpSize)
	}

	// Verify with identical payload passes
	if err := VerifyRestore(manifest, data); err != nil {
		t.Errorf("restore verification failed unexpectedly: %v", err)
	}

	// Verify with tampered payload fails
	tampered := []byte("-- TAMPERED CONTENT --")
	if err := VerifyRestore(manifest, tampered); err == nil {
		t.Errorf("expected checksum mismatch error on tampered payload, got nil")
	}
}
