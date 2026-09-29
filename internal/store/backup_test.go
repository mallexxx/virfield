package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestBackupRestoresJournalAndIdempotency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l := domain.Lease{ID: "lease-backup", State: "ready"}
	j := domain.Job{ID: "job-backup", LeaseID: l.ID, State: "succeeded"}
	if err := s.Save(ctx, l, &j, "backup-request", "fp", "ready", "retained"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "backup.db")
	if err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	op, err := restored.Replay(ctx, "backup-request", "fp")
	if err != nil || !op.Replayed || op.Lease.ID != l.ID {
		t.Fatal("restore lost idempotency", err)
	}
	if err := s.Backup(ctx, path); err == nil {
		t.Fatal("overwrote backup")
	}
}

func TestBackupWriteFailurePreservesLiveJournal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l := domain.Lease{ID: "lease-live", State: "ready"}
	if err := s.Save(ctx, l, nil, "", "", "ready", "keep"); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(ctx, filepath.Join(dir, "missing", "backup.db")); err == nil {
		t.Fatal("expected storage failure")
	}
	if got, err := s.Lease(ctx, l.ID); err != nil || got.State != "ready" {
		t.Fatal("live journal damaged", err)
	}
}
