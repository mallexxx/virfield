package verifiedcopy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestVerifiedSparseCopyDoesNotOverwriteAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "copy")
	// APFS eagerly allocates small files, even when their contents are sparse.
	data := make([]byte, 128<<20)
	copy(data[1<<20:], []byte("retained workspace and private VM identity"))
	if err := os.WriteFile(src, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	digest, err := File(context.Background(), f, dst)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if digest != hex.EncodeToString(want[:]) {
		t.Fatal(digest)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(data, got) {
		t.Fatal("content", err)
	}
	st, _ := os.Stat(dst)
	if st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Blocks*512 >= int64(len(data)) {
		t.Fatal("not private or sparse", st)
	}
	if _, err := File(context.Background(), f, dst); err == nil {
		t.Fatal("overwrote existing destination")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	if f, err := Open(link); err == nil {
		f.Close()
		t.Fatal("followed source symlink")
	}
}

func TestCancelledCopyCannotBecomeVerified(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	os.WriteFile(src, []byte("source retained"), 0600)
	f, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if digest, err := File(ctx, f, filepath.Join(dir, "partial")); err == nil || digest != "" {
		t.Fatal("canceled copy accepted", digest, err)
	}
	got, _ := os.ReadFile(src)
	if string(got) != "source retained" {
		t.Fatal("source changed")
	}
}
