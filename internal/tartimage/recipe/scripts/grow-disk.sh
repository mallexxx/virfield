#!/bin/bash
# Tart grows the raw disk to disk_size_gb; the boot APFS container must be
# grown inside the guest before Xcode is expanded.
set -euo pipefail
store="$(diskutil info -plist / | plutil -extract APFSPhysicalStores.0.APFSPhysicalStore raw -o - -)"
[[ "$store" =~ ^disk[0-9]+s[0-9]+$ ]] || { echo "unexpected APFS physical store: $store"; exit 66; }
printf 'y\n' | sudo -n diskutil repairDisk "${store%s*}"
sudo -n diskutil apfs resizeContainer "$store" 0
available_kib="$(df -k / | awk 'NR==2 {print $4}')"
(( available_kib >= 40 * 1024 * 1024 )) || { echo "only ${available_kib} KiB free"; exit 66; }
echo "boot volume has ${available_kib} KiB free"
