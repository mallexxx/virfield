// Package xcodearchive downloads and verifies pinned Apple Xcode archives.
package xcodearchive

import (
	"context"
	"fmt"
	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/sys/unix"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	CacheDir, ImportDir, CookiePath string
	HTTP                            *http.Client
	mu                              sync.Mutex
	locks                           map[string]*sync.Mutex
}

func New(cache, cookies string) *Store {
	return &Store{CacheDir: cache, CookiePath: cookies, HTTP: &http.Client{Timeout: 30 * time.Minute}}
}
func (s *Store) Archive(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	if !filepath.IsAbs(s.CacheDir) {
		return "", domain.Err("invalid_profile", "Xcode cache must be an absolute operator-owned directory")
	}
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	lock := s.locks[x.SHA1]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[x.SHA1] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.archive(ctx, x, progress)
}
func Space(path string, required int64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return err
	}
	available := int64(st.Bavail) * int64(st.Bsize)
	if available < required {
		return domain.Err("disk_space", fmt.Sprintf("Need %.1f GiB free; available %.1f GiB. Free space before retrying the image build.", float64(required)/(1<<30), float64(available)/(1<<30)))
	}
	return nil
}
