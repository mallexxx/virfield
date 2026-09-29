package hostlock

import (
	"path/filepath"
	"testing"
)

func TestOnlyOneOwnerAndRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lock")
	a, err := acquireAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := acquireAt(dir); err == nil {
		b.Close()
		t.Fatal("second owner allowed")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := acquireAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
