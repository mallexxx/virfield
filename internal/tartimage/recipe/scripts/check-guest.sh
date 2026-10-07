#!/bin/bash
# Fail fast before any mutation if the pinned upstream is not what the profile says.
set -euo pipefail
case "$(sysctl -n hw.model)" in VirtualMac*) ;; *) echo 'not a virtual Mac'; exit 64 ;; esac
version="$(sw_vers -productVersion)"
build="$(sw_vers -buildVersion)"
[[ "$version" == "$VF_MACOS" ]] || { echo "macOS $version, expected $VF_MACOS"; exit 65; }
[[ "$build" == "$VF_BUILD" ]] || { echo "build $build, expected $VF_BUILD"; exit 65; }
[[ "$(csrutil status)" == "System Integrity Protection status: $VF_SIP." ]] || { csrutil status; exit 65; }
sudo -n true
available_kib="$(df -k / | awk 'NR==2 {print $4}')"
(( available_kib >= 40 * 1024 * 1024 )) || { echo "only ${available_kib} KiB free"; exit 66; }
echo "guest matches profile: $version ($build), SIP $VF_SIP"
