package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestSchemaSevenSnapshotAndThirtyDayRetention(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	save := func(id, state string, updated time.Time, jobState string) {
		t.Helper()
		lease := domain.Lease{ID: id, State: state, CreatedAt: updated, UpdatedAt: updated}
		job := domain.Job{ID: "job-" + id, LeaseID: id, Kind: "prepare", State: jobState, CreatedAt: updated, UpdatedAt: updated}
		if err := s.Save(ctx, lease, &job, "key-"+id, "fp", "accepted", id); err != nil {
			t.Fatal(err)
		}
	}
	save("old-released", "released", now.Add(-32*24*time.Hour), "succeeded")
	save("new-released", "released", now.Add(-29*24*time.Hour), "succeeded")
	save("old-active", "ready", now.Add(-40*24*time.Hour), "succeeded")
	save("old-unfinished", "released", now.Add(-40*24*time.Hour), "running")
	if _, err := s.db.Exec(`UPDATE events SET at=? WHERE lease_id IN ('old-released','old-active','old-unfinished')`, now.Add(-35*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP INDEX jobs_lease; DROP INDEX events_at; PRAGMA user_version=6`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 7 {
		t.Fatalf("schema version = %d: %v", version, err)
	}
	for _, name := range []string{"jobs_lease", "events_at"} {
		var got string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&got); err != nil || got != name {
			t.Fatalf("index %s missing: %v", name, err)
		}
	}
	backups, err := os.ReadDir(filepath.Join(filepath.Dir(path), "migration-backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one pre-migration backup, got %d: %v", len(backups), err)
	}
	backupPath := filepath.Join(filepath.Dir(path), "migration-backups", backups[0].Name())
	info, err := os.Stat(backupPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup permissions = %v: %v", info, err)
	}
	copy, err := sql.Open("sqlite", "file:"+backupPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var oldVersion, oldLeaseCount int
	if err := copy.QueryRow(`PRAGMA user_version`).Scan(&oldVersion); err != nil || oldVersion != 6 {
		t.Fatalf("backup version = %d: %v", oldVersion, err)
	}
	if err := copy.QueryRow(`SELECT count(*) FROM leases`).Scan(&oldLeaseCount); err != nil || oldLeaseCount != 4 {
		t.Fatalf("backup leases = %d: %v", oldLeaseCount, err)
	}
	got, err := s.Prune(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Leases != 1 || got.Jobs != 1 || got.Requests != 1 || got.Events != 3 {
		t.Fatalf("unexpected prune counts: %+v", got)
	}
	if _, err := s.Lease(ctx, "old-released"); err == nil {
		t.Fatal("old completed lease survived")
	}
	if replay, err := s.Replay(ctx, "key-old-released", "fp"); err != nil || replay != nil {
		t.Fatalf("expired idempotency key survived: %+v %v", replay, err)
	}
	for _, id := range []string{"new-released", "old-active", "old-unfinished"} {
		if _, err := s.Lease(ctx, id); err != nil {
			t.Fatalf("retained lease %s: %v", id, err)
		}
		if replay, err := s.Replay(ctx, "key-"+id, "fp"); err != nil || replay == nil {
			t.Fatalf("retained idempotency key %s: %+v %v", id, replay, err)
		}
	}
	if repeat, err := s.Prune(ctx, now.Add(-30*24*time.Hour)); err != nil || repeat != (PruneResult{}) {
		t.Fatalf("prune should be idempotent: %+v %v", repeat, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("prune broke foreign keys", rows.Err())
	}
}

func TestPruneInvalidLeaseRollsBack(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := time.Now().Add(-40 * 24 * time.Hour).UTC()
	l := domain.Lease{ID: "good", State: "released", UpdatedAt: old}
	j := domain.Job{ID: "job-good", LeaseID: l.ID, State: "succeeded"}
	if err := s.Save(ctx, l, &j, "key-good", "fp", "accepted", "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO leases(id,state,body) VALUES('broken','released','{')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(ctx, time.Now().Add(-30*24*time.Hour)); err == nil {
		t.Fatal("invalid lease body accepted")
	}
	if replay, err := s.Replay(ctx, "key-good", "fp"); err != nil || replay == nil {
		t.Fatalf("prune did not roll back: %+v %v", replay, err)
	}
}

func TestMigrationFailsClosedWithoutPrivateBackupDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP INDEX jobs_lease; DROP INDEX events_at; PRAGMA user_version=6`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(path), "migration-backups")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path); err == nil {
		reopened.Close()
		t.Fatal("migration accepted an exposed backup directory")
	}
	copy, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var version int
	if err := copy.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 6 {
		t.Fatalf("failed migration changed schema: %d %v", version, err)
	}
}
