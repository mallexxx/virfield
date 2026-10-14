package qualify

import (
	"context"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestscripts"
)

// Security verifies the automation policy after a normal-boot restart.
func Security(ctx context.Context, g Guest, user string, log func(string) error) error {
	if !domain.ValidName(user) {
		return domain.Err("security_verification_failed", "Invalid guest account")
	}
	out, err := g.RunReader(ctx, 2*time.Minute, "sudo -n /bin/bash -s -- verify "+Quote(user), strings.NewReader(guestscripts.Security))
	if e := log(out); e != nil {
		return e
	}
	if err != nil || !strings.Contains(out, "Guest policy verify complete; SIP disabled; Gatekeeper disabled") {
		return domain.Err("security_verification_failed", "Guest automation security did not persist after reboot")
	}
	return nil
}
