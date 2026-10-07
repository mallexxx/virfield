package qualify

import (
	"context"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// OS verifies the exact build and, when given, the version; it returns the
// reported version for evidence and DeviceSupport naming.
func OS(ctx context.Context, g Guest, version, build string) (string, error) {
	actualBuild, err := g.Run(ctx, "/usr/bin/sw_vers -buildVersion", "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(actualBuild) != build {
		return "", domain.Err("guest_build_mismatch", "Guest macOS build differs from the image manifest")
	}
	out, err := g.Run(ctx, "/usr/bin/sw_vers -productVersion", "")
	if err != nil {
		return "", err
	}
	actual := strings.TrimSpace(out)
	if !domain.ValidVersion(actual) {
		return "", domain.Err("guest_version_mismatch", "Guest reported an invalid macOS version")
	}
	if version != "" && domain.CompareVersions(actual, version) != 0 {
		return "", domain.Err("guest_version_mismatch", "Guest macOS version differs from the selected catalog release")
	}
	return actual, nil
}

// SIP verifies the canonical normal-boot SIP status and returns it.
func SIP(ctx context.Context, g Guest, disabled bool) (string, error) {
	out, err := g.Run(ctx, "/usr/bin/csrutil status", "")
	if err != nil {
		return "", err
	}
	expected := "System Integrity Protection status: enabled."
	if disabled {
		expected = "System Integrity Protection status: disabled."
	}
	if strings.TrimSpace(out) != expected {
		return "", domain.Err("sip_verification_failed", "Guest SIP status does not match the requested canonical state")
	}
	return expected, nil
}

// Password proves the rotated account password persisted across a reboot.
// The password travels on stdin; the user is a validated name.
func Password(ctx context.Context, g Guest, user, password string) error {
	failure := domain.Err("credential_verification_failed", "Image password did not persist across reboot; image will not be published")
	if !domain.ValidName(user) {
		return failure
	}
	if _, err := g.Run(ctx, "/bin/bash -c 'IFS= read -r password; /usr/bin/dscl . -authonly "+user+" \"$password\"'", password+"\n"); err != nil {
		return failure
	}
	return nil
}
