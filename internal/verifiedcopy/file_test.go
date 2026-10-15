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

func TestSparseCopyDoesNotExpandSmallExtentsToIOBufferSize(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	f, err := os.OpenFile(src, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	const size = 128 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	block := bytes.Repeat([]byte{0x57}, 4096)
	for offset := int64(0); offset < size; offset += 1 << 20 {
		if _, err := f.WriteAt(block, offset); err != nil {
			t.Fatal(err)
		}
	}
	// APFS eagerly allocates the gaps above. Create actual small sparse extents
	// so this regression cannot silently skip the allocation failure it tests.
	for offset := int64(4096); offset < size; offset += 1 << 20 {
		if err := punchHole(f, offset, (1<<20)-4096); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before, _ := f.Stat()
	allocated := before.Sys().(*syscall.Stat_t).Blocks * 512
	if allocated > size/2 {
		t.Fatal("fixture is not sparse", allocated)
	}
	if _, err := File(context.Background(), f, dst); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Sys().(*syscall.Stat_t).Blocks * 512; got > allocated+(8<<20) {
		t.Fatalf("Sparse copy expanded %d allocated bytes to %d (logical size %d)", allocated, got, size)
	} else {
		t.Logf("source allocated %d; destination allocated %d; logical size %d", allocated, got, size)
	}
}

func TestSparseCopyAllHolesAndUnalignedFinalData(t *testing.T) {
	for _, tail := range []bool{false, true} {
		name := "all-holes"
		if tail {
			name = "unaligned-tail"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "copy")
			f, err := os.OpenFile(src, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			const size = (8 << 20) + 37
			if err := f.Truncate(size); err != nil {
				t.Fatal(err)
			}
			if tail {
				if _, err := f.WriteAt(bytes.Repeat([]byte{0x75}, 37), size-37); err != nil {
					t.Fatal(err)
				}
				if err := punchHole(f, 0, size-37); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.Seek(123, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := File(context.Background(), f, dst); err != nil {
				t.Fatal(err)
			}
			if position, err := f.Seek(0, 1); err != nil || position != 123 {
				t.Fatal("source file position changed", position, err)
			}
			want, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || len(got) != size || !bytes.Equal(got, want) {
				t.Fatal("sparse boundary content changed", err)
			}
		})
	}
}
