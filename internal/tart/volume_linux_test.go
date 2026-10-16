package tart

import (
	"errors"
	"testing"
)

func TestLocalTartStorageRequiresMacOS(t *testing.T) {
	if _, err := statVolume(t.TempDir()); !errors.Is(err, errTartStorageUnsupported) {
		t.Fatal("Linux storage was accepted as a local Tart volume", err)
	}
	if _, err := mountPoint(t.TempDir()); !errors.Is(err, errTartStorageUnsupported) {
		t.Fatal("Linux mount was accepted for diskutil lookup", err)
	}
}
