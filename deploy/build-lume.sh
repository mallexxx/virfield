#!/bin/bash
# Build the pinned NAT-only Lume dependency; never installs or restarts a service.
# Requires Xcode, Swift and network access for the exact Package.resolved revisions.
set -euo pipefail
[[ $# == 1 ]] || { echo "Usage: $0 NEW_OUTPUT_DIRECTORY" >&2; exit 2; }
recipe_dir="$(cd "$(dirname "$0")" && pwd)"
output="$1"
[[ "$output" == /* && ! -e "$output" ]] || { echo 'Output must be a new absolute directory' >&2; exit 2; }
umask 077
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/virfield-lume-build.XXXXXX")"
echo "Build workspace: $work_dir"
curl --fail --location --max-time 300 \
  https://codeload.github.com/trycua/cua/tar.gz/refs/tags/lume-v0.5.3 \
  --output "$work_dir/source.tar.gz"
echo "4585ebc2b492bfa839ae6b2d1a18c1dac6eded6e0c54a9d42885956962233a8c  $work_dir/source.tar.gz" | shasum -a 256 -c -
mkdir "$work_dir/source"
tar -xzf "$work_dir/source.tar.gz" -C "$work_dir/source" --strip-components=3 cua-lume-v0.5.3/libs/lume
cd "$work_dir/source"
patch --batch --fuzz=0 -p1 < "$recipe_dir/lume-0.5.3-guest-shutdown.patch"
patch --batch --fuzz=0 -p1 < "$recipe_dir/lume-0.5.3-registry-integrity.patch"
cp "$recipe_dir/GuestShutdownCacheTests.swift" tests/GuestShutdownCacheTests.swift
cp "$recipe_dir/RegistryIntegrityTests.swift" tests/RegistryIntegrityTests.swift
swift build -c release --disable-automatic-resolution --product lume -j 8
mkdir "$output"
cp .build/release/lume "$output/lume"
cp -R .build/release/lume_lume.bundle "$output/lume_lume.bundle"
cp Package.resolved "$output/Package.resolved"
tar -xOf "$work_dir/source.tar.gz" cua-lume-v0.5.3/LICENSE.md > "$output/LICENSE.md"
cp "$recipe_dir/lume-0.5.3-guest-shutdown.patch" "$output/guest-shutdown.patch"
cp "$recipe_dir/lume-0.5.3-registry-integrity.patch" "$output/registry-integrity.patch"
codesign --force --entitlements "$recipe_dir/lume-nat.entitlements" --sign - "$output/lume"
codesign --verify --strict "$output/lume"
"$output/lume" --version
(cd "$output" && shasum -a 256 lume Package.resolved guest-shutdown.patch registry-integrity.patch > SHA256SUMS)
echo "Built Lume 0.5.3 with Virfield patches: $output"
