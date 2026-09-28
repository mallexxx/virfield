#!/usr/bin/env bash
# macOS 27 Recovery SIP workflow for Lume 0.5.3.
# Its VNC proxy needs vncdotool; the legacy raw-RFB client does not deliver input.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_vnc_tool.sh"

VM_NAME="$1"
VM_USER="${VNC_USERNAME:-lume}"
VM_PASS="${VNC_PASSWORD:-lume}"
LOG_DIR="${2:-.}"

ensure_vncdotool

vnc_url=""
for _ in $(seq 1 60); do
  vnc_url="$(lume ls --format json 2>/dev/null | python3 -c '
import json, re, sys
for vm in json.load(sys.stdin):
    if vm.get("name") == sys.argv[1]:
        print(vm.get("vncUrl") or "")
' "$VM_NAME" 2>/dev/null || true)"
  [[ -n "$vnc_url" ]] && break
  sleep 2
done
[[ -n "$vnc_url" ]] || { echo "No VNC URL for $VM_NAME" >&2; exit 1; }

read -r vnc_password vnc_host vnc_port < <(
  python3 -c 'import re,sys; m=re.match(r"vnc://:([^@]+)@([^:]+):(\d+)",sys.argv[1]); print(*m.groups())' "$vnc_url"
)
VNC=("$VNCDTOOL" -s "$vnc_host::$vnc_port" -p "$vnc_password")
shot() { "${VNC[@]}" capture "$LOG_DIR/$1"; }

# Options is the second boot choice. Recovery then exposes a language chooser.
"${VNC[@]}" key right key right key enter pause 70
"${VNC[@]}" key enter pause 15
shot 01-recovery-home.png

# macOS 27 puts Terminal in the Utilities menu; opening the first recovery tile
# starts Time Machine instead. These coordinates are for Virfield's 1920x1080 VM.
"${VNC[@]}" move 661 30 click 1 pause 1
"${VNC[@]}" move 695 192 click 1 pause 4
shot 02-terminal.png
"${VNC[@]}" type 'csrutil disable' key enter pause 2
"${VNC[@]}" type y key enter pause 1
"${VNC[@]}" type "$VM_PASS" key enter pause 2
shot 03-sip-disabled.png
"${VNC[@]}" type reboot key enter
