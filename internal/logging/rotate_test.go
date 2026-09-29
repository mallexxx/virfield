package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotationIsBounded(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "daemon.jsonl"), 100)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if _, err := w.Write([]byte("1234567890\n")); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 4 {
		t.Fatal(len(files), err)
	}
	for _, f := range files {
		s, _ := f.Info()
		if s.Size() > 100 || s.Mode().Perm() != 0600 {
			t.Fatal("unbounded or unprotected log")
		}
	}
}

func TestRotationRecoversAndBoundsOversizedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.jsonl")
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory makes the archive rename fail deterministically,
	// including when tests run as root. Removing it simulates storage recovery.
	if err := os.Mkdir(path+".3", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".3/block", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".2", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("rotation error hidden")
	}
	if err := os.RemoveAll(path + ".2"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path + ".3"); err != nil {
		t.Fatal(err)
	}
	p := make([]byte, 47)
	if n, err := w.Write(p); err != nil || n != len(p) {
		t.Fatal(n, err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) > 4 {
		t.Fatal("retention exceeded")
	}
	for _, f := range files {
		s, _ := f.Info()
		if s.Size() > 10 {
			t.Fatal("oversized log")
		}
	}
}
