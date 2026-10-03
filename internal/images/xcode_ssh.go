package images

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

const guestXcodeArchive = "/Users/lume/.virfield-xcode/archive.xip"

// xcodeProgressReader reports transfer progress without buffering the archive.
type xcodeProgressReader struct {
	reader   io.Reader
	total    int64
	read     int64
	last     time.Time
	progress func(string) error
	err      error
}

func (r *xcodeProgressReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.reader.Read(p)
	r.read += int64(n)
	if n > 0 && time.Since(r.last) >= 5*time.Second {
		message := ""
		if r.total == 0 {
			message = fmt.Sprintf("Streaming Xcode.app to guest: %.1f GiB", float64(r.read)/(1<<30))
		} else {
			message = fmt.Sprintf("Transferring Xcode XIP to guest: %d%%", r.read*100/r.total)
		}
		if r.err = r.progress(message); r.err != nil {
			return 0, r.err
		}
		r.last = time.Now()
	}
	return n, err
}

func (e *Engine) installXcodeArchive(ctx context.Context, l domain.Lease, g *guest, archive string, x domain.XcodeRelease, progress func(string) error) error {
	if err := progress("Transferring verified Xcode XIP to guest over SSH"); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > maxXcodeArchive {
		return domain.Err("unsafe_cache", "Xcode archive is not a bounded regular file")
	}
	reader := &xcodeProgressReader{reader: f, total: st.Size(), progress: progress}
	out, transferErr := g.RunReader(ctx, 2*time.Hour, "/bin/bash -c "+shellQuote(sshXcodeReceiveScript(x.SHA1)), reader)
	if transferErr != nil {
		_ = e.provisionLog(l, "transfer", out)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if reader.err != nil {
			return reader.err
		}
		return domain.Err("xcode_transfer_failed", "SSH XIP transfer or guest checksum failed; inspect private transfer log")
	}
	if reader.read != st.Size() {
		return domain.Err("xcode_transfer_failed", "SSH XIP transfer did not read the complete archive")
	}
	if err := progress("Xcode XIP transferred; verifying Apple signature and expanding in guest"); err != nil {
		return err
	}
	out, err = g.RunReader(ctx, 2*time.Hour, "/bin/bash -c "+shellQuote(sshXcodeExpandScript(x.Version, x.Build)), nil)
	if err != nil {
		_ = e.provisionLog(l, "expand", out)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if xipOutOfSpace(out) {
			if err := progress("Guest XIP expansion needs more disk; streaming verified Xcode.app over SSH"); err != nil {
				return err
			}
			app, err := e.xcodeSource(ctx, x, progress)
			if err != nil {
				return err
			}
			return e.installXcodeBundleSSH(ctx, l, g, app, x, progress)
		}
		return domain.Err("xcode_install_failed", "Guest XIP verification or expansion failed; inspect private expand log")
	}
	return nil
}

func xipOutOfSpace(out string) bool {
	return strings.Contains(strings.ToLower(out), "free space") && strings.Contains(out, "xip:")
}

func (e *Engine) installXcodeBundleSSH(ctx context.Context, l domain.Lease, g *guest, app string, x domain.XcodeRelease, progress func(string) error) error {
	log, err := os.OpenFile(filepath.Join(e.Dir, "images", l.ID, "provision-host-tar.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, "/usr/bin/tar", "--no-mac-metadata", "-C", filepath.Dir(app), "-cpf", "-", filepath.Base(app))
	cmd.Env = append(os.Environ(), "COPYFILE_DISABLE=1")
	cmd.Stderr = log
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := &xcodeProgressReader{reader: pipe, progress: progress}
	out, transferErr := g.RunReader(ctx, 2*time.Hour, "/bin/bash -c "+shellQuote(sshXcodeBundleInstallScript(x.Version, x.Build)), reader)
	if transferErr != nil {
		_ = cmd.Process.Kill()
	}
	hostErr := cmd.Wait()
	if transferErr != nil || hostErr != nil {
		_ = e.provisionLog(l, "bundle-transfer", out)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if reader.err != nil {
			return reader.err
		}
		return domain.Err("xcode_transfer_failed", "SSH Xcode bundle transfer failed; inspect private transfer logs")
	}
	return nil
}

func sshXcodeReceiveScript(digest string) string {
	partial := guestXcodeArchive + ".partial"
	return `set -eu
umask 077
/bin/mkdir -p ` + shellQuote(filepath.Dir(guestXcodeArchive)) + `
partial=` + shellQuote(partial) + `
archive=` + shellQuote(guestXcodeArchive) + `
/bin/rm -f "$partial" "$archive"
/bin/rm -rf /Users/lume/.virfield-xcode/expanded
trap '/bin/rm -f "$partial"' EXIT HUP INT TERM
/bin/cat > "$partial"
actual="$(/usr/bin/shasum -a 1 "$partial")"
test "${actual%% *}" = ` + shellQuote(digest) + `
/bin/mv "$partial" "$archive"
trap - EXIT HUP INT TERM
`
}

func sshXcodeExpandScript(version, build string) string {
	return `set -eu
archive=` + shellQuote(guestXcodeArchive) + `
work=/Users/lume/.virfield-xcode/expanded
/bin/rm -rf "$work"
/bin/mkdir -p "$work"
trap '/bin/rm -rf "$work"; /bin/rm -f "$archive"' EXIT HUP INT TERM
cd "$work"
/usr/bin/xip --expand "$archive"
app="$work/Xcode.app"
test -x "$app/Contents/Developer/usr/bin/xcodebuild"
test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/version.plist")" = ` + shellQuote(version) + `
test "$(/usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' "$app/Contents/version.plist")" = ` + shellQuote(build) + `
/usr/bin/codesign --verify --deep --strict -R ` + shellQuote(appleCodeRequirement) + ` "$app"
sudo /bin/rm -rf /Applications/Xcode.app
sudo /bin/mv "$app" /Applications/Xcode.app
test -x /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
`
}

func sshXcodeBundleInstallScript(version, build string) string {
	return `set -eu
work=/Users/lume/.virfield-xcode/expanded
/bin/rm -rf "$work"
/bin/mkdir -p "$work"
trap '/bin/rm -rf "$work"' EXIT HUP INT TERM
COPYFILE_DISABLE=1 /usr/bin/tar -xpf - -C "$work"
app="$work/Xcode.app"
test -x "$app/Contents/Developer/usr/bin/xcodebuild"
test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/version.plist")" = ` + shellQuote(version) + `
test "$(/usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' "$app/Contents/version.plist")" = ` + shellQuote(build) + `
/usr/bin/codesign --verify --deep --strict -R ` + shellQuote(appleCodeRequirement) + ` "$app"
sudo /bin/rm -rf /Applications/Xcode.app
sudo /bin/mv "$app" /Applications/Xcode.app
test -x /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
`
}
