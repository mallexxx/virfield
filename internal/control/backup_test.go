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
	if _, err := c.Backup(context.Background(), dir); err == nil {
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
		if _, err := c.Backup(context.Background(), dir); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(entries) != 5 {
		t.Fatal("retention failed", len(entries), err)
	}
}
