package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestSchemaEightSnapshotProtectsRevalidationFromOlderExecutors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: "lease-revalidate", State: "needs_attention", VMName: "vf-revalidate"}
	if err := s.Save(context.Background(), l, nil, "", "", "lease.drift", "Existing VM retained"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version=8`); err != nil {
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
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "migration-backups", fmt.Sprintf("schema-8-to-%d-*.db", SchemaVersion)))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	info, err := os.Stat(backups[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Migration snapshot is not private", err)
	}
	copy, err := sql.Open("sqlite", "file:"+backups[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var integrity string
	if err := copy.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 8 {
		t.Fatal(version, err)
	}
	if err := copy.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	var body string
	if err := copy.QueryRow(`SELECT body FROM leases WHERE id=?`, l.ID).Scan(&body); err != nil || !strings.Contains(body, l.VMName) {
		t.Fatal("Compatible snapshot lost owned VM", err)
	}
	// Executors limited to schema 8 must reject newer journals before dispatch.
	if SchemaVersion <= 8 {
		t.Fatal("Old executor could interpret revalidation as clone")
	}
}

func TestSchemaNineSnapshotPreservesPendingPinnedToolchain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: "image-toolchain", Purpose: "image", VMName: "legacy-profile", State: "image_building"}
	j := domain.Job{ID: "job-toolchain", LeaseID: l.ID, Kind: "image_build", State: "queued", Phase: "queued", Image: &domain.ImageProfile{Toolchain: "legacy-ios-v1", ToolchainSHA256: strings.Repeat("a", 64)}}
	if err := s.Save(context.Background(), l, &j, "pinned-request", "fingerprint", "image.accepted", "Pinned profile"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version=9`); err != nil {
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
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != SchemaVersion || SchemaVersion <= 9 {
		t.Fatal(version, err)
	}
	job, err := s.Job(context.Background(), j.ID)
	if err != nil || job.Image == nil || job.Image.Toolchain != j.Image.Toolchain || job.Image.ToolchainSHA256 != j.Image.ToolchainSHA256 {
		t.Fatal(job, err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "migration-backups", fmt.Sprintf("schema-9-to-%d-*.db", SchemaVersion)))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	snapshot, err := sql.Open("sqlite", "file:"+backups[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err := snapshot.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 9 {
		t.Fatal(version, err)
	}
	var integrity string
	if err := snapshot.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
}
