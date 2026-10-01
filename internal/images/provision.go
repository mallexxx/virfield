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

func (e *Engine) provision(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) error {
	if p.Provision != "uitest-27-v1" && p.Provision != "developer-v1" {
		return domain.Err("invalid_profile", "Unknown guest provisioning recipe")
	}
	source := e.Tools.Xcode
	var sourceVersion string
	var err error
	if p.Xcode != nil {
		source, err = e.xcodeSource(ctx, *p.Xcode, progress)
		sourceVersion = "Xcode " + p.Xcode.Version + "\nBuild version " + p.Xcode.Build
	} else {
		sourceVersion, err = e.checkXcode(ctx)
	}
	if err != nil {
		return err
	}
	vm, err := e.boot(ctx, l)
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
	out, versionErr := g.Run(ctx, "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild -version", "")
	reuseXcode := false
	if versionErr == nil && strings.TrimSpace(out) == sourceVersion {
		if err := progress("Checking the existing matching Xcode signature before reuse"); err != nil {
			return err
		}
		_, signatureErr := g.RunReader(ctx, 5*time.Minute, "/usr/bin/codesign --verify --deep --strict /Applications/Xcode.app", nil)
		reuseXcode = signatureErr == nil
	}
	if !reuseXcode {
		if err := progress("Copying local Xcode.app over authenticated SSH; no shared host directory is mounted"); err != nil {
			return err
		}
		transfer, stop := context.WithCancel(ctx)
		defer stop()
		cmd := exec.CommandContext(transfer, "/usr/bin/tar", "-C", filepath.Dir(source), "-cf", "-", filepath.Base(source))
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		cmd.Stderr = nil
		cmd.WaitDelay = 5 * time.Second
		if err := cmd.Start(); err != nil {
			return err
		}
		out, copyErr := g.RunReader(transfer, time.Hour, "/usr/bin/tar -xpf - -C /Users/lume/.virfield-xcode", pipe)
		if copyErr != nil {
			stop()
		}
		waitErr := cmd.Wait()
		if copyErr != nil || waitErr != nil {
			_ = e.provisionLog(l, "transfer", out)
			return domain.Err("xcode_transfer_failed", "Authenticated Xcode transfer failed; inspect image before retry")
		}
		if err := progress("Xcode transferred; installing developer components and completing first launch"); err != nil {
			return err
		}
		move := "set -eu; sudo /bin/rm -rf /Applications/Xcode.app; /bin/mv " + shellQuote(filepath.Join("/Users/lume/.virfield-xcode", filepath.Base(source))) + " /Applications/Xcode.app"
		if _, err := g.Run(ctx, "/bin/bash -c "+shellQuote(move), ""); err != nil {
			return err
		}
	}

	install := `set -eu
sudo /usr/bin/xcode-select -s /Applications/Xcode.app
sudo /usr/bin/xcodebuild -license accept
sudo /usr/bin/xcodebuild -runFirstLaunch
/usr/bin/xcodebuild -version
`
	out, err = g.RunReader(ctx, 30*time.Minute, "/bin/bash -c "+shellQuote(install), nil)
	if err != nil {
		e.provisionLog(l, "xcode", out)
		return domain.Err("xcode_install_failed", "Guest Xcode first launch failed; inspect private stage log")
	}
	if p.Xcode != nil {
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
func (e *Engine) provisionLog(l domain.Lease, stage, out string) error {
	return os.WriteFile(filepath.Join(e.Dir, "images", l.ID, "provision-"+stage+".log"), []byte(out), 0600)
}
func (e *Engine) verifyProvision(ctx context.Context, l domain.Lease, g *guest) error {
	probe := `set -eu
export PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
for tool in brew jq socat peekaboo screenresolution xcbeautify; do command -v "$tool"; done
xcodebuild -version
xcrun --find swift
xcrun swift -e 'import Foundation; print("virfield-swift-ok")'
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
	out, err := g.Run(ctx, "/usr/bin/xcodebuild -version", "")
	if err != nil || strings.TrimSpace(out) != "Xcode "+x.Version+"\nBuild version "+x.Build {
		return domain.Err("xcode_version_mismatch", "Guest Xcode version/build does not match the manifest")
	}
	probe := `set -eu
 test "$(/usr/bin/xcode-select -p)" = /Applications/Xcode.app/Contents/Developer
 /usr/bin/xcodebuild -checkFirstLaunchStatus
 /usr/bin/xcrun --find swift
 /usr/bin/xcrun swift -e 'import Foundation; print("virfield-swift-ok")'
 /usr/bin/codesign --verify --deep --strict -R 'anchor apple' /Applications/Xcode.app
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
