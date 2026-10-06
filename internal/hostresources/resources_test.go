package hostresources

import (
	"path/filepath"
	"testing"
)

func TestDiskKeepsAvailableLocationsWhenFallbackIsUnavailable(t *testing.T) {
	available := t.TempDir()
	values, err := Disk(map[string]string{
		"available": available,
		"missing":   filepath.Join(t.TempDir(), "missing"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if values["available"] <= 0 {
		t.Fatalf("available storage not reported: %#v", values)
	}
	if _, ok := values["missing"]; ok {
		t.Fatalf("unavailable storage reported: %#v", values)
	}
}

func TestDiskFailsWhenEveryLocationIsUnavailable(t *testing.T) {
	if _, err := Disk(map[string]string{"missing": filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("all unavailable storage locations accepted")
	}
}
