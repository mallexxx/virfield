package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

func (c *Controller) Backup(ctx context.Context, stateDir string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.healthy(); err != nil {
		return "", err
	}
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return "", err
	}
	if len(c.active) > 0 || c.capacity(ls).Used > 0 {
		return "", domain.Err("operation_in_progress", "Backup requires idle manager and stopped VMs")
	}
	jobs, err := c.store.Jobs(ctx)
	if err != nil {
		return "", err
	}
	if len(jobs) > 0 {
		return "", domain.Err("operation_in_progress", "Resolve all jobs before backup")
	}
	id := domain.NewID("backup-")
	folder := filepath.Join(stateDir, "backups", id)
	if err := os.MkdirAll(filepath.Dir(folder), 0700); err != nil {
		return "", err
	}
	if err := os.Mkdir(folder, 0700); err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(folder)
		}
	}()
	if err := c.store.Backup(ctx, filepath.Join(folder, "state.db")); err != nil {
		return "", err
	}
	// The IPSW cache, live VM disks and diagnostic screenshots are not credentials.
	// Snapshot only verified state and its identities; the VMs remain managed by Lume.
	for _, l := range ls {
		scope := "leases"
		if l.Purpose == "image" {
			scope = "images"
		}
		for _, name := range []string{"credentials.json", "verification.json", "provision-verification.json"} {
			src := filepath.Join(stateDir, scope, l.ID, name)
			b, err := os.ReadFile(src)
			if os.IsNotExist(err) && name != "credentials.json" {
				continue
			}
			if err != nil {
				return "", err
			}
			dest := filepath.Join(folder, scope, l.ID)
			if err := os.MkdirAll(dest, 0700); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(dest, name), b, 0600); err != nil {
				return "", err
			}
		}
	}
	for _, name := range []string{"config.json", "token"} {
		b, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(folder, name), b, 0600); err != nil {
			return "", err
		}
	}
	manifest, _ := json.MarshalIndent(map[string]any{"format": 1, "id": id, "created_at": c.now(), "leases": len(ls), "database_integrity": "ok", "vm_disks_included": false}, "", "  ")
	if err := os.WriteFile(filepath.Join(folder, "manifest.json"), manifest, 0600); err != nil {
		return "", err
	}
	if err := filepath.WalkDir(folder, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	}); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "backups"))
	if err != nil {
		return "", err
	}
	type snapshot struct {
		name string
		at   int64
	}
	var completed []snapshot
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "backup-") || !domain.ValidName(entry.Name()) {
			continue
		}
		info, err := os.Stat(filepath.Join(stateDir, "backups", entry.Name(), "manifest.json"))
		if err == nil {
			completed = append(completed, snapshot{entry.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].at > completed[j].at })
	for _, old := range completed[min(5, len(completed)):] {
		if err := os.RemoveAll(filepath.Join(stateDir, "backups", old.name)); err != nil {
			return "", err
		}
	}
	complete = true
	return id, nil
}
