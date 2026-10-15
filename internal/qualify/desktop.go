package qualify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// DesktopProbe reports assistant, desktop or waiting.
const DesktopProbe = `if /usr/bin/pgrep -x 'Setup Assistant' >/dev/null; then echo assistant; elif test -f /var/db/.AppleSetupDone && /usr/bin/pgrep -x Finder >/dev/null; then /usr/bin/osascript -l JavaScript -e 'ObjC.import("CoreGraphics"); ObjC.bindFunction("CGSessionCopyCurrentDictionary", ["id", []]);
// ai.virfield.console-state begin
function virfieldConsoleState(session) {
  var lock = session && session.CGSSessionScreenIsLocked;
  var available = !!session && session.kCGSSessionOnConsoleKey === true && session.kCGSessionLoginDoneKey === true &&
    (typeof lock === "undefined" || typeof lock === "boolean");
  return {ready:available && lock !== true, screen_locked:available ? lock === true : null};
}
// ai.virfield.console-state end
function run() { return virfieldConsoleState(ObjC.deepUnwrap($.CGSessionCopyCurrentDictionary())).ready ? "desktop" : "waiting"; }'; else echo waiting; fi`

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

// Desktop requires a settled, unlocked console Finder within two minutes. Setup
// Assistant is a failure here: adopted images never automate it.
func Desktop(ctx context.Context, g Guest) error {
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	started := time.Now()
	var trace strings.Builder
	state, err := WaitDesktop(wait, 2*time.Second, 5*time.Second, func(ctx context.Context) (string, error) {
		probeStarted := time.Now()
		out, err := g.Run(ctx, DesktopProbe, "")
		// Only this fixed, read-only probe is recorded. Keep the first and
		// failing observations, including partial output from a timed-out call.
		if trace.Len() < 8192 || err != nil {
			fmt.Fprintf(&trace, "at=%s elapsed=%s probe_duration=%s output=%q error=%v\n",
				probeStarted.UTC().Format(time.RFC3339Nano), probeStarted.Sub(started), time.Since(probeStarted), out[:min(len(out), 4096)], err)
		}
		return out, err
	})
	if err != nil {
		if reporter, ok := g.(interface{ ReportDesktopFailure(string) }); ok {
			deadline, _ := wait.Deadline()
			reporter.ReportDesktopFailure(fmt.Sprintf("Desktop wait deadline=%s elapsed=%s error=%v\n%s", deadline.UTC().Format(time.RFC3339Nano), time.Since(started), err, trace.String()))
		}
		return err
	}
	if state != "desktop" {
		return domain.Err("assistant_incomplete", "Guest shows Setup Assistant instead of a verified Finder desktop")
	}
	return nil
}
