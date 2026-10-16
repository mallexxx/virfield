package guestkeychain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

func TestEnsureDemandsOwnedAgentAfterRegistrationRace(t *testing.T) {
	script := guestscripts.Keychain
	start := strings.Index(script, "# ai.virfield.ensure-agent begin")
	end := strings.Index(script, "# ai.virfield.ensure-agent end")
	if start < 0 {
		start = strings.Index(script, "  launchctl bootstrap \"gui/$uid\" \"$plist\"\n")
		if start >= 0 {
			end = start + strings.Index(script[start:], "\nelse\n")
		}
	}
	if start < 0 || end < start {
		t.Fatal("missing ensure agent startup")
	}
	block := script[start:end]
	harness := `set -eu
umask 077
dir="$VF_TEST_DIR"; uid=501; label=ai.virfield.guest-keychain; plist="$dir/agent.plist"
launchctl() {
 case "$1" in
 bootstrap)
  [[ "$2" == gui/501 && "$3" == "$plist" ]] || exit 90
  if [[ "$VF_BOOTSTRAP_STATUS" != 0 ]]; then echo 'Bootstrap failed: 5: Input/output error' >&2; fi
  return "$VF_BOOTSTRAP_STATUS";;
 print) [[ "$2" == gui/501/ai.virfield.guest-keychain ]] || exit 91; return "$VF_LOOKUP_STATUS";;
 kickstart)
  if [[ "${2:-}" == -k ]]; then shift; kind=fresh; else kind=normal; fi
  [[ "$2" == gui/501/ai.virfield.guest-keychain ]] || exit 92
  printf 'demand:%s\n' "$kind"
  return "$VF_DEMAND_STATUS";;
 *) exit 93;;
 esac
}
`
	for _, tc := range []struct {
		name, bootstrap, lookup, demand, want string
		wantExit                              int
	}{
		{"fresh registration", "0", "3", "0", "demand:normal", 0},
		{"registered after bootout", "5", "0", "0", "demand:fresh", 0},
		{"bootstrap failure without registered job", "5", "3", "0", "", 5},
		{"registered job cannot restart", "5", "0", "70", "demand:fresh", 70},
		{"fresh job cannot start", "0", "3", "70", "demand:normal", 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", harness+block)
			cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir, "VF_BOOTSTRAP_STATUS="+tc.bootstrap, "VF_LOOKUP_STATUS="+tc.lookup, "VF_DEMAND_STATUS="+tc.demand)
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if ctx.Err() != nil || code != tc.wantExit || strings.TrimSpace(stdout.String()) != tc.want {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if code == 0 && stderr.Len() != 0 {
				t.Fatalf("bootstrap diagnostics contaminated native proof output: %q", stderr.String())
			}
			file, err := os.Stat(filepath.Join(dir, "bootstrap.log"))
			if err != nil {
				t.Fatal(err)
			}
			if file.Mode().Perm() != 0600 {
				t.Fatalf("bootstrap log permissions: %v", file.Mode())
			}
		})
	}
}
