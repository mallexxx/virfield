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

// Execute the actual password-selection loop with a valid native I/O receipt
// whose UI is blocked. A desktop problem must never select a wrong password or
// leave a previously usable login keychain locked.
func TestLoginPasswordSelectionIgnoresIndependentDesktopBlockers(t *testing.T) {
	script := guestscripts.Keychain
	start := strings.Index(script, "    ready=false\n    attempt=0")
	endMarker := `    [[ "$ready" == true ]] || exit 65`
	end := strings.Index(script, endMarker)
	if start < 0 || end < start {
		t.Fatal("Missing actual login authentication loop")
	}
	block := script[start : end+len(endMarker)]
	block = strings.NewReplacer("/usr/bin/security", "security", "/usr/bin/osascript", "osascript").Replace(block)
	dir := t.TempDir()
	login := filepath.Join(dir, "login.keychain-db")
	if err := os.WriteFile(login, nil, 0600); err != nil {
		t.Fatal(err)
	}
	harness := `set -eu
dir="$VF_TEST_DIR"; login="$dir/login.keychain-db"
password=current-private; previous=previous-private; scope=lease-fixture; nonce=keychain-fixture; boot=current-boot
locks=0; probes=0; rekeys=0; selected=
trap 'printf "%s %s %s" "$locks" "$probes" "$rekeys"' EXIT
phase() { :; }
id() { printf admin; }
security() {
 case "$1" in
 lock-keychain) locks=$((locks+1));;
 unlock-keychain) selected="$3";;
 set-keychain-password) echo 'Interactive CLI rekey is forbidden'; return 1;;
 esac
 return 0
}
osascript() {
 if [[ "$3" == */login-auth.js ]]; then
  rekeys=$((rekeys+1))
  case "$6" in current) old=current-private;; previous) old=previous-private;; user) old=admin;; esac
  if [[ "$old" != "$VF_TEST_SECRET" ]]; then printf '{"authenticated":false,"try_next":true,"status":-25293}'; return; fi
  selected=current-private
  printf '{"authenticated":true,"try_next":false,"status":0}'; return
 fi
 probes=$((probes+1)); io=false
 [[ "$selected" != current-private || "$VF_TEST_NATIVE" != true ]] || io=true
 printf '{"keychain_ready":%s,"ready":false,"keyboard_setup_visible":true,"security_dialog_visible":true}' "$io"
}
`
	for _, tc := range []struct {
		name, secret, native, counts string
		wantExit                     int
	}{
		{"current password with UI blockers", "current-private", "true", "0 1 1", 0},
		{"previous password migration", "previous-private", "true", "0 1 2", 0},
		{"public base migration", "admin", "true", "0 1 3", 0},
		{"native failure after authenticated rekey", "current-private", "false", "0 1 1", 65},
		{"no authenticated password", "unknown", "true", "0 0 3", 65},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", harness+block)
			cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir, "VF_TEST_SECRET="+tc.secret, "VF_TEST_NATIVE="+tc.native)
			out, err := cmd.CombinedOutput()
			exit := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					exit = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if ctx.Err() != nil || exit != tc.wantExit || string(out) != tc.counts {
				t.Fatal("Authentication/desktop separation failed", string(out), exit, ctx.Err())
			}
		})
	}
}
