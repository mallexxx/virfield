package qualify

import (
	"context"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// InstalledXcode reads the guest Xcode release for profiles that adopt an
// upstream Xcode instead of pinning one; the signature is checked separately.
func InstalledXcode(ctx context.Context, g Guest) (domain.XcodeRelease, error) {
	out, err := g.Run(ctx, "/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' /Applications/Xcode.app/Contents/version.plist && /usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' /Applications/Xcode.app/Contents/version.plist", "")
	fields := strings.Fields(out)
	if err != nil || len(fields) != 2 || !domain.ValidVersion(fields[0]) || !domain.ValidName(fields[1]) {
		return domain.XcodeRelease{}, domain.Err("xcode_version_mismatch", "Guest Xcode bundle metadata is unreadable")
	}
	return domain.XcodeRelease{Version: fields[0], Build: fields[1]}, nil
}

// Provision verifies the uitest-27-v1 tools, Gatekeeper, AMFI and TCC policy
// after reboot. The probe output goes to log once the probe has run.
func Provision(ctx context.Context, g Guest, user string, log func(string) error) error {
	failure := domain.Err("provision_verification_failed", "Guest tool, TCC, AMFI or Gatekeeper verification failed after reboot")
	if !domain.ValidName(user) {
		return failure
	}
	probe := `set -eu
export PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
for tool in brew jq socat peekaboo screenresolution xcbeautify; do command -v "$tool"; done
xcodebuild -version
xcrun --find swiftc
sudo -n /usr/sbin/DevToolsSecurity -status 2>&1 | grep -q 'currently enabled'
id -Gn ` + user + ` | tr ' ' '\n' | grep -qx _developer
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
	out, err := g.Run(ctx, "/bin/bash -c "+Quote(probe), "")
	if logErr := log(out); logErr != nil {
		return logErr
	}
	if err != nil || !strings.Contains(out, "assessments disabled") {
		return failure
	}
	return nil
}
