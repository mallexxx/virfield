package guestssh

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual root preference writes against an inherited locked-login
// flag. Arming the correct password alone must not preserve a locked desktop.
func TestAutologinClearsInheritedScreenLock(t *testing.T) {
	start := strings.Index(autologinScript, `printf '%s\n' "$new" | sudo -S -p '' /usr/bin/defaults write`)
	if start < 0 {
		t.Fatal("Missing autologin preference writes")
	}
	file := filepath.Join(t.TempDir(), "locked")
	if err := os.WriteFile(file, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	harness := `set -eu
new=private-fixture; user=admin
sudo() {
 IFS= read -r secret
 [[ "$secret" == "$new" ]] || return 1
 if [[ "$7" == autoLoginUserScreenLocked && "$8" == -bool && "$9" == false ]]; then printf 0 > "$VF_LOCK_FIXTURE"; fi
}
`
	cmd := exec.Command("/bin/bash", "-c", harness+autologinScript[start:])
	cmd.Env = append(os.Environ(), "VF_LOCK_FIXTURE="+file)
	out, err := cmd.CombinedOutput()
	value, readErr := os.ReadFile(file)
	if err != nil || readErr != nil || len(out) != 0 || string(value) != "0" {
		t.Fatal("Inherited lock flag retained or secret printed", err, readErr)
	}
}
