package baremetal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runScript executes a fixed node script locally with a private HOME, as the
// node account would over SSH.
func runScript(t *testing.T, home, script, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, "sh"}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestLeaseScriptsReserveIsolateAndCleanUp(t *testing.T) {
	home := t.TempDir()
	hold := filepath.Join(home, "hold-file")
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0700); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(ssh, "authorized_keys")
	operator := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOperatorKeyOperatorKeyOperatorKey00 operator@laptop\n"
	if err := os.WriteFile(keys, []byte(operator), 0600); err != nil {
		t.Fatal(err)
	}
	vm := "vf-0123456789abcdef"
	lease := filepath.Join(home, "virfield", "leases", vm)

	runScript(t, home, cloneScript, "", vm)
	if b, _ := os.ReadFile(filepath.Join(lease, "state")); string(b) != "stopped" {
		t.Fatalf("clone state = %q", b)
	}
	runScript(t, home, startScript, "", vm, hold)
	if b, _ := os.ReadFile(filepath.Join(lease, "state")); string(b) != "running" {
		t.Fatalf("start state = %q", b)
	}
	if _, err := os.Stat(hold); err != nil {
		t.Fatal("start did not create the keep-awake file", err)
	}

	client := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIClientKeyClientKeyClientKeyClient000"
	for range 2 {
		if out := strings.TrimSpace(runScript(t, home, prepareScript, client+"\n", vm)); out != "1" {
			t.Fatalf("prepare reported %q tagged lines", out)
		}
	}
	probe := runScript(t, home, probeScript, "")
	if !strings.Contains(probe, "lease="+vm+" running") {
		t.Fatal("probe does not report the running lease:\n" + probe)
	}

	runScript(t, home, stopScript, "", vm, hold)
	if _, err := os.Stat(hold); !os.IsNotExist(err) {
		t.Fatal("stop left the keep-awake file it created")
	}
	runScript(t, home, deleteScript, "", vm, hold)
	b, _ := os.ReadFile(keys)
	if string(b) != operator {
		t.Fatalf("delete changed untagged keys or kept the lease key:\n%s", b)
	}
	if _, err := os.Stat(lease); !os.IsNotExist(err) {
		t.Fatal("delete kept the lease directory")
	}
	runScript(t, home, deleteScript, "", vm, hold) // idempotent
}

func TestStartNeverRemovesAKeepAwakeFileItDidNotCreate(t *testing.T) {
	home := t.TempDir()
	hold := filepath.Join(home, "hold-file")
	if err := os.WriteFile(hold, nil, 0600); err != nil {
		t.Fatal(err)
	}
	vm := "vf-feedfacefeedface"
	runScript(t, home, cloneScript, "", vm)
	runScript(t, home, startScript, "", vm, hold)
	runScript(t, home, stopScript, "", vm, hold)
	if _, err := os.Stat(hold); err != nil {
		t.Fatal("removed a keep-awake file owned by another process")
	}
}
