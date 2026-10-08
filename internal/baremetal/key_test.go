package baremetal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementKeyIsCreatedOnceAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes", "management")
	signer, public, err := LoadOrCreateKey(path)
	if err != nil || signer == nil || !strings.HasPrefix(public, "ssh-ed25519 ") {
		t.Fatal(public, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode(), err)
	}
	if b, err := os.ReadFile(path + ".pub"); err != nil || strings.TrimSpace(string(b)) != public+" virfield-node-management" {
		t.Fatal("public key file differs", err)
	}
	_, again, err := LoadOrCreateKey(path)
	if err != nil || again != public {
		t.Fatal("key was regenerated", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateKey(path); err == nil {
		t.Fatal("accepted a readable private key")
	}
}
