package qualify

import (
	"context"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// DesktopProbe reports assistant, desktop or waiting.
const DesktopProbe = "if /usr/bin/pgrep -x 'Setup Assistant' >/dev/null; then echo assistant; elif test -f /var/db/.AppleSetupDone && /usr/bin/pgrep -x Finder >/dev/null; then echo desktop; else echo waiting; fi"

// WaitDesktop polls until Setup Assistant appears or Finder stays up for settle.
func WaitDesktop(ctx context.Context, poll, settle time.Duration, observe func(context.Context) (string, error)) (string, error) {
	var desktopSince time.Time
	for {
		out, err := observe(ctx)
		if err != nil {
			return "", err
		}
		state := strings.TrimSpace(out)
		if state == "assistant" {
			return state, nil
		}
		// Finder can appear before the login-time Assistant. Require a settled
		// desktop before rotating credentials or declaring the GUI ready.
		if state == "desktop" {
			if desktopSince.IsZero() {
				desktopSince = time.Now()
			} else if time.Since(desktopSince) >= settle {
				return state, nil
			}
		} else {
			desktopSince = time.Time{}
		}
		if err := pause(ctx, poll); err != nil {
			return "", domain.Err("desktop_timeout", "Guest login did not reach Setup Assistant or Finder within two minutes")
		}
	}
}

// Desktop requires a settled Finder desktop within two minutes. Setup
// Assistant is a failure here: adopted images never automate it.
func Desktop(ctx context.Context, g Guest) error {
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	state, err := WaitDesktop(wait, 2*time.Second, 5*time.Second, func(ctx context.Context) (string, error) {
		return g.Run(ctx, DesktopProbe, "")
	})
	if err != nil {
		return err
	}
	if state != "desktop" {
		return domain.Err("assistant_incomplete", "Guest shows Setup Assistant instead of a verified Finder desktop")
	}
	return nil
}
