package images

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
)

//go:embed provision27.sh
var provisionScript string

const guestSharedFolder = "/Volumes/My Shared Files"

func (e *Engine) checkXcode(ctx context.Context) (string, error) {
	name := filepath.Base(e.Tools.Xcode)
	if !filepath.IsAbs(e.Tools.Xcode) || (name != "Xcode.app" && name != "Xcode-beta.app") {
		return "", domain.Err("invalid_profile", "uitest-27-v1 requires an absolute local Xcode.app or Xcode-beta.app source")
	}
	binary := filepath.Join(e.Tools.Xcode, "Contents/Developer/usr/bin/xcodebuild")
	if _, err := os.Stat(binary); err != nil {
		return "", domain.Err("image_dependency", "Configured Xcode.app is incomplete")
	}
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probe, binary, "-version").Output()
	if err != nil || !compatibleXcode(string(out)) {
		return "", domain.Err("image_dependency", "macOS 27 UI-test tools require Xcode 27 or newer; choose a compatible image_tools.xcode source")
	}
	return strings.TrimSpace(string(out)), nil
}

func compatibleXcode(version string) bool {
	fields := strings.Fields(version)
	if len(fields) < 2 || fields[0] != "Xcode" {
		return false
	}
	major, err := strconv.Atoi(strings.Split(fields[1], ".")[0])
	return err == nil && major >= 27
}
func guestSupportsVirtioFS(macos string) bool {
	return domain.ValidVersion(macos) && domain.CompareVersions(macos, "13") >= 0
}

func usesSharedXcodeBundle(p domain.ImageProfile) bool {
	return p.Xcode == nil && guestSupportsVirtioFS(p.MacOS)
}

