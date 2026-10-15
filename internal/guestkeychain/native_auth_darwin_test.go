package guestkeychain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

// Run the production native caller against disposable keychains, including a
// locked one. Never open, lock, rekey or replace the user's login/default.
func TestNativeLoginAuthenticationDoesNotPromptAndPreservesItems(t *testing.T) {
	begin := strings.Index(guestscripts.Keychain, "  cat > \"$dir/login-auth.js\" <<'AUTH'\n")
	end := strings.Index(guestscripts.Keychain, "\nAUTH\n")
	if begin < 0 || end < begin {
		t.Fatal("Missing production native authentication")
	}
	begin += len("  cat > \"$dir/login-auth.js\" <<'AUTH'\n")
	script := guestscripts.Keychain[begin:end]
	user, err := exec.Command("/usr/bin/id", "-un").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                  string
		locked, public, stale bool
	}{{"unlocked", false, false, false}, {"locked", true, false, false}, {"public base", true, true, false}, {"stale unlocked cache", false, false, true}, {"stale locked cache", true, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			keychain := filepath.Join(dir, "disposable.keychain-db")
			js := filepath.Join(dir, "login-auth.js")
			identity := filepath.Join(dir, "identity")
			old, password := "old-private-fixture", "new-private-fixture"
			if tc.public {
				old = strings.TrimSpace(string(user))
			}
			command := func(args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, "/usr/bin/security", args...).CombinedOutput()
				if err != nil {
					t.Fatal("Disposable keychain operation failed", args[0], err)
				}
				return out
			}
			command("create-keychain", "-p", old, keychain)
			defer command("delete-keychain", keychain)
			command("add-generic-password", "-a", "fixture", "-s", "ai.virfield.disposable-auth", "-w", "preserved-fixture-item", keychain)
			if tc.locked {
				command("lock-keychain", keychain)
			}
			previous := old
			if tc.public {
				previous = "incorrect-previous-fixture"
			}
			caller := script
			if tc.stale {
				// Inject only the stale original-cache response reproduced live.
				// Copy authentication, the guarded cache refresh, password change
				// and preservation of the original item still use real native APIs.
				probe := "status = Number($.SecKeychainUnlock(keychain, length(candidate), candidate, true));"
				if strings.Count(caller, probe) != 2 {
					t.Fatal("Missing original/refresh unlock calls")
				}
				caller = strings.Replace(caller, probe, "status = -25293; // injected stale original-cache response", 1)
			}
			if err := os.WriteFile(js, []byte(caller), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(identity, []byte("lease-fixture\n"+password+"\n"+previous+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var refreshed, validated bool
			call := func(mode string) (bool, bool, int) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", js, keychain, identity, mode).CombinedOutput()
				var result struct {
					Authenticated bool `json:"authenticated"`
					TryNext       bool `json:"try_next"`
					Status        int  `json:"status"`
					Validated     bool `json:"cache_refresh_validated"`
					Refreshed     bool `json:"cache_refreshed"`
				}
				if err != nil || json.Unmarshal(out, &result) != nil || strings.Contains(string(out), password) || strings.Contains(string(out), old) {
					t.Fatal("Native auth failed, prompted or disclosed a credential", err)
				}
				refreshed, validated = result.Refreshed, result.Validated
				copies, err := filepath.Glob(filepath.Join(dir, ".login-auth-*.keychain-db"))
				if err != nil || len(copies) != 0 {
					t.Fatal("Private authentication copy was retained", err)
				}
				return result.Authenticated, result.TryNext, result.Status
			}
			if ok, next, status := call("current"); ok || !next || status != -25293 {
				t.Fatal("Wrong current password accepted or did not fail noninteractively", ok, next, status)
			}
			if refreshed || validated {
				t.Fatal("Wrong password was allowed to refresh the original cache")
			}
			mode := "previous"
			if tc.public {
				if ok, next, status := call("previous"); ok || !next || status != -25293 {
					t.Fatal("Wrong inherited password accepted", ok, next, status)
				}
				mode = "user"
			}
			if ok, next, status := call(mode); !ok || next || status != 0 {
				t.Fatal("Correct native migration failed", ok, next, status)
			}
			if tc.stale && (!validated || !refreshed) {
				t.Fatal("Stale original cache was not refreshed after real copy authentication")
			}
			command("lock-keychain", keychain)
			command("unlock-keychain", "-p", password, keychain)
			if got := strings.TrimSpace(string(command("find-generic-password", "-a", "fixture", "-s", "ai.virfield.disposable-auth", "-w", keychain))); got != "preserved-fixture-item" {
				t.Fatal("Existing item lost during rekey")
			}
			if ok, next, status := call("current"); !ok || next || status != 0 {
				t.Fatal("Idempotent native authentication failed", ok, next, status)
			}
		})
	}
}
