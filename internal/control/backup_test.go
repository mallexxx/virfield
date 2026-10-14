package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRetentionAndIncompleteCleanup(t *testing.T) {
	c, _, _ := setup(t)
	dir := t.TempDir()
	configPath, tokenPath := filepath.Join(dir, "config.json"), filepath.Join(dir, "token")
	if _, err := c.Backup(context.Background(), dir, configPath, tokenPath); err == nil {
		t.Fatal("missing configuration accepted")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(entries) != 0 {
		t.Fatal("partial backup retained", err)
	}
	for _, name := range []string{"config.json", "token"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 7 {
		if _, err := c.Backup(context.Background(), dir, configPath, tokenPath); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(entries) != 5 {
		t.Fatal("retention failed", len(entries), err)
	}
}

func TestBackupUsesConfiguredExternalPaths(t *testing.T) {
	c, _, _ := setup(t)
	dir, external := t.TempDir(), t.TempDir()
	configPath, tokenPath := filepath.Join(external, "host.json"), filepath.Join(external, "api.secret")
	for path, content := range map[string]string{configPath: "configured settings", tokenPath: "configured secret", filepath.Join(dir, "token"): "stale secret"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := c.Backup(context.Background(), dir, configPath, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("configured secret"))
	for name, want := range map[string]string{"config.json": "configured settings", "token.sha256": hex.EncodeToString(digest[:]) + "\n"} {
		path := filepath.Join(dir, "backups", id, name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q, %v", name, got, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("backup secret permissions", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "backups", id, "token")); !os.IsNotExist(err) {
		t.Fatal("raw owner token was copied into backup")
	}
}

func TestBackupPreservesNodeIdentitiesAndRejectsSymlinks(t *testing.T) {
	c, _, _ := setup(t)
	dir := t.TempDir()
	for path, body := range map[string]string{"config.json": "settings", "token": "secret", "nodes/management/id_ed25519": "private-key", "nodes/htpc.probe.json": "cached-probe"} {
		target := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := c.Backup(context.Background(), dir, filepath.Join(dir, "config.json"), filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"nodes/management/id_ed25519": "private-key", "nodes/htpc.probe.json": "cached-probe"} {
		raw, err := os.ReadFile(filepath.Join(dir, "backups", id, path))
		if err != nil || string(raw) != want {
			t.Fatal(path, err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "token"), filepath.Join(dir, "nodes", "unexpected-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Backup(context.Background(), dir, filepath.Join(dir, "config.json"), filepath.Join(dir, "token")); err == nil {
		t.Fatal("followed node identity symlink")
	}
}
