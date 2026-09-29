package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestReopenAndAtomicRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	l := domain.Lease{ID: "lease-one", State: "pending", CreatedAt: now}
	j := domain.Job{ID: "job-one", LeaseID: l.ID, Kind: "prepare", State: "queued"}
	ctx := context.Background()
	if err := s.Save(ctx, l, &j, "request-one", "fp", "accepted", "saved"); err != nil {
		t.Fatal(err)
	}
	l.State = "ready"
	j.State = "succeeded"
	if err := s.Save(ctx, l, &j, "request-one", "fp", "ready", "must roll back"); err == nil {
		t.Fatal("duplicate idempotency key accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Lease(ctx, l.ID)
	if err != nil || got.State != "pending" {
		t.Fatalf("%+v %v", got, err)
	}
	es, err := s.Events(ctx, 0, "", 100)
	if err != nil || len(es) != 1 || es[0].Type != "accepted" {
		t.Fatalf("%+v %v", es, err)
	}
	replay, err := s.Replay(ctx, "request-one", "fp")
	if err != nil || !replay.Replayed {
		t.Fatalf("%+v %v", replay, err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("%v %v", st, err)
	}
}
func TestCleanupAcceptanceAtomicallyCancelsPrepare(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	l := domain.Lease{ID: "lease-one", State: "provisioning"}
	j := domain.Job{ID: "job-one", LeaseID: l.ID, Kind: "prepare", State: "running"}
	if err := s.Save(ctx, l, &j, "request-one", "fp", "accepted", ""); err != nil {
		t.Fatal(err)
	}
	cleanup := domain.Job{ID: "job-two", LeaseID: l.ID, Kind: "cleanup", State: "queued", Phase: "queued"}
	l.State = "releasing"
	if err := s.Save(ctx, l, &cleanup, "release-one", "release", "cleanup.queued", ""); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Job(ctx, j.ID)
	if old.State != "canceled" {
		t.Fatal(old)
	}
	jobs, _ := s.Jobs(ctx)
	if len(jobs) != 1 || jobs[0].ID != cleanup.ID {
		t.Fatal(jobs)
	}
	es, _ := s.Events(ctx, 1, l.ID, 1)
	if len(es) != 1 || es[0].Type != "cleanup.queued" {
		t.Fatal(es)
	}
}
func TestFutureSchemaRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version=99`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("future schema accepted")
	}
}
func TestMissingRecords(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Lease(context.Background(), "unknown")
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != "not_found" {
		t.Fatal(err)
	}
}
