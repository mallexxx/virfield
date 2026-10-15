set -eu
[ "$(/usr/bin/sw_vers -productVersion)" = 12.7.6 ] || { echo 'legacy-ios-v1 requires macOS 12.7.6' >&2; exit 1; }
[ "$(/usr/bin/xcodebuild -version)" = "$(printf 'Xcode 13.4.1\nBuild version 13F100')" ] || { echo 'legacy-ios-v1 requires Xcode 13.4.1/13F100' >&2; exit 1; }
case "$(/usr/bin/uname -m)" in arm64|x86_64) ;; *) echo 'unsupported native guest architecture' >&2; exit 1;; esac
[ "$(/usr/sbin/sysctl -in sysctl.proc_translated 2>/dev/null || true)" != 1 ] || { echo 'profile must run in the native guest architecture' >&2; exit 1; }
