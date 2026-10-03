package images

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

// CachedXcode reports archives verified by the download stage. The marker is
// tied to file metadata; use still rechecks the full checksum.
func (e *Engine) CachedXcode(x domain.XcodeRelease) bool {
	if x.Validate() != nil {
		return false
	}
	path := filepath.Join(e.Dir, "cache", "xcode", x.SHA1+".xip")
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

func xcodeBundleVerifiedMarker(ctx context.Context, app string, x domain.XcodeRelease) bool {
	if !xcodeBundleMatches(ctx, app, x) {
		return false
	}
	st, err := os.Lstat(filepath.Join(app, "Contents", "version.plist"))
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	marker, err := os.ReadFile(filepath.Join(filepath.Dir(app), ".xcode-bundle.verified"))
	return err == nil && string(marker) == fmt.Sprintf("%s:%s:%d:%d\n", x.Version, x.Build, st.Size(), st.ModTime().UnixNano())
}

func markXcodeBundleVerified(app string, x domain.XcodeRelease) {
	st, err := os.Lstat(filepath.Join(app, "Contents", "version.plist"))
	if err != nil || !st.Mode().IsRegular() {
		return
	}
	marker := filepath.Join(filepath.Dir(app), ".xcode-bundle.verified")
	tmp := marker + ".tmp"
	body := fmt.Sprintf("%s:%s:%d:%d\n", x.Version, x.Build, st.Size(), st.ModTime().UnixNano())
	if err := os.WriteFile(tmp, []byte(body), 0600); err == nil {
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

func (e *Engine) xcodeArchiveOnce(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	if err := x.Validate(); err != nil {
		return "", err
	}
	dir := filepath.Join(e.Dir, "cache", "xcode")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, x.SHA1+".xip")
	if st, err := os.Lstat(final); err == nil {
		if !st.Mode().IsRegular() {
			return "", domain.Err("unsafe_cache", "Xcode download cache is not a regular file")
		}
		if err := verifyXcodeArchive(ctx, final, x); err == nil {
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
		cookiePath := e.Tools.AppleCookies
		if e.AuthCookiePath != "" {
			if _, statErr := os.Lstat(e.AuthCookiePath); statErr == nil {
				cookiePath = e.AuthCookiePath
			} else if !os.IsNotExist(statErr) {
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
			if err := verifyXcodeArchive(ctx, partial, x); err == nil {
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
	if err := verifyXcodeArchive(ctx, partial, x); err != nil {
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
func (e *Engine) xcodeSource(ctx context.Context, x domain.XcodeRelease, progress func(string) error) (string, error) {
	// An operator may already own the selected signed bundle. Read metadata
	// without executing it; an unrelated host Xcode never constrains selection.
	if e.Tools.Xcode != "" && xcodeBundleMatches(ctx, e.Tools.Xcode, x) {
		if err := progress("Checking the operator's matching Xcode " + x.Version + " bundle"); err != nil {
			return "", err
		}
		return e.Tools.Xcode, checkOperatorXcodeBundle(ctx, e.Tools.Xcode, x)
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
		// xip needs the login bootstrap and Darwin temporary directory. A
		// LaunchDaemon with the same UID is still in a different session.
		userTempOut, err := exec.CommandContext(ctx, "/usr/bin/getconf", "DARWIN_USER_TEMP_DIR").Output()
		userTemp := strings.TrimSpace(string(userTempOut))
		if err != nil || !filepath.IsAbs(userTemp) {
			return "", domain.Err("image_dependency", "Cannot locate the macOS user temporary directory for Xcode expansion")
		}
		if st, err := os.Stat(userTemp); err != nil || !st.IsDir() {
			return "", domain.Err("image_dependency", "The macOS user temporary directory is unavailable for Xcode expansion")
		}
		cmd := exec.CommandContext(ctx, "/bin/launchctl", "asuser", strconv.Itoa(os.Getuid()), "/usr/bin/xip", "--expand", archive)
		cmd.Dir = temp
		cmd.Env = xipEnvironment(os.Environ(), userTemp)
		// xip verifies the Apple signature before expansion. Output stays private.
		if err := lume.RunPrivate(ctx, cmd, filepath.Join(e.Dir, "cache", "xcode", x.SHA1+"-expand.log")); err != nil {
			return "", domain.Err("xcode_signature_failed", "Apple XIP verification or expansion failed; check archive and available disk space")
		}
		if err := checkXcodeBundle(ctx, filepath.Join(temp, "Xcode.app"), x); err != nil {
			return "", err
		}
		markXcodeBundleVerified(filepath.Join(temp, "Xcode.app"), x)
		if err := os.Rename(temp, dir); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if xcodeBundleVerifiedMarker(ctx, app, x) {
		if err := progress("Using verified cached Xcode " + x.Version + " for VirtioFS transfer"); err != nil {
			return "", err
		}
		return app, nil
	}
	if err := progress("Checking cached Xcode " + x.Version + " before VirtioFS transfer"); err != nil {
		return "", err
	}
	if err := checkXcodeBundle(ctx, app, x); err != nil {
		return "", err
	}
	markXcodeBundleVerified(app, x)
	return app, nil
}

func xipEnvironment(base []string, temp string) []string {
	cleaned := []string{}
	for _, entry := range base {
		if strings.HasPrefix(entry, "TMPDIR=") || strings.HasPrefix(entry, "TMP=") || strings.HasPrefix(entry, "TEMP=") {
			continue
		}
		cleaned = append(cleaned, entry)
	}
	if !strings.HasSuffix(temp, string(os.PathSeparator)) {
		temp += string(os.PathSeparator)
	}
	return append(cleaned, "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp)
}

func checkXcodeBundle(ctx context.Context, app string, x domain.XcodeRelease) error {
	st, err := os.Lstat(app)
	if err != nil || !st.IsDir() {
		return domain.Err("image_dependency", "Expanded Xcode.app is absent or invalid")
	}
	if !xcodeBundleMatches(ctx, app, x) {
		return domain.Err("xcode_version_mismatch", "Xcode bundle version/build differs from the selected release")
	}
	return nil
}

func checkOperatorXcodeBundle(ctx context.Context, app string, x domain.XcodeRelease) error {
	if err := checkXcodeBundle(ctx, app, x); err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(probe, "/usr/bin/codesign", "--verify", "--strict", "-R", appleCodeRequirement, app)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Xcode host signature check failed: %v\n", err)
		if errors.Is(probe.Err(), context.DeadlineExceeded) {
			return domain.Err("image_dependency", "Timed out checking the operator Xcode signature on the host")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return domain.Err("xcode_signature_failed", "Operator Xcode must have an intact Apple signature")
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
