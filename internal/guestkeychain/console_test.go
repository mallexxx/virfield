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

// Execute the actual console observation block with a deterministic delayed
// console. No guest commands, account changes, keychains or real sleeps run.
func TestConsoleObservationAllowsColdLoginAndBoundsMissingLogin(t *testing.T) {
	start := strings.Index(guestscripts.Keychain, "for ((attempt=0; attempt<")
	end := strings.Index(guestscripts.Keychain, "\nuid=")
	if start < 0 || end <= start {
		t.Fatal("Missing console observation block")
	}
	for _, test := range []struct {
		name  string
		after int
		ready bool
	}{{"cold login after forty observations", 40, true}, {"console never logs in", 10000, false}} {
		t.Run(test.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "console-calls")
			if err := os.WriteFile(counter, []byte("0"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			harness := `set -eu
user=admin
stat() {
  console_calls=$(( $(/bin/cat "$VIRFIELD_TEST_COUNTER") + 1 ))
  printf '%s' "$console_calls" > "$VIRFIELD_TEST_COUNTER"
  if [ "$console_calls" -gt "$VIRFIELD_TEST_CONSOLE_AFTER" ]; then printf admin; else printf root; fi
}
sleep() { :; }
`
			command := exec.CommandContext(ctx, "/bin/bash", "-c", harness+guestscripts.Keychain[start:end])
			command.Env = append(os.Environ(), "VIRFIELD_TEST_COUNTER="+counter, "VIRFIELD_TEST_CONSOLE_AFTER="+strconv.Itoa(test.after))
			out, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("Console observation was not bounded", ctx.Err())
			}
			if test.ready && err != nil {
				t.Fatalf("Cold console login rejected: %v %s", err, out)
			}
			if !test.ready {
				failure, ok := err.(*exec.ExitError)
				if !ok || failure.ExitCode() != 65 {
					t.Fatalf("Missing console login accepted: %v %s", err, out)
				}
			}
			b, err := os.ReadFile(counter)
			calls, parseErr := strconv.Atoi(string(b))
			if err != nil || parseErr != nil || calls > 200 {
				t.Fatal("Console observation exceeded its bound", calls, err, parseErr)
			}
		})
	}
}
