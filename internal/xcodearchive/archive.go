package xcodearchive

import (
	"bufio"
	"context"
	"crypto/sha1" // Catalog legacy checksum; Apple's XIP signature is also mandatory.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

const maxXcodeArchive = 32 << 30

// CachedXcode reports archives verified by the download stage. The marker is
// tied to file metadata; use still rechecks the full checksum.
func (e *Store) CachedXcode(x domain.XcodeRelease) bool {
	if x.Validate() != nil {
		return false
	}
	path := filepath.Join(e.CacheDir, x.SHA1+".xip")
	return xcodeVerifiedMarker(path)
}

func xcodeVerifiedMarker(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	marker, err := os.ReadFile(path + ".verified")
	return err == nil && string(marker) == fmt.Sprintf("%d:%d\n", st.Size(), st.ModTime().UnixNano())
}

func markXcodeVerified(path string) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return
	}
	marker := path + ".verified"
	tmp := marker + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d:%d\n", st.Size(), st.ModTime().UnixNano())), 0600); err == nil {
		_ = os.Rename(tmp, marker)
	}
}

// Apple credentials are operator-owned files, never API fields or durable job data.
func appleCookies(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 64<<10 {
		return "", domain.Err("apple_auth_required", "apple_cookies must be a private regular Netscape cookie file (chmod 600)")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	parts := []string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			continue
		}
		host := strings.TrimPrefix(fields[0], ".")
		if host != "download.developer.apple.com" && host != "developer.apple.com" && !(fields[1] == "TRUE" && host == "apple.com") {
			continue
		}
		expiry, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || (expiry != 0 && expiry < time.Now().Unix()) {
			continue
		}
		if fields[2] != "/" && !strings.HasPrefix(fields[2], "/Developer_Tools/") {
			continue
		}
		cookie := &http.Cookie{Name: fields[5], Value: fields[6]}
		if cookie.Valid() != nil {
			continue
		}
		parts = append(parts, cookie.String())
	}
	if scanner.Err() != nil {
		return "", domain.Err("apple_auth_required", "Cannot read Apple cookie file")
	}
	return strings.Join(parts, "; "), nil
}
func Verify(ctx context.Context, path string, x domain.XcodeRelease) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > maxXcodeArchive {
		return domain.Err("download_integrity", "Xcode archive must be a regular XIP file up to 32 GiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha1.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != x.SHA1 {
		return domain.Err("download_integrity", "Xcode archive checksum does not match the selected release")
	}
	return nil
}
func (e *Store) archive(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		path, err := e.xcodeArchiveOnce(ctx, x, progress)
		var failure *domain.Error
		if err == nil || !errors.As(err, &failure) || failure.Code != "download_failed" || attempt == 2 {
			return path, err
		}
		wait := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			wait.Stop()
			return "", ctx.Err()
		case <-wait.C:
		}
	}
	return "", domain.Err("download_failed", "Xcode download attempts exhausted")
}

