package qualify

import (
	"context"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// AppleCodeRequirement accepts only Apple-signed code.
const AppleCodeRequirement = "=anchor apple"

// XcodeBundle checks bundle metadata and Apple's signature before any
// guest-side execution of Xcode.
func XcodeBundle(ctx context.Context, g Guest, x domain.XcodeRelease) error {
	out, err := g.Run(ctx, "/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' /Applications/Xcode.app/Contents/version.plist && /usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' /Applications/Xcode.app/Contents/version.plist", "")
	if err != nil || strings.TrimSpace(out) != x.Version+"\n"+x.Build {
		return domain.Err("xcode_version_mismatch", "Guest Xcode bundle metadata differs from the selected release")
	}
	if _, err := g.RunReader(ctx, 5*time.Minute, "/usr/bin/codesign --verify --deep --strict -R "+Quote(AppleCodeRequirement)+" /Applications/Xcode.app", nil); err != nil {
		return domain.Err("xcode_signature_failed", "Guest Xcode must have an intact Apple signature before execution")
	}
	return nil
}

// Xcode verifies the bundle, the toolchain and the debugging prerequisites.
// It returns the probe output (for the caller's private log) once the probe ran.
func Xcode(ctx context.Context, g Guest, user string, x domain.XcodeRelease) (string, error) {
	if !domain.ValidName(user) {
		return "", domain.Err("xcode_verification_failed", "Guest user name is invalid")
	}
	if err := XcodeBundle(ctx, g, x); err != nil {
		return "", err
	}
	out, err := g.Run(ctx, "/usr/bin/xcodebuild -version", "")
	if err != nil || strings.TrimSpace(out) != "Xcode "+x.Version+"\nBuild version "+x.Build {
		return "", domain.Err("xcode_version_mismatch", "Guest Xcode version/build does not match the manifest")
	}
	probe := `set -eu
 test "$(/usr/bin/xcode-select -p)" = /Applications/Xcode.app/Contents/Developer
 /usr/bin/xcodebuild -checkFirstLaunchStatus
 /usr/bin/xcrun --find swiftc
 sudo -n /usr/sbin/DevToolsSecurity -status 2>&1 | grep -q 'currently enabled'
 id -Gn ` + user + ` | tr ' ' '\n' | grep -qx _developer
 sudo -n /sbin/nfsd status
 grep -Eq '/(Cryptexes/OS/)?System/Library/dyld' /etc/exports
 probe_dir="$(/usr/bin/mktemp -d /tmp/virfield-swift.XXXXXX)"
 trap '/bin/rm -rf "$probe_dir"' EXIT
 printf '%s\n' 'import Foundation' 'print("virfield-swift-ok")' > "$probe_dir/probe.swift"
 /usr/bin/xcrun swiftc "$probe_dir/probe.swift" -o "$probe_dir/probe"
 test "$("$probe_dir/probe")" = virfield-swift-ok
 `
	out, err = g.RunReader(ctx, 5*time.Minute, "/bin/bash -c "+Quote(probe), nil)
	if err != nil {
		return out, domain.Err("xcode_verification_failed", "Guest Xcode signature, first launch or Swift compilation check failed")
	}
	return out, nil
}
