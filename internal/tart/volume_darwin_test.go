package tart

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStatVolumeUsesActualDirectoryAndAllocatableBytes(t *testing.T) {
	dir := t.TempDir()
	v, err := statVolume(dir)
	if err != nil || v.mount.point == "" || v.mount.point == dir || v.total <= 0 || v.free < 0 || v.free > v.total {
		t.Fatal(v, err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := statVolume(file); err == nil {
		t.Fatal("file accepted as storage directory")
	}
	if _, err := statVolume(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("absent directory accepted", err)
	}
}
