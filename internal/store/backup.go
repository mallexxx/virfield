package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Backup writes a transactionally consistent SQLite snapshot, then opens it and
// validates integrity before publishing it. The caller owns a private directory.
func (s *Store) Backup(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("backup path must be absolute")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("backup destination must be new")
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	restored, err := Open(path)
	if err != nil {
		return err
	}
	defer restored.Close()
	var result string
	if err := restored.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("backup integrity validation failed")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
