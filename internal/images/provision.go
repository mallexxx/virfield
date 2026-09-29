package images

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
)

//go:embed provision27.sh
var provisionScript string

func (e *Engine) provision(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) error {
	if p.Provision != "uitest-27-v1" || !filepath.IsAbs(e.Tools.Xcode) || filepath.Base(e.Tools.Xcode) != "Xcode.app" {
		return domain.Err("invalid_profile", "uitest-27-v1 requires an absolute local Xcode.app source")
	}
	if _, err := os.Stat(filepath.Join(e.Tools.Xcode, "Contents/Developer/usr/bin/xcodebuild")); err != nil {
		return domain.Err("image_dependency", "Configured Xcode.app is incomplete")
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
	if err := e.secure(ctx, l, vm.IP, g); err != nil {
		return err
	}
	c, err := guestssh.LoadCredentials((&guestssh.Manager{Dir: e.Dir}).CredentialPath(l))
	if err != nil {
		return err
	}
	// This sudo policy is intentionally confined to the disposable UI-test guest.
	bootstrap := `IFS= read -r password
printf '%s\n' "$password" | sudo -S -p '' /bin/sh -c 'printf "lume ALL=(ALL) NOPASSWD:ALL\n" > /etc/sudoers.d/virfield-worker; chmod 440 /etc/sudoers.d/virfield-worker; /usr/sbin/visudo -cf /etc/sudoers.d/virfield-worker'
mkdir -p /Users/lume/.virfield-xcode
`
	if _, err := g.Run(ctx, "/bin/bash -c "+shellQuote(bootstrap), c.Password+"\n"); err != nil {
		return err
	}
	if err := progress("Copying local Xcode.app over authenticated SSH; no shared host directory is mounted"); err != nil {
		return err
	}
	transfer, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(transfer, "/usr/bin/tar", "-C", filepath.Dir(e.Tools.Xcode), "-cf", "-", "Xcode.app")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	_, copyErr := g.RunReader(transfer, time.Hour, "/usr/bin/tar -xpf - -C /Users/lume/.virfield-xcode", pipe)
	if copyErr != nil {
		stop()
	}
	waitErr := cmd.Wait()
	if copyErr != nil || waitErr != nil {
		return domain.Err("xcode_transfer_failed", "Authenticated Xcode transfer failed; inspect image before retry")
	}
	install := `set -eu
sudo /bin/rm -rf /Applications/Xcode.app
/bin/mv /Users/lume/.virfield-xcode/Xcode.app /Applications/Xcode.app
sudo /usr/bin/xcode-select -s /Applications/Xcode.app
sudo /usr/bin/xcodebuild -license accept
sudo /usr/bin/xcodebuild -runFirstLaunch
/usr/bin/xcodebuild -version
`
	out, err := g.RunReader(ctx, 30*time.Minute, "/bin/bash -c "+shellQuote(install), nil)
	if err != nil {
		e.provisionLog(l, "xcode", out)
		return domain.Err("xcode_install_failed", "Guest Xcode first launch failed; inspect private stage log")
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
