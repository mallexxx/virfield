#!/bin/bash
# Port of the Lume developer-v1 post-install; the user is the SSH session's.
set -euo pipefail
user="$(id -un)"
sudo -n /usr/bin/xcode-select -s /Applications/Xcode.app
sudo -n /usr/bin/xcodebuild -license accept
sudo -n /usr/bin/xcodebuild -runFirstLaunch
sudo -n /usr/sbin/DevToolsSecurity -enable
sudo -n /usr/sbin/dseditgroup -o edit -a "$user" -t user _developer
# Host LLDB symbolication reads the guest dyld cache over a read-only export.
dyld_cache=
for candidate in /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld /System/Library/dyld; do
  if test -d "$candidate"; then
    dyld_cache="$candidate"
    break
  fi
done
test -n "$dyld_cache"
dyld_export="\"$dyld_cache\" -ro -mapall=$(id -u):$(id -g) -network 192.168.64.0 -mask 255.255.255.0"
sudo -n touch /etc/exports
sudo -n grep -Fqx "$dyld_export" /etc/exports || printf '%s\n' "$dyld_export" | sudo -n tee -a /etc/exports >/dev/null
sudo -n /sbin/nfsd enable
sudo -n /sbin/nfsd restart
/usr/bin/xcodebuild -version
