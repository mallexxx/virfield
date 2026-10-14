package guestssh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeychainDiagnosticReplacesSymlinkWithoutWritingItsTarget(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "credentials.json")
	if err := os.WriteFile(target, []byte("private-fixture-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "keychain.log")); err != nil {
		t.Fatal(err)
	}
	if err := writeKeychainDiagnostic(directory, "gui", "native probe failed", "private agent diagnostic"); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(target)
	if err != nil || string(unchanged) != "private-fixture-credential" {
		t.Fatal("Diagnostic overwrote identity", err)
	}
	info, err := os.Lstat(filepath.Join(directory, "keychain.log"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("Diagnostic is not a private regular file", info, err)
	}
}
