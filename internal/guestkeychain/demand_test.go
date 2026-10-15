package guestkeychain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

func scriptBlock(t *testing.T, name string) string {
	t.Helper()
	start := strings.Index(guestscripts.Keychain, "# ai.virfield."+name+" begin")
	end := strings.Index(guestscripts.Keychain, "# ai.virfield."+name+" end")
	if start < 0 || end <= start {
		t.Fatal("Missing actual startup block", name)
	}
	return guestscripts.Keychain[start:end]
}

// A real shell models launchd registering Aqua after SSH, then keeping a
// RunAtLoad job at runs=0 until a demand arrives. No keychain APIs are executed.
func TestWorkerSSHRequiresCurrentBootGUIProofBeforeOriginalCommand(t *testing.T) {
	block := scriptBlock(t, "ssh-gui-startup")
	block = strings.NewReplacer("/bin/launchctl", "launchctl", "/usr/bin/id", "id", "/usr/sbin/sysctl", "sysctl").Replace(block)
	for _, test := range []struct {
		name, scope, startup string
		registered, ready    int
		pass                 bool
		starts               int
	}{
		{"deferred after delayed registration", "lease-worker", "lease-worker\nold-boot", 3, 7, true, 1},
		{"current boot needs no restart", "lease-worker", "lease-worker\ncurrent-boot", 0, 0, true, 0},
		{"wrong scope is not ready", "lease-worker", "lease-other\ncurrent-boot", 0, 4, true, 1},
		{"missing GUI registration is bounded", "lease-worker", "", 10000, 10000, false, 0},
		{"failed agent is not blindly restarted", "lease-worker", "", 0, 10000, false, 1},
		{"image bootstrap SSH stays available", "image-base", "", 10000, 10000, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.startup != "" {
				if err := os.WriteFile(filepath.Join(dir, "startup"), []byte(test.startup), 0600); err != nil {
					t.Fatal(err)
				}
			}
			harness := `set -eu
dir="$VF_TEST_DIR"; scope="$VF_TEST_SCOPE"; ticks=0; starts=0; SECONDS=0
trap 'printf "%s %s" "$starts" "$ticks" > "$VF_TEST_COUNTS"' EXIT
id() { printf 501; }
sysctl() { printf current-boot; }
launchctl() {
  case "$1" in
    print) (( ticks >= VF_TEST_REGISTERED ));;
    kickstart) [[ "$#" == 2 && "$2" == gui/501/ai.virfield.guest-keychain ]] || exit 90; starts=$((starts+1));;
    *) exit 91;;
  esac
}
sleep() {
  ticks=$((ticks+1)); SECONDS=$ticks
  if (( starts > 0 && ticks >= VF_TEST_READY )); then printf '%s\ncurrent-boot' "$scope" > "$dir/startup"; fi
}
`
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", harness+block+"\nIFS= read -r original; printf 'command:%s' \"$original\"")
			cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir, "VF_TEST_SCOPE="+test.scope, "VF_TEST_COUNTS="+filepath.Join(dir, "counts"), "VF_TEST_REGISTERED="+strconv.Itoa(test.registered), "VF_TEST_READY="+strconv.Itoa(test.ready))
			cmd.Stdin = strings.NewReader("original-stdin\n")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("GUI gate was unbounded", ctx.Err())
			}
			if test.pass && (err != nil || string(out) != "command:original-stdin") {
				t.Fatal("valid startup or original stdin lost", string(out), err)
			}
			if !test.pass {
				failure, ok := err.(*exec.ExitError)
				if !ok || failure.ExitCode() != 65 || strings.Contains(string(out), "command:") {
					t.Fatal("unverified GUI allowed the original command", string(out), err)
				}
			}
			counts, err := os.ReadFile(filepath.Join(dir, "counts"))
			fields := strings.Fields(string(counts))
			if err != nil || len(fields) != 2 || fields[0] != strconv.Itoa(test.starts) {
				t.Fatal("unexpected demand start count", string(counts), err)
			}
			ticks, err := strconv.Atoi(fields[1])
			if err != nil || ticks > 585 {
				t.Fatal("GUI wait extended its deadline", string(counts), err)
			}
		})
	}
}

