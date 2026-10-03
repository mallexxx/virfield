package lume

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestRecoverySessionEndpointRequiresThisLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	password := "private8"
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"url":"vnc://:private8@127.0.0.1:53421"}`)
	if endpoint, err := recoverySessionEndpoint(path, password); err != nil || endpoint != "vnc://:private8@127.0.0.1:53421" {
		t.Fatalf("valid session: %q, %v", endpoint, err)
	}
	for _, bad := range []string{
		`{"url":"vnc://:stale123@127.0.0.1:53421"}`,
		`{"url":"vnc://:private8@192.0.2.1:53421"}`,
		`{"url":"vnc://:private8@127.0.0.1:0"}`,
		`{"url":"vnc://:private8@127.0.0.1:53421/path"}`,
		`{"url":"http://:private8@127.0.0.1:53421"}`,
		`{"url":"vnc://:private8@127.0.0.1:53421"}` + strings.Repeat(" ", 1024),
	} {
		write(bad)
		if _, err := recoverySessionEndpoint(path, password); err == nil {
			t.Fatalf("accepted bad session %q", bad[:min(len(bad), 80)])
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := recoverySessionEndpoint(path, password); err == nil {
		t.Fatal("accepted symlink session")
	}
}

func TestRecoveryUsesPrivatePasswordFileAndKernelPort(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	sessionPath := filepath.Join(dir, "sessions.json")
	binary := filepath.Join(dir, "fake-lume")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$VIRFIELD_TEST_ARGS"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--vnc-password-file" ]; then password_file="$2"; break; fi
  shift
done
password=$(cat "$password_file") || exit 1
printf '{"url":"vnc://:%s@127.0.0.1:54321"}' "$password" > "$VIRFIELD_TEST_SESSION"
sleep 30
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIRFIELD_TEST_ARGS", argsPath)
	t.Setenv("VIRFIELD_TEST_SESSION", sessionPath)
	want := errors.New("driver finished")
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	err = c.Recovery(context.Background(), binary, filepath.Join(dir, "private"), sessionPath,
		domain.Lease{VMName: "recovery-test", Location: "home"},
		func(_ context.Context, endpoint string) error {
			if _, err := recoverySessionEndpoint(sessionPath, strings.TrimPrefix(strings.SplitN(endpoint, "@", 2)[0], "vnc://:")); err != nil {
				t.Fatal(err)
			}
			return want
		})
	if !errors.Is(err, want) {
		t.Fatalf("driver result: %v", err)
	}
	b, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(b)
	if strings.Contains(args, "--vnc-password\n") || !strings.Contains(args, "--vnc-password-file\n") || !strings.Contains(args, "--vnc-port\n0\n") {
		t.Fatalf("unsafe Recovery arguments: %q", args)
	}
	passwordFile := strings.TrimSpace(strings.SplitAfter(args, "--vnc-password-file\n")[1])
	if _, err := os.Stat(passwordFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private password file not removed: %v", err)
	}
}
