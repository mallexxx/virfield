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

// Execute the actual last observation loop with a deterministic shell clock.
// This reproduces a valid cold agent finishing after the old 120-second guard,
// and proves that an earlier stage cannot give the next stage a fresh budget.
func TestNativeResultUsesRemainingCommandBudget(t *testing.T) {
	script := guestscripts.Keychain
	probe := strings.LastIndex(script, "\n  if [[ -f \"$dir/result\" ]]; then")
	if probe < 0 {
		t.Fatal("Missing native result observation")
	}
	start := strings.LastIndex(script[:probe], "\n") + 1
	budgetStart := strings.Index(script, "deadline=$((SECONDS+285))")
	if budgetStart < 0 {
		t.Fatal("Missing shared command budget")
	}
	budgetEnd := strings.Index(script[budgetStart:], "\nfor ((attempt=0;") + budgetStart
	if budgetEnd <= budgetStart {
		t.Fatal("Missing shared command budget")
	}
	for _, test := range []struct {
		name, mode             string
		elapsed, after, budget int
		ready                  bool
	}{
		{"initial proof after 130 seconds", "ensure", 0, 130, 285, true},
		{"cold reboot proof beyond five minutes", "verify", 0, 310, 585, true},
		{"missing reboot proof", "verify", 0, 10000, 585, false},
		{"ensure prior phase spent its budget", "ensure", 230, 130, 285, false},
		{"verify prior phase spent its budget", "verify", 530, 130, 585, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			counter := filepath.Join(directory, "ticks")
			harness := `set -eu
dir="$VIRFIELD_TEST_DIR"
ticks=0
mode="$VIRFIELD_TEST_MODE"
SECONDS=0
` + script[budgetStart:budgetEnd] + `
SECONDS="$VIRFIELD_TEST_ELAPSED"
trap 'printf "%s" "$ticks" > "$VIRFIELD_TEST_COUNTER"' EXIT
sleep() {
  ticks=$((ticks+1))
  SECONDS=$((VIRFIELD_TEST_ELAPSED+ticks))
  if [ "$ticks" -eq "$VIRFIELD_TEST_AFTER" ]; then printf 'native-proof' > "$dir/result"; fi
}
`
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/bash", "-c", harness+script[start:])
			command.Env = append(os.Environ(), "VIRFIELD_TEST_DIR="+directory, "VIRFIELD_TEST_COUNTER="+counter, "VIRFIELD_TEST_AFTER="+strconv.Itoa(test.after), "VIRFIELD_TEST_ELAPSED="+strconv.Itoa(test.elapsed), "VIRFIELD_TEST_MODE="+test.mode)
			out, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("Observation loop was unbounded", ctx.Err())
			}
			if test.ready && (err != nil || string(out) != "native-proof") {
				t.Fatalf("Valid late native proof rejected: %v %s", err, out)
			}
			if !test.ready {
				failure, ok := err.(*exec.ExitError)
				if !ok || failure.ExitCode() != 65 {
					t.Fatal("Unavailable native proof accepted", err, string(out))
				}
			}
			b, err := os.ReadFile(counter)
			ticks, parseErr := strconv.Atoi(string(b))
			if err != nil || parseErr != nil || ticks+test.elapsed > test.budget {
				t.Fatal("A phase extended the shared command budget", ticks, err, parseErr)
			}
		})
	}
}
