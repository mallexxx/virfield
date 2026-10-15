#!/bin/bash
# Tart grows the raw disk to disk_size_gb. The plugin claims the space itself
# only when it relocates a recovery partition (Monterey); otherwise (Ventura
# base) the boot APFS container must be grown here before Xcode is expanded.
set -euo pipefail
required_kib=$((40 * 1024 * 1024))
# Installation writes go to Data, while / is a sealed system snapshot. APFS
# capacity notifications can also lag a successful resize; never resize twice.
available() {
  local free
  free=$(df -k /System/Volumes/Data | awk 'NR==2 {print $4}')
  [[ "$free" =~ ^[0-9]+$ ]] || { echo 'cannot read Data volume free space' >&2; return 66; }
  printf '%s' "$free"
}
free=$(available)
if (( free < required_kib )); then
  store="$(diskutil info -plist / | plutil -extract APFSPhysicalStores.0.APFSPhysicalStore raw -o - -)"
  [[ "$store" =~ ^disk[0-9]+s[0-9]+$ ]] || { echo "unexpected APFS physical store: $store"; exit 66; }
  printf 'y\n' | sudo -n diskutil repairDisk "${store%s*}"
  sudo -n diskutil apfs resizeContainer "$store" 0
fi
for ((attempt=0; attempt<30; attempt++)); do
  free=$(available)
  if (( free >= required_kib )); then
    echo "Data volume has $free KiB free (capacity check $attempt)"
    df -k / /System/Volumes/Data
    exit 0
  fi
  sleep 1
done
echo "only $free KiB free on Data after resize"
df -k / /System/Volumes/Data
exit 66
