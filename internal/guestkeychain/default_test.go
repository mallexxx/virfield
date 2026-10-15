package guestkeychain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

func TestLoginRemainsDefaultForNextBoot(t *testing.T) {
	script := guestscripts.Keychain
	begin := strings.Index(script, "  # ai.virfield.default-keychain-policy begin")
	end := strings.Index(script, "  # ai.virfield.default-keychain-policy end")
	if begin < 0 || end < begin {
		t.Fatal("Missing production default policy")
	}
	block := strings.ReplaceAll(script[begin:end], "/usr/bin/security", "security")
	for _, exists := range []bool{true, false} {
		dir := t.TempDir()
		login := filepath.Join(dir, "login.keychain-db")
		if exists {
			if err := os.WriteFile(login, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		harness := `set -eu
login="$VF_TEST_LOGIN"; keychain=scoped
security() {
 case "$1" in
  default-keychain) printf '%s' "$5" > "$VF_TEST_DEFAULT";;
  list-keychains) printf '%s' "$5" > "$VF_TEST_SEARCH_FIRST";;
 esac
}
`
		cmd := exec.Command("/bin/bash", "-c", harness+block)
		cmd.Env = append(os.Environ(), "VF_TEST_LOGIN="+login, "VF_TEST_DEFAULT="+filepath.Join(dir, "default"), "VF_TEST_SEARCH_FIRST="+filepath.Join(dir, "search"))
		out, err := cmd.CombinedOutput()
		if !exists {
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 65 {
				t.Fatal("Missing login was accepted", err)
			}
			continue
		}
		if err != nil || len(out) != 0 {
			t.Fatal("Default policy failed", string(out), err)
		}
		for _, name := range []string{"default", "search"} {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || string(b) != login {
				t.Fatal("Custom default would prompt before GUI unlock", name, err)
			}
		}
	}
}
