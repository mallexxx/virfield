// Package logging bounds persistent daemon output without an external rotator.
package logging

import (
	"fmt"
	"os"
	"sync"
)

type Writer struct {
	mu          sync.Mutex
	path        string
	file        *os.File
	size, limit int64
	closed      bool
}

func Open(path string, limit int64) (*Writer, error) {
	if limit < 1 {
		return nil, fmt.Errorf("log limit must be positive")
	}
	w := &Writer{path: path, limit: limit}
	if err := w.reopen(); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *Writer) reopen() error {
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	s, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file, w.size = f, s.Size()
	return nil
}
func (w *Writer) rotate() error {
	// Keep the active descriptor usable if an archive rename fails. A later
	// write retries rotation after the filesystem problem has been resolved.
	for i := 2; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return err
	}
	err := w.file.Close()
	w.file = nil
	if err != nil {
		return err
	}
	return w.reopen()
}
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.file == nil {
		if err := w.reopen(); err != nil {
			return 0, err
		}
	}
	total := 0
	for len(p) > 0 {
		// Preserve ordinary records in one file; oversized writes are split so
		// even a noisy child cannot exceed the configured per-file bound.
		if w.size >= w.limit || (int64(len(p)) <= w.limit && w.size+int64(len(p)) > w.limit) {
			if err := w.rotate(); err != nil {
				return total, err
			}
		}
		chunk := min(int64(len(p)), w.limit-w.size)
		n, err := w.file.Write(p[:chunk])
		total += n
		w.size += int64(n)
		p = p[n:]
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}
