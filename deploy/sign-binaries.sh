#!/bin/sh
# Sign Go binaries with a stable identity and identifier so macOS privacy
# grants survive rebuilds. Falls back to the linker's ad-hoc signature.
set -eu
identity="$1"
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -z "$identity" ] || ! security find-identity -v -p codesigning | grep -qF "\"$identity\""; then
  echo "warning: signing identity unavailable; binaries keep ad-hoc signatures" >&2
  exit 0
fi
for binary in "$root"/bin/virfield*; do
  [ -f "$binary" ] || continue
  codesign --force --timestamp=none --sign "$identity" --identifier "ai.virfield.$(basename "$binary")" "$binary"
  codesign --verify --strict "$binary"
done
