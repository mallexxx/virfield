package guestssh

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestStandardAccountPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, current, inherited, target, final string
		changes, status                         int
		ineffective                             bool
	}{
		{name: "standard account is never changed", current: "admin", inherited: "old-image", target: "admin", final: "admin"},
		{name: "legacy clone is converted once", current: "old-image", inherited: "old-image", target: "admin", final: "admin", changes: 1},
		{name: "wrong inherited credential preserves account", current: "unknown", inherited: "old-image", target: "admin", final: "unknown", status: 65},
		{name: "random target is refused", current: "admin", inherited: "admin", target: "random-password", final: "admin", status: 64},
		{name: "unchanged failed account is refused", current: "unknown", inherited: "admin", target: "admin", final: "unknown", status: 65},
		{name: "successful tool exit requires actual authentication", current: "old-image", inherited: "old-image", target: "admin", final: "old-image", changes: 1, status: 1, ineffective: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			state, changes := filepath.Join(dir, "account"), filepath.Join(dir, "changes")
			if err := os.WriteFile(state, []byte(tc.current), 0600); err != nil {
				t.Fatal(err)
			}
			// Run the production policy with stateful OS-command doubles. No host
			// account, password database or privileged command is touched.
			prefix := `dscl() { [[ "$1" == . && "$2" == -authonly && "$3" == admin && "$4" == "$(cat "$ACCOUNT_FIXTURE")" ]]; }
sysadminctl() {
 [[ "$1" == -newPassword && "$2" == admin && "$3" == -oldPassword && "$4" == "$(cat "$ACCOUNT_FIXTURE")" ]] || return 99
 printf 'changed\n' >> "$CHANGES_FIXTURE"
 [[ "$INEFFECTIVE_FIXTURE" == true ]] || printf '%s' "$2" > "$ACCOUNT_FIXTURE"
 return 0
}
`
			script := prefix + strings.NewReplacer("/usr/bin/dscl", "dscl", "/usr/sbin/sysadminctl", "sysadminctl").Replace(standardAccountScript)
			cmd := exec.Command("/bin/bash", "-c", script)
			cmd.Env = append(os.Environ(), "ACCOUNT_FIXTURE="+state, "CHANGES_FIXTURE="+changes, "INEFFECTIVE_FIXTURE="+map[bool]string{true: "true", false: "false"}[tc.ineffective])
			cmd.Stdin = strings.NewReader("admin\n" + tc.inherited + "\n" + tc.target + "\n")
			out, err := cmd.CombinedOutput()
			status := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					status = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			got, readErr := os.ReadFile(state)
			changed, _ := os.ReadFile(changes)
			if status != tc.status || readErr != nil || string(got) != tc.final || strings.Count(string(changed), "changed\n") != tc.changes || len(out) != 0 {
				t.Fatalf("status=%d expected=%d changes=%d expected=%d preserved=%t output bytes=%d error=%v", status, tc.status, strings.Count(string(changed), "changed\n"), tc.changes, string(got) == tc.final, len(out), readErr)
			}
		})
	}
}

func TestStandardCredentialsKeepIndependentKeysAndPreserveLegacyImages(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	var previous []byte
	for _, scope := range []string{"image-standard", "lease-standard"} {
		l := domain.Lease{ID: scope, GuestUser: "admin"}
		if err := os.MkdirAll(m.Directory(l), 0700); err != nil {
			t.Fatal(err)
		}
		c, err := m.CredentialsFor(l)
		if err != nil || c.Password != "admin" || len(c.Private) == 0 || string(c.Private) == string(previous) {
			t.Fatal("Standard credentials did not retain independent SSH keys", err)
		}
		previous = c.Private
		c.Password = "previously-secured-fixture"
		if err := SaveCredentials(m.CredentialPath(l), c); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(m.CredentialPath(l))
		got, err := m.CredentialsFor(l)
		if err != nil || got.Password != c.Password {
			t.Fatal("Existing credential was silently rewritten", err)
		}
		if err := m.SecureImage(context.Background(), l, "", nil); err == nil {
			t.Fatal("Legacy image entered credential mutation without migration")
		}
		after, _ := os.ReadFile(m.CredentialPath(l))
		if string(before) != string(after) {
			t.Fatal("Legacy credential journal changed")
		}
	}
}
