package dockerx

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	// nameRe matches Docker container / network / volume names accepted by the platform.
	nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	// idRe matches a container id (hex) or a name.
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)
	labelKey = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-/]{0,127}$`)
	// refRe implements the docker distribution reference grammar
	// (domain[:port]/path[:tag][@digest]).
	refRe = regexp.MustCompile(`^(?:(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9])(?::[0-9]+)?/)?` +
		`[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*` +
		`(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?` +
		`(?:@[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*:[0-9a-fA-F]{32,})?$`)
)

// ValidName reports whether s is an acceptable container/network/volume name or id.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ValidImageRef rejects anything that is not a well-formed docker image reference
// (leading '-', whitespace, shell metacharacters, uppercase repository names, ...).
func ValidImageRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("image reference is empty")
	}
	if len(ref) > 255 {
		return fmt.Errorf("image reference too long")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("image reference must not start with '-'")
	}
	if !refRe.MatchString(ref) {
		return fmt.Errorf("invalid image reference %q", truncate(ref, 80))
	}
	return nil
}

// ValidEnvKey reports whether k is a valid environment variable name.
func ValidEnvKey(k string) bool { return envKeyRe.MatchString(k) }

// ValidUUID reports whether s is a canonical UUID.
func ValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}

// Short8 returns the first 8 hex chars of a UUID without dashes (projShort8 / deploymentShort8).
func Short8(id string) string {
	id = strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ProjectNetwork is the per-project private bridge network name.
func ProjectNetwork(projectID string) string { return "jp_p_" + Short8(projectID) }

// AppService is the production Traefik service name for a project.
func AppService(projectID string) string { return "jp-app-" + Short8(projectID) }

// PreviewService is the Traefik service name for a preview deployment.
func PreviewService(deploymentID string) string { return "jp-prev-" + Short8(deploymentID) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
