// Package images implements the image pipeline. It owns restore media, not VM
// lifecycle; all VM effects go through the Lume adapter.
package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/sys/unix"
)

// Download uses a content-addressed cache. Partial bytes can resume, but only a
// full size + SHA-256 verification permits an atomic promotion to .ipsw.
func Download(ctx context.Context, client *http.Client, dir string, p domain.ImageProfile, progress func(string) error) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, p.SHA256+".ipsw")
	verify := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || st.Size() != p.Size {
			return domain.Err("download_integrity", "IPSW size does not match the pinned manifest")
		}
		h := sha256.New()
		buf := make([]byte, 1024*1024)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, e := f.Read(buf)
			if n > 0 {
				_, _ = h.Write(buf[:n])
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
		}
		if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
			return domain.Err("download_integrity", "IPSW SHA-256 does not match the pinned manifest")
		}
		return nil
	}
	if st, err := os.Lstat(final); err == nil {
		if !st.Mode().IsRegular() {
			return "", domain.Err("unsafe_cache", "cached IPSW is not a regular file")
		}
		if err := verify(final); err != nil {
			return "", err
		}
		return final, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	partial := final + ".partial"
	if st, err := os.Lstat(partial); err == nil && !st.Mode().IsRegular() {
		return "", domain.Err("unsafe_cache", "partial download is not a regular file")
	}
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := st.Size()
	if offset > p.Size {
		return "", domain.Err("download_integrity", "partial download exceeds expected size")
	}
	if err := Space(dir, p.Size-offset+8<<30); err != nil {
		return "", err
	}
	if offset < p.Size {
		req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept-Encoding", "identity")
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		res, err := client.Do(req)
		if err != nil {
			return "", domain.Err("download_failed", "Apple download request failed; partial bytes are retained")
		}
		defer res.Body.Close()
		if offset > 0 && res.StatusCode == http.StatusOK {
			if err := Space(dir, p.Size+(8<<30)); err != nil {
				return "", err
			}
			offset = 0
			if err := f.Truncate(0); err != nil {
				return "", err
			}
		}
		if offset > 0 {
			var start, end, total int64
			if res.StatusCode != http.StatusPartialContent {
				return "", domain.Err("download_failed", "server did not honor range request")
			}
			_, err := fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total)
			if err != nil || start != offset || end != p.Size-1 || total != p.Size {
				return "", domain.Err("download_integrity", "invalid Content-Range for pinned IPSW")
			}
		} else if res.StatusCode != http.StatusOK {
			return "", domain.Err("download_failed", fmt.Sprintf("Apple restore download returned HTTP %d; partial bytes are retained", res.StatusCode))
		}
		if res.ContentLength != -1 && res.ContentLength != p.Size-offset {
			return "", domain.Err("download_integrity", "unexpected download length")
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return "", err
		}
		buf := make([]byte, 1024*1024)
		last := time.Time{}
		for {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			n, e := res.Body.Read(buf)
			if n > 0 {
				if offset+int64(n) > p.Size {
					return "", domain.Err("download_integrity", "server sent more bytes than expected")
				}
				if _, err := f.Write(buf[:n]); err != nil {
					return "", err
				}
				offset += int64(n)
				if time.Since(last) > 5*time.Second {
					if err := progress(fmt.Sprintf("Downloading IPSW: %.1f / %.1f GiB (%d%%)", float64(offset)/(1<<30), float64(p.Size)/(1<<30), offset*100/p.Size)); err != nil {
						return "", err
					}
					last = time.Now()
				}
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return "", domain.Err("download_failed", "Download interrupted; partial bytes retained for explicit retry")
			}
		}
		if err := f.Sync(); err != nil {
			return "", err
		}
	}
	if err := progress("Verifying IPSW SHA-256"); err != nil {
		return "", err
	}
	if err := verify(partial); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(partial, final); err != nil {
		return "", err
	}
	d, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return "", err
	}
	return final, nil
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