func (e *Engine) provision(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) error {
	if p.Provision == "security-v1" {
		return e.provisionSecurity(ctx, l)
	}
	if p.Provision != "uitest-27-v1" && p.Provision != "developer-v1" {
		return domain.Err("invalid_profile", "Unknown guest provisioning recipe")
	}
	// Operator-provided app bundles use VirtioFS. Catalog XIPs stay compact and
	// are expanded in the guest, where xip has the required login audit session.
	sharedTransfer := usesSharedXcodeBundle(p)
	source := e.Tools.Xcode
	archive := ""
	var sourceVersion string
	var err error
	if p.Xcode != nil {
		archive, err = e.xcodeArchive(ctx, *p.Xcode, progress)
		sourceVersion = "Xcode " + p.Xcode.Version + "\nBuild version " + p.Xcode.Build
	} else {
		sourceVersion, err = e.checkXcode(ctx)
	}
	if err != nil {
		return err
	}
	transferRoot := filepath.Join(e.Dir, "images", l.ID, "virtiofs-transfer")
	detachedTransferRoot := ""
	var vm domain.VM
	if sharedTransfer {
		if err := progress("Cleaning leftover VirtioFS transfer folder"); err != nil {
			return err
		}
		detachedTransferRoot, err = detachTransferRoot(transferRoot)
		if err != nil {
			return domain.Err("xcode_transfer_cleanup_failed", "Stale VirtioFS transfer folder could not be detached before retry")
		}
		if detachedTransferRoot != "" {
			go func() { _ = os.RemoveAll(detachedTransferRoot) }()
		}
		defer os.RemoveAll(transferRoot)
		if !filepath.IsAbs(source) || filepath.Base(source) == "." || filepath.Base(source) == string(filepath.Separator) {
			return domain.Err("invalid_profile", "Xcode source must be an absolute app bundle")
		}
		if err := progress("Starting image VM with the VirtioFS Xcode source folder"); err != nil {
			return err
		}
		vm, err = e.bootWithSharedDirectory(ctx, l, filepath.Dir(source))
	} else {
		if err := progress("Starting image VM for SSH XIP transfer"); err != nil {
			return err
		}
		vm, err = e.boot(ctx, l)
	}
	if err != nil {
		return err
	}
	g, err := e.connect(ctx, l, vm.IP, true)
	if err != nil {
		return err
	}
	defer g.Close()
	if err := e.prepareDesktop(ctx, l, g); err != nil {
		return err
	}
	if err := e.secure(ctx, l, vm.IP, g); err != nil {
		return err
	}
	c, err := guestssh.LoadCredentials((&guestssh.Manager{Dir: e.Dir}).CredentialPath(l))
	if err != nil {
		return err
	}
	// This sudo policy is intentionally confined to the disposable UI-test guest.
	bootstrap := `set -eu
IFS= read -r password
printf '%s\n' "$password" | sudo -S -p '' /bin/sh -c 'printf "lume ALL=(ALL) NOPASSWD:ALL\n" > /etc/sudoers.d/virfield-worker; chmod 440 /etc/sudoers.d/virfield-worker; /usr/sbin/visudo -cf /etc/sudoers.d/virfield-worker'
mkdir -p /Users/lume/.virfield-xcode
`
	if _, err := g.Run(ctx, "/bin/bash -c "+shellQuote(bootstrap), c.Password+"\n"); err != nil {
		return err
	}
	var out string
	reuseXcode := false
	if p.Xcode != nil {
		// Read metadata and verify Apple's signature before ever executing an
		// existing bundle. Failed checks cause replacement from the verified source.
		reuseXcode = verifyGuestXcodeBundle(ctx, g, *p.Xcode) == nil
	} else {
		if err := progress("Checking the existing Xcode signature before running it"); err != nil {
			return err
		}
		_, signatureErr := g.RunReader(ctx, 5*time.Minute, "/usr/bin/codesign --verify --deep --strict /Applications/Xcode.app", nil)
		if signatureErr == nil {
			var versionErr error
			out, versionErr = g.Run(ctx, "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild -version", "")
			reuseXcode = versionErr == nil && strings.TrimSpace(out) == sourceVersion
		}
	}
	if !reuseXcode {
		if sharedTransfer {
			if err := progress("Installing Xcode.app through the VirtioFS shared folder"); err != nil {
				return err
			}
			out, copyErr := g.RunReader(ctx, time.Hour, "/bin/bash -c "+shellQuote(sharedXcodeInstallScript(filepath.Base(source))), nil)
			if copyErr != nil {
				_ = e.provisionLog(l, "transfer", out)
				return domain.Err("xcode_transfer_failed", "VirtioFS Xcode transfer failed; inspect image before retry")
			}
			if detachedTransferRoot != "" {
				_ = os.RemoveAll(detachedTransferRoot)
			}
			_ = os.RemoveAll(transferRoot)
		} else if err := e.installXcodeArchive(ctx, l, g, archive, *p.Xcode, progress); err != nil {
			return err
		}
		if err := progress("Xcode transferred; installing developer components and completing first launch"); err != nil {
			return err
		}
	}

	if p.Xcode != nil && !reuseXcode {
		if err := verifyGuestXcodeBundle(ctx, g, *p.Xcode); err != nil {
			return err
		}
	}
	install := `set -eu
sudo /usr/bin/xcode-select -s /Applications/Xcode.app
sudo /usr/bin/xcodebuild -license accept
sudo /usr/bin/xcodebuild -runFirstLaunch
sudo -n /usr/sbin/DevToolsSecurity -enable
sudo -n /usr/sbin/dseditgroup -o edit -a lume -t user _developer
dyld_cache=
for candidate in /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld /System/Library/dyld; do
  if test -d "$candidate"; then
    dyld_cache="$candidate"
    break
  fi
done
test -n "$dyld_cache"
dyld_export="\"$dyld_cache\" -ro -mapall=$(id -u):$(id -g) -network 192.168.64.0 -mask 255.255.255.0"
sudo touch /etc/exports
sudo grep -Fqx "$dyld_export" /etc/exports || printf '%s\n' "$dyld_export" | sudo tee -a /etc/exports >/dev/null
sudo /sbin/nfsd enable
sudo /sbin/nfsd restart
/usr/bin/xcodebuild -version
`
	out, err = g.RunReader(ctx, 30*time.Minute, "/bin/bash -c "+shellQuote(install), nil)
	if err != nil {
		e.provisionLog(l, "xcode", out)
		return domain.Err("xcode_install_failed", "Guest Xcode first launch failed; inspect private stage log")
	}
	if p.Xcode != nil {
		if p.Security == "automation" {
			automation := `set -eu
sudo -n /usr/bin/automationmodetool enable-automationmode-without-authentication
`
			if out, err := g.Run(ctx, "/bin/bash -c "+shellQuote(automation), c.Password+"\n"); err != nil {
				_ = e.provisionLog(l, "xcode-automation", out)
				return domain.Err("tool_provision_failed", "Guest Xcode automation mode could not be enabled")
			}
			if err := e.securityPolicy(ctx, l, g, "apply"); err != nil {
				return err
			}
		}
		if err := e.verifyXcode(ctx, l, g, *p.Xcode); err != nil {
			return err
		}
		return e.stop(ctx, l)
	}
	if err := progress("Xcode installed; provisioning guest tools, Gatekeeper, AMFI and TCC"); err != nil {
		return err
	}
	out, err = g.RunReader(ctx, time.Hour, "/bin/bash -c "+shellQuote("IFS= read -r VF_PASSWORD; eval \"$(cat)\""), strings.NewReader(c.Password+"\n"+provisionScript))
	if logErr := e.provisionLog(l, "tools", out); logErr != nil {
		return logErr
	}
	if err != nil {
		return domain.Err("tool_provision_failed", "Guest tool provisioning failed; inspect private stage log")
	}
	return e.stop(ctx, l)
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func detachTransferRoot(path string) (string, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	detached := path + ".cleanup-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = os.RemoveAll(detached)
	if err := os.Rename(path, detached); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return detached, nil
}
func sharedXcodeInstallScript(base string) string {
	source := filepath.Join(guestSharedFolder, base)
	staged := filepath.Join("/Users/lume/.virfield-xcode", base)
	return `set -eu
src=` + shellQuote(source) + `
staged=` + shellQuote(staged) + `
test -d "$src"
/bin/rm -rf /Users/lume/.virfield-xcode
/bin/mkdir -p /Users/lume/.virfield-xcode
/usr/bin/ditto "$src" "$staged"
sudo /bin/rm -rf /Applications/Xcode.app
sudo /bin/mv "$staged" /Applications/Xcode.app
test -x /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
`
}
func (e *Engine) provisionLog(l domain.Lease, stage, out string) error {
	return os.WriteFile(filepath.Join(e.Dir, "images", l.ID, "provision-"+stage+".log"), []byte(out), 0600)
}
func (e *Engine) verifyProvision(ctx context.Context, l domain.Lease, g *guest) error {
	probe := `set -eu
export PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
for tool in brew jq socat peekaboo screenresolution xcbeautify; do command -v "$tool"; done
xcodebuild -version
xcrun --find swiftc
sudo -n /usr/sbin/DevToolsSecurity -status 2>&1 | grep -q 'currently enabled'
id -Gn lume | tr ' ' '\n' | grep -qx _developer
sudo -n /sbin/nfsd status
probe_dir="$(/usr/bin/mktemp -d /tmp/virfield-swift.XXXXXX)"
trap '/bin/rm -rf "$probe_dir"' EXIT
printf '%s\n' 'import Foundation' 'print("virfield-swift-ok")' > "$probe_dir/probe.swift"
xcrun swiftc "$probe_dir/probe.swift" -o "$probe_dir/probe"
test "$("$probe_dir/probe")" = virfield-swift-ok
sudo -n true
assessment="$(spctl --status 2>&1 || true)"
printf '%s\n' "$assessment"
test "$assessment" = 'assessments disabled'
sysctl -n kern.bootargs | grep -q amfi_get_out_of_my_way=1
[ "$(sudo sqlite3 '/Library/Application Support/com.apple.TCC/TCC.db' "select count(*) from access where client='com.apple.Terminal' and auth_value=2 and service in ('kTCCServiceAccessibility','kTCCServiceScreenCapture','kTCCServiceSystemPolicyAllFiles');")" = 3 ]
/usr/bin/osascript -e 'tell application "System Events" to get name of first process'
/opt/homebrew/bin/peekaboo permissions status --json --no-remote | jq -e '.success == true and .data.source == "local" and ([.data.permissions[] | select(.isRequired == true)] | length >= 2 and all(.isGranted == true))'
`
	out, err := g.Run(ctx, "/bin/bash -c "+shellQuote(probe), "")
	if logErr := e.provisionLog(l, "verify", out); logErr != nil {
		return logErr
	}
	if err != nil || !strings.Contains(out, "assessments disabled") {
		return domain.Err("provision_verification_failed", "Guest tool, TCC, AMFI or Gatekeeper verification failed after reboot")
	}
	b, _ := json.MarshalIndent(map[string]any{"recipe": "uitest-27-v1", "verified_at": time.Now().UTC(), "probes": out}, "", "  ")
	return os.WriteFile(filepath.Join(e.Dir, "images", l.ID, "provision-verification.json"), b, 0600)
}

func (e *Engine) verifyXcode(ctx context.Context, l domain.Lease, g *guest, x domain.XcodeRelease) error {
	if err := verifyGuestXcodeBundle(ctx, g, x); err != nil {
		return err
	}
	out, err := g.Run(ctx, "/usr/bin/xcodebuild -version", "")
	if err != nil || strings.TrimSpace(out) != "Xcode "+x.Version+"\nBuild version "+x.Build {
		return domain.Err("xcode_version_mismatch", "Guest Xcode version/build does not match the manifest")
	}
	probe := `set -eu
 test "$(/usr/bin/xcode-select -p)" = /Applications/Xcode.app/Contents/Developer
 /usr/bin/xcodebuild -checkFirstLaunchStatus
 /usr/bin/xcrun --find swiftc
 sudo -n /usr/sbin/DevToolsSecurity -status 2>&1 | grep -q 'currently enabled'
 id -Gn lume | tr ' ' '\n' | grep -qx _developer
 sudo -n /sbin/nfsd status
 grep -Eq '/(Cryptexes/OS/)?System/Library/dyld' /etc/exports
 probe_dir="$(/usr/bin/mktemp -d /tmp/virfield-swift.XXXXXX)"
 trap '/bin/rm -rf "$probe_dir"' EXIT
 printf '%s\n' 'import Foundation' 'print("virfield-swift-ok")' > "$probe_dir/probe.swift"
 /usr/bin/xcrun swiftc "$probe_dir/probe.swift" -o "$probe_dir/probe"
 test "$("$probe_dir/probe")" = virfield-swift-ok
 `
	out, err = g.RunReader(ctx, 5*time.Minute, "/bin/bash -c "+shellQuote(probe), nil)
	if logErr := e.provisionLog(l, "xcode-verify", out); logErr != nil {
		return logErr
	}
	if err != nil {
		return domain.Err("xcode_verification_failed", "Guest Xcode signature, first launch or Swift compilation check failed")
	}
	return nil
}

func verifyGuestXcodeBundle(ctx context.Context, g *guest, x domain.XcodeRelease) error {
	out, err := g.Run(ctx, "/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' /Applications/Xcode.app/Contents/version.plist && /usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' /Applications/Xcode.app/Contents/version.plist", "")
	if err != nil || strings.TrimSpace(out) != x.Version+"\n"+x.Build {
		return domain.Err("xcode_version_mismatch", "Guest Xcode bundle metadata differs from the selected release")
	}
	if _, err := g.RunReader(ctx, 5*time.Minute, "/usr/bin/codesign --verify --deep --strict -R "+shellQuote(appleCodeRequirement)+" /Applications/Xcode.app", nil); err != nil {
		return domain.Err("xcode_signature_failed", "Guest Xcode must have an intact Apple signature before execution")
	}
	return nil
}
