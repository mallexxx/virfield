#!/bin/bash
# Tart grows the raw disk to disk_size_gb. The plugin claims the space itself
# only when it relocates a recovery partition (Monterey); otherwise (Ventura
# base) the boot APFS container must be grown here before Xcode is expanded.
set -euo pipefail
required_kib=$((40 * 1024 * 1024))
available() { df -k / | awk 'NR==2 {print $4}'; }
if (( $(available) < required_kib )); then
  store="$(diskutil info -plist / | plutil -extract APFSPhysicalStores.0.APFSPhysicalStore raw -o - -)"
  [[ "$store" =~ ^disk[0-9]+s[0-9]+$ ]] || { echo "unexpected APFS physical store: $store"; exit 66; }
  printf 'y\n' | sudo -n diskutil repairDisk "${store%s*}"
  sudo -n diskutil apfs resizeContainer "$store" 0
fi
(( $(available) >= required_kib )) || { echo "only $(available) KiB free"; exit 66; }
echo "boot volume has $(available) KiB free"
