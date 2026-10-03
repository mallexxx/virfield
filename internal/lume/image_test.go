package lume

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestPrivateProcessBoundsLogsAndHidesOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "stage.log")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "head -c 10000000 /dev/zero; printf secret-diagnostic >&2; exit 7")
	err := RunPrivate(ctx, cmd, path)
	failure, ok := err.(*domain.Error)
	if !ok || failure.Code != "image_command_failed" {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 8<<20 || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if failure.Message != "Image subprocess failed; inspect the private stage log before recovery" {
		t.Fatal("raw child output escaped", failure)
	}
}

func TestPrivateProcessCancellationKillsOnlyOwnedGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "orphan")
	// If only the parent is killed, the child survives and creates the marker.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `(sleep 1; printf orphan > "$1") & wait`, "test", marker)
	started := time.Now()
	err := RunPrivate(ctx, cmd, filepath.Join(dir, "stage.log"))
	failure, ok := err.(*domain.Error)
	if !ok || failure.Code != "image_interrupted" || time.Since(started) > 2*time.Second {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("owned descendant survived cancellation", err)
	}
}

func TestImagePipelineRejectsStockLumeVersion(t *testing.T) {
	for _, tc := range []struct {
		version  string
		accepted bool
	}{{"0.5.3", false}, {"0.5.3-virfield6", true}} {
		path := filepath.Join(t.TempDir(), "lume")
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+tc.version+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		err := VerifyImageVersion(context.Background(), path)
		if (err == nil) != tc.accepted {
			t.Fatal(tc.version, err)
		}
	}
}

func TestImageCreatePassesSelectedDiskSizeToLume(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lume")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{VMName: "monterey-xcode", Location: "home"}
	if err := ImageCommand(context.Background(), binary, dir, dir, "create", l, "/tmp/install.ipsw", "1920x1440", "120GB"); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "create.log"))
	if err != nil || !strings.Contains(string(out), "--disk-size\n120GB\n") {
		t.Fatalf("Lume create args: %s, %v", out, err)
	}
	if err := ImageCommand(context.Background(), binary, dir, dir, "create", l, "/tmp/install.ipsw", "1920x1440", "160GB"); err == nil {
		t.Fatal("unapproved disk size accepted")
	}
}
