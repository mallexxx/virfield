package images

import (
	"bufio"
	"context"
	"crypto/sha1" // Catalog legacy checksum; Apple's XIP signature is also mandatory.
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/lume"
)

const maxXcodeArchive = 32 << 30

// The leading = selects inline requirement syntax; otherwise codesign opens a file.
const appleCodeRequirement = "=anchor apple"

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
		if host != "download.developer.apple.com" && !(fields[1] == "TRUE" && (host == "developer.apple.com" || host == "apple.com")) {
			continue
		}
		expiry, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || (expiry != 0 && expiry < time.Now().Unix()) {
			continue
		}
		if fields[2] != "/" && !strings.HasPrefix("/Developer_Tools/", fields[2]) {
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
func verifyXcodeArchive(ctx context.Context, path string, x domain.XcodeRelease) error {
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
func (e *Engine) xcodeArchive(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	if err := x.Validate(); err != nil {
		return "", err
	}
	dir := filepath.Join(e.Dir, "cache", "xcode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, x.SHA1+".xip")
	if _, err := os.Lstat(final); err == nil {
		return final, verifyXcodeArchive(ctx, final, x)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	partial := final + ".partial"
	if st, err := os.Lstat(partial); err == nil && !st.Mode().IsRegular() {
		return "", domain.Err("unsafe_cache", "Xcode download cache is not a regular file")
	}
	if err := verifyXcodeArchive(ctx, partial, x); err == nil {
		if err := os.Rename(partial, final); err != nil {
			return "", err
		}
		return final, nil
	}
	var source *os.File
	if e.Tools.XcodeArchives != "" {
		path := filepath.Join(e.Tools.XcodeArchives, "Xcode_"+x.Version+".xip")
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
		cookies, err := appleCookies(e.Tools.AppleCookies)
		if err != nil {
			return "", err
		}
		if cookies != "" {
			req.Header.Set("Cookie", cookies)
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		// Never forward developer cookies through a redirect, including a login page.
		client := *e.HTTP
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, err := client.Do(req)
		if err != nil {
			return "", domain.Err("download_failed", "Xcode download interrupted; partial bytes retained")
		}
		defer res.Body.Close()
		if res.StatusCode == 401 || res.StatusCode == 403 || (res.StatusCode >= 300 && res.StatusCode < 400) || strings.Contains(res.Header.Get("Content-Type"), "text/html") {
			return "", domain.Err("apple_auth_required", "Apple requires sign-in to download Xcode "+x.Version+". Configure image_tools.apple_cookies (private Netscape file), or download Xcode_"+x.Version+".xip from Apple Developer into image_tools.xcode_archives; retry the download stage afterward")
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
	if err := verifyXcodeArchive(ctx, partial, x); err != nil {
		return "", err
	}
	if err := os.Rename(partial, final); err != nil {
		return "", err
	}
	return final, nil
}
func (e *Engine) xcodeSource(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	// An operator may already own the selected signed bundle. Read metadata
	// without executing it; an unrelated host Xcode never constrains selection.
	if e.Tools.Xcode != "" && xcodeBundleMatches(ctx, e.Tools.Xcode, x) {
		if err := progress("Verifying the operator's matching Xcode " + x.Version + " bundle"); err != nil {
			return "", err
		}
		return e.Tools.Xcode, checkXcodeBundle(ctx, e.Tools.Xcode, x)
	}
	archive, err := e.xcodeArchive(ctx, x, progress)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(e.Dir, "cache", "xcode", x.SHA1)
	app := filepath.Join(dir, "Xcode.app")
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		if err := Space(e.Dir, 40<<30); err != nil {
			return "", err
		}
		if err := progress("Verifying Apple's XIP signature and expanding Xcode " + x.Version); err != nil {
			return "", err
		}
		temp, err := os.MkdirTemp(filepath.Dir(dir), "expand-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(temp)
		cmd := exec.CommandContext(ctx, "/usr/bin/xip", "--expand", archive)
		cmd.Dir = temp
		// xip verifies the Apple signature before expansion. Output stays private.
		if err := lume.RunPrivate(ctx, cmd, filepath.Join(e.Dir, "cache", "xcode", x.SHA1+"-expand.log")); err != nil {
			return "", domain.Err("xcode_signature_failed", "Apple XIP verification or expansion failed; check archive and available disk space")
		}
		if err := checkXcodeBundle(ctx, filepath.Join(temp, "Xcode.app"), x); err != nil {
			return "", err
		}
		if err := os.Rename(temp, dir); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return app, checkXcodeBundle(ctx, app, x)
}
func checkXcodeBundle(ctx context.Context, app string, x domain.XcodeRelease) error {
	st, err := os.Lstat(app)
	if err != nil || !st.IsDir() {
		return domain.Err("image_dependency", "Expanded Xcode.app is absent or invalid")
	}
	if !xcodeBundleMatches(ctx, app, x) {
		return domain.Err("xcode_version_mismatch", "Xcode bundle version/build differs from the selected release")
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := exec.CommandContext(probe, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R", appleCodeRequirement, app).Run(); err != nil {
		return domain.Err("xcode_signature_failed", "Xcode must have an intact Apple signature")
	}
	return nil
}

// xcodeBundleMatches performs bounded metadata reads, never starts xcodebuild.
func xcodeBundleMatches(ctx context.Context, app string, x domain.XcodeRelease) bool {
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for key, want := range map[string]string{"CFBundleShortVersionString": x.Version, "ProductBuildVersion": x.Build} {
		out, err := exec.CommandContext(probe, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, filepath.Join(app, "Contents/version.plist")).Output()
		if err != nil || strings.TrimSpace(string(out)) != want {
			return false
		}
	}
	return true
}
