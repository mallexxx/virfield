#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
app="$root/bin/VirfieldAppleBrowser.app"
mkdir -p "$app/Contents/MacOS"
cp "$root/apple-browser/Info.plist" "$app/Contents/Info.plist"
browser_arch=$(uname -m)
browser_macos_min=$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$app/Contents/Info.plist")
swiftc -swift-version 5 -O -module-cache-path /tmp/virfield-swift-module-cache \
  -target "${browser_arch}-apple-macos${browser_macos_min}" \
  -framework AppKit -framework WebKit \
  -o "$app/Contents/MacOS/VirfieldAppleBrowser" \
  "$root/apple-browser/TrustedOrigin.swift" "$root/apple-browser/main.swift"
codesign --force --deep --sign - "$app"
codesign --verify --strict "$app"
