package control

import (
	"context"
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
	for name, want := range map[string]string{"config.json": "configured settings", "token": "configured secret"} {
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
}
