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

func TestAgentFailureIsPrivateAndBoundToItsRequest(t *testing.T) {
	script := guestscripts.Keychain
	start := strings.Index(script, "report_exit() {")
	if start < 0 {
		t.Fatal("Missing production EXIT reporter")
	}
	end := strings.Index(script[start:], "aqua_session=")
	if end < 0 {
		t.Fatal("Missing production EXIT reporter")
	}
	dir := t.TempDir()
	cmd := exec.Command("/bin/bash", "-c", `set -eu; dir="$VF_TEST_DIR"; scope=lease-fixture; boot=boot-fixture; mode=ensure; nonce=keychain-abc; vf_phase=login-rekey-previous; `+script[start:start+end]+"\nexit 65")
	cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir)
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 65 {
		t.Fatal("Reporter hid agent failure", err)
	}
	failure := filepath.Join(dir, "failure")
	data, err := os.ReadFile(failure)
	if err != nil || string(data) != "lease-fixture\nboot-fixture\nensure\nkeychain-abc\n65\nlogin-rekey-previous\n" {
		t.Fatal("Missing exact failure identity/phase", err)
	}
	info, err := os.Stat(failure)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Failure record is not private", err)
	}
}

func TestInstallerFailsPromptlyOnlyForItsAgentRequest(t *testing.T) {
	script := guestscripts.Keychain
	start := strings.Index(script, "while (( SECONDS < deadline )); do\n  if [[ -f \"$dir/result\" ]]")
	if start < 0 {
		t.Fatal("Missing production result wait")
	}
	end := strings.Index(script[start:], "echo 'GUI keychain agent did not complete")
	if end < 0 {
		t.Fatal("Missing result wait end")
	}
	block := script[start : start+end]
	exact := "lease-fixture\nboot-fixture\nensure\nkeychain-abc\n65\nlogin-rekey-previous\n"
	for _, tc := range []struct {
		name, failure   string
		result, symlink bool
		exit            int
	}{
		{name: "current failure", failure: exact, exit: 65},
		{name: "other boot", failure: strings.Replace(exact, "boot-fixture", "boot-old", 1), exit: 92},
		{name: "other request", failure: strings.Replace(exact, "keychain-abc", "keychain-old", 1), exit: 92},
		{name: "other scope", failure: strings.Replace(exact, "lease-fixture", "lease-other", 1), exit: 92},
		{name: "other mode", failure: strings.Replace(exact, "ensure", "verify", 1), exit: 92},
		{name: "complete result", failure: exact, result: true, exit: 0},
		{name: "failure symlink", failure: exact, symlink: true, exit: 92},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "failure")
			if tc.symlink {
				target = filepath.Join(dir, "untrusted")
			}
			if err := os.WriteFile(target, []byte(tc.failure), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.symlink {
				if err := os.Symlink(target, filepath.Join(dir, "failure")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.result {
				if err := os.WriteFile(filepath.Join(dir, "result"), []byte(`{"ready":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", `set -eu; dir="$VF_TEST_DIR"; scope=lease-fixture; boot=boot-fixture; mode=ensure; nonce=keychain-abc; deadline=$((SECONDS+60)); sleep(){ exit 92; }; `+block)
			cmd.Env = append(os.Environ(), "VF_TEST_DIR="+dir)
			out, err := cmd.CombinedOutput()
			exit := 0
			if err != nil {
				e, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				exit = e.ExitCode()
			}
			if ctx.Err() != nil || exit != tc.exit {
				t.Fatal("Incorrect fresh/stale failure handling", exit, string(out), ctx.Err())
			}
			if tc.exit == 65 && !strings.Contains(string(out), "exited unsuccessfully") {
				t.Fatal("Failure did not explain agent exit")
			}
		})
	}
}
