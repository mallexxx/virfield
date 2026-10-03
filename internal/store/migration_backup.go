package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// snapshotBeforeMigration preserves the old schema before any automatic DDL or
// retention can run. The containing directory is private even during SQLite's
// initial creation of the VACUUM INTO file.
func (s *Store) snapshotBeforeMigration(version int) error {
	dir := filepath.Join(filepath.Dir(s.path), "migration-backups")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 || !ok || int(owner.Uid) != os.Getuid() {
		return fmt.Errorf("migration backup directory must be private and owned by the service user")
	}
	name := fmt.Sprintf("schema-%d-to-7-%s-%s.db", version, time.Now().UTC().Format("20060102T150405Z"), domain.NewID(""))
	path := filepath.Join(dir, name)
	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		_ = os.Remove(path)
		return err
	}
	valid := false
	defer func() {
		if !valid {
			_ = os.Remove(path)
		}
	}()
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: path}
	copy, err := sql.Open("sqlite", u.String()+"?mode=ro")
	if err != nil {
		return err
	}
	defer copy.Close()
	var gotVersion int
	if err := copy.QueryRow(`PRAGMA user_version`).Scan(&gotVersion); err != nil {
		return err
	}
	if gotVersion != version {
		return fmt.Errorf("migration snapshot schema mismatch: got %d, want %d", gotVersion, version)
	}
	var integrity string
	if err := copy.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("migration snapshot failed integrity check: %s", integrity)
	}
	rows, err := copy.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := rows.Next()
	readErr := rows.Err()
	if err := rows.Close(); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if broken {
		return fmt.Errorf("migration snapshot has broken foreign keys")
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	if err := d.Close(); err != nil {
		return err
	}
	valid = true
	return nil
}
