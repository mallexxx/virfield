#!/bin/bash
# Verify the uploaded archive, then Apple's signature and the exact release
# before replacing /Applications/Xcode.app.
set -euo pipefail
archive="$HOME/.virfield-xcode/archive.xip"
work="$HOME/.virfield-xcode/expanded"
trap 'rm -rf "$work" "$archive"' EXIT
actual="$(shasum -a 1 "$archive")"
[[ "${actual%% *}" == "$VF_XIP_SHA1" ]] || { echo 'XIP digest mismatch'; exit 65; }
rm -rf "$work"
mkdir -p "$work"
cd "$work"
xip --expand "$archive"
app="$work/Xcode.app"
test -x "$app/Contents/Developer/usr/bin/xcodebuild"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/version.plist")" == "$VF_XCODE_VERSION" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' "$app/Contents/version.plist")" == "$VF_XCODE_BUILD" ]]
codesign --verify --deep --strict -R '=anchor apple' "$app"
sudo -n rm -rf /Applications/Xcode.app
sudo -n mv "$app" /Applications/Xcode.app
test -x /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