func (e *Store) xcodeArchiveOnce(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	if err := x.Validate(); err != nil {
		return "", err
	}
	dir := e.CacheDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, x.SHA1+".xip")
	if st, err := os.Lstat(final); err == nil {
		if !st.Mode().IsRegular() {
			return "", domain.Err("unsafe_cache", "Xcode download cache is not a regular file")
		}
		if err := Verify(ctx, final, x); err == nil {
			markXcodeVerified(final)
			return final, nil
		} else if !isIntegrityError(err) {
			return "", err
		}
		if err := quarantineCache(final); err != nil {
			return "", err
		}
		_ = os.Remove(final + ".verified")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	partial := final + ".partial"
	if st, err := os.Lstat(partial); err == nil {
		if !st.Mode().IsRegular() {
			return "", domain.Err("unsafe_cache", "Xcode download cache is not a regular file")
		}
		if st.Size() > maxXcodeArchive {
			if err := quarantineCache(partial); err != nil {
				return "", err
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	var source *os.File
	if e.ImportDir != "" {
		path := filepath.Join(e.ImportDir, "Xcode_"+x.Version+".xip")
		if st, err := os.Lstat(path); err == nil {
			if !st.Mode().IsRegular() || st.Size() > maxXcodeArchive {
				return "", domain.Err("download_integrity", "Imported Xcode archive must be a regular file up to 32 GiB")
			}
			source, err = os.Open(path)
			if err != nil {
				return "", err
			}
			defer source.Close()
		} else if !os.IsNotExist(err) {
			return "", err
		}
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
	resumed := offset > 0
	if offset > maxXcodeArchive {
		return "", domain.Err("download_integrity", "Partial Xcode archive is too large")
	}
	var reader io.Reader
	var total int64
	if source != nil {
		st, err := source.Stat()
		if err != nil {
			return "", err
		}
		total = st.Size()
		offset = 0
		if err := f.Truncate(0); err != nil {
			return "", err
		}
		reader = source
	} else {
		req, err := http.NewRequestWithContext(ctx, "GET", x.URL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept-Encoding", "identity")
		cookiePath := e.CookiePath
		if cookiePath != "" {
			if _, err := os.Lstat(cookiePath); os.IsNotExist(err) {
				cookiePath = ""
			} else if err != nil {
				return "", domain.Err("apple_auth_required", "Cannot inspect the private Apple session")
			}
		}
		cookies, err := appleCookies(cookiePath)
		if err != nil {
			return "", err
		}
		if cookies != "" {
			req.Header.Set("Cookie", cookies)
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		// Apple may send a signed CDN redirect after authentication. Follow only
		// HTTPS Apple hosts, and never forward the developer session onward.
		client := *e.HTTP
		client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			host := strings.ToLower(next.URL.Hostname())
			if len(via) >= 5 || next.URL.Scheme != "https" || next.URL.User != nil ||
				!(host == "apple.com" || strings.HasSuffix(host, ".apple.com") || strings.HasSuffix(host, ".cdn-apple.com")) ||
				host == "idmsa.apple.com" || strings.Contains(next.URL.Path, "/unauthorized") {
				return http.ErrUseLastResponse
			}
			next.Header.Del("Cookie")
			next.Header.Del("Authorization")
			next.Header.Del("Referer")
			return nil
		}
		res, err := client.Do(req)
		if err != nil {
			return "", domain.Err("download_failed", "Xcode download interrupted; partial bytes retained")
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
			if err := Verify(ctx, partial, x); err == nil {
				if err := f.Close(); err != nil {
					return "", err
				}
				if err := os.Rename(partial, final); err != nil {
					return "", err
				}
				markXcodeVerified(final)
				return final, nil
			} else if !isIntegrityError(err) {
				return "", err
			}
			if err := f.Close(); err != nil {
				return "", err
			}
			if err := quarantineCache(partial); err != nil {
				return "", err
			}
			return e.xcodeArchiveOnce(ctx, x, progress)
		}
		if res.StatusCode == 401 || res.StatusCode == 403 || (res.StatusCode >= 300 && res.StatusCode < 400) || strings.Contains(res.Header.Get("Content-Type"), "text/html") {
			return "", domain.Err("apple_auth_required", "Apple requires sign-in to download Xcode "+x.Version+". Call image_apple_auth for this job, send its local link privately, and wait for automatic download retry")
		}
		if res.StatusCode == 200 {
			offset = 0
			if err := f.Truncate(0); err != nil {
				return "", err
			}
			total = res.ContentLength
		} else if res.StatusCode == 206 && offset > 0 {
			var start, end int64
			n, err := fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total)
			if err != nil || n != 3 || start != offset || end != total-1 || total <= offset {
				return "", domain.Err("download_integrity", "Invalid Xcode download range")
			}
			if res.ContentLength >= 0 && res.ContentLength != total-offset {
				return "", domain.Err("download_integrity", "Invalid Xcode download length")
			}
		} else {
			return "", domain.Err("download_failed", fmt.Sprintf("Apple Xcode download returned HTTP %d; partial bytes are retained", res.StatusCode))
		}
		reader = res.Body
	}
	if total <= 0 || total > maxXcodeArchive {
		return "", domain.Err("download_integrity", "Xcode download must declare a size up to 32 GiB")
	}
	if err := Space(dir, total-offset+(1<<30)); err != nil {
		return "", err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	buf := make([]byte, 1<<20)
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := reader.Read(buf)
		if n > 0 {
			offset += int64(n)
			if offset > total {
				return "", domain.Err("download_integrity", "Xcode download exceeds expected size")
			}
			if _, err := f.Write(buf[:n]); err != nil {
				return "", err
			}
			if time.Since(last) > 5*time.Second {
				if err := progress(fmt.Sprintf("Downloading Xcode %s: %d%%", x.Version, offset*100/total)); err != nil {
					return "", err
				}
				last = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", domain.Err("download_failed", "Xcode download interrupted; partial bytes retained")
		}
	}
	if offset != total {
		return "", domain.Err("download_failed", "Xcode download is incomplete; partial bytes retained")
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := Verify(ctx, partial, x); err != nil {
		if isIntegrityError(err) {
			if quarantineErr := quarantineCache(partial); quarantineErr != nil {
				return "", quarantineErr
			}
			if resumed && source == nil {
				return e.xcodeArchiveOnce(ctx, x, progress)
			}
		}
		return "", err
	}
	if err := os.Rename(partial, final); err != nil {
		return "", err
	}
	markXcodeVerified(final)
	return final, nil
}