func TestAutomaticAquaStartupIsIdempotentButExplicitProbeAlwaysRuns(t *testing.T) {
	block := scriptBlock(t, "agent-startup-once")
	for _, test := range []struct {
		name, startup, explicit, session string
		work                             bool
	}{
		{"completed same boot/session", "lease-worker\ncurrent-boot", "false", "100010", false},
		{"old boot", "lease-worker\nold-boot", "false", "100010", true},
		{"other scope", "lease-other\ncurrent-boot", "false", "100010", true},
		{"new Aqua login in the same boot", "lease-worker\ncurrent-boot", "false", "100009", true},
		{"explicit fresh probe", "lease-worker\ncurrent-boot", "true", "100010", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "startup"), []byte(test.startup), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "startup-session"), []byte(test.session), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/bash", "-c", "set -eu\ndir=\"$VF_TEST_DIR\"; scope=lease-worker; boot=current-boot; aqua_session=100010; explicit_request=\"$VF_TEST_EXPLICIT\"\n"+block+"\nprintf agent-work")
			cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir, "VF_TEST_EXPLICIT="+test.explicit)
			out, err := cmd.CombinedOutput()
			if err != nil || (string(out) == "agent-work") != test.work {
				t.Fatal("startup reused a stale proof or repeated a completed boot", string(out), err)
			}
		})
	}
}

func TestVerifyDemandsDeferredImageAgentWithinRemainingBudget(t *testing.T) {
	block := scriptBlock(t, "verify-startup")
	for _, ready := range []bool{true, false} {
		t.Run(strconv.FormatBool(ready), func(t *testing.T) {
			dir := t.TempDir()
			harness := `set -eu
dir="$VF_TEST_DIR"; scope=image-base; boot=current-boot; uid=501; label=ai.virfield.guest-keychain
SECONDS=0; ticks=0; starts=0; deadline=8
launchctl() {
  case "$1" in
    print) (( ticks >= 2 ));;
    kickstart) [[ "$#" == 2 && "$2" == gui/501/ai.virfield.guest-keychain ]] || exit 90; starts=$((starts+1));;
    *) exit 91;;
  esac
}
sleep() {
  ticks=$((ticks+1)); SECONDS=$ticks
  if [[ "$VF_TEST_READY" == true ]] && (( starts > 0 && ticks == 4 )); then printf '%s\n%s' "$scope" "$boot" > "$dir/startup"; fi
}
`
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", harness+block+"\nprintf '%s %s' \"$starts\" \"$ticks\"")
			cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir, "VF_TEST_READY="+strconv.FormatBool(ready))
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("deferred startup was unbounded", ctx.Err())
			}
			if ready {
				if err != nil || string(out) != "1 4" {
					t.Fatal("registered RunAtLoad agent was not demanded once", string(out), err)
				}
			} else if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 65 {
				t.Fatal("missing startup extended or bypassed the remaining budget", string(out), err)
			}
		})
	}
}

func TestSSHGUIWaitDoesNotGiveVerificationAnotherBudget(t *testing.T) {
	start := strings.Index(guestscripts.Keychain, "deadline=$((SECONDS+285))")
	end := strings.Index(guestscripts.Keychain[start:], "\nfor ((attempt=0;") + start
	if start < 0 || end <= start {
		t.Fatal("Missing actual verification budget")
	}
	for _, elapsed := range []string{"0", "310", "585", "586", "-1", "bad"} {
		cmd := exec.Command("/bin/bash", "-c", "set -eu\nmode=verify; SECONDS=0\n"+guestscripts.Keychain[start:end]+"\nprintf '%s' \"$deadline\"")
		cmd.Env = append(os.Environ(), "VIRFIELD_GUI_WAIT_ELAPSED="+elapsed)
		out, err := cmd.CombinedOutput()
		spent, parseErr := strconv.Atoi(elapsed)
		if parseErr == nil && spent == 585 {
			if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 65 {
				t.Fatal("exhausted inherited wait started another phase", elapsed, string(out), err)
			}
		} else if parseErr != nil || spent < 0 || spent > 585 {
			if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 64 {
				t.Fatal("invalid inherited wait accepted", elapsed, string(out), err)
			}
		} else if err != nil || string(out) != strconv.Itoa(585-spent) {
			t.Fatal("GUI startup reset the verification budget", elapsed, string(out), err)
		}
	}
}
