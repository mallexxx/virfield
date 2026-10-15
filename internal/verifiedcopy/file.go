// Package verifiedcopy copies private regular files without overwriting a
// destination, preserves sparse zero ranges, and verifies both sides afterward.
package verifiedcopy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"
)

func Open(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("copy source must be a regular file")
	}
	return f, nil
}

func File(ctx context.Context, source *os.File, destination string) (string, error) {
	before, err := source.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("copy source is not regular")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer out.Close()
	first := sha256.New()
	buf, zero := make([]byte, 1<<20), make([]byte, 1<<20)
	var offset int64
	for offset < before.Size() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := source.ReadAt(buf[:min(int64(len(buf)), before.Size()-offset)], offset)
		if err != nil && err != io.EOF {
			return "", err
		}
		if n == 0 {
			return "", io.ErrUnexpectedEOF
		}
		first.Write(buf[:n])
		if !bytes.Equal(buf[:n], zero[:n]) {
			if _, err := out.WriteAt(buf[:n], offset); err != nil {
				return "", err
			}
		}
		offset += int64(n)
	}
	if err := out.Truncate(before.Size()); err != nil {
		return "", err
	}
	if err := preserveHoles(ctx, source, out, before.Size()); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	copy, err := Open(destination)
	if err != nil {
		return "", err
	}
	defer copy.Close()
	for _, file := range []*os.File{source, copy} {
		h := sha256.New()
		var offset int64
		for offset < before.Size() {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			n, err := file.ReadAt(buf[:min(int64(len(buf)), before.Size()-offset)], offset)
			if err != nil && err != io.EOF {
				return "", err
			}
			if n == 0 {
				return "", io.ErrUnexpectedEOF
			}
			h.Write(buf[:n])
			offset += int64(n)
		}
		if !bytes.Equal(first.Sum(nil), h.Sum(nil)) {
			return "", errors.New("source or copied content changed")
		}
	}
	after, err := source.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("source identity changed during copy")
	}
	return hex.EncodeToString(first.Sum(nil)), nil
}

func SyncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
