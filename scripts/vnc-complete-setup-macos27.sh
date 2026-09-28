#!/usr/bin/env bash
# Complete the Setup Assistant screens left open by Lume 0.5.3's offline setup.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_lib.sh"
source "$SCRIPT_DIR/_vnc_tool.sh"

VM_NAME="$1"
LOG_DIR="${2:-.}"
mkdir -p "$LOG_DIR"
ensure_vncdotool

vnc_url=""
for _ in $(seq 1 60); do
  vnc_url="$(lume ls --format json 2>/dev/null | python3 -c '
import json, sys
for vm in json.load(sys.stdin):
    if vm.get("name") == sys.argv[1]:
        print(vm.get("vncUrl") or "")
' "$VM_NAME" 2>/dev/null || true)"
  [[ -n "$vnc_url" ]] && break
  sleep 2
done
[[ -n "$vnc_url" ]] || die "No VNC URL for $VM_NAME"

read -r vnc_password vnc_host vnc_port < <(
  python3 -c 'import re,sys; m=re.match(r"vnc://:([^@]+)@([^:]+):(\d+)",sys.argv[1]); print(*m.groups())' "$vnc_url"
)
vnc() { "$VNCDTOOL" -s "$vnc_host::$vnc_port" -p "$vnc_password" "$@"; }
tap() { vnc move "$1" "$2" pause 1 click 1 pause "${3:-5}"; }

# macOS 27 resumes after Lume's offline account creation at Accessibility.
welcome_submitted=false
for attempt in $(seq 1 40); do
  image="$LOG_DIR/setup-assistant-27-${attempt}.png"
  vnc capture "$image"
  screen="$(tesseract "$image" stdout 2>/dev/null | tr '\n' ' ')"
  log "  macOS 27 Setup Assistant step $attempt: ${screen:0:120}"

  case "$screen" in
    *Accessibility*)
      tap 1600 1058 12 ;; # Not Now
    *"Are you sure you want to skip signing in"*)
      tap 1080 746 8 ;; # Skip confirmation
    *"Sign In to Your Apple Account"*)
      tap 360 1058 2 # Other Sign-In Options
      tap 377 1031 5 ;; # Sign in Later in Settings
    *"Age Range"*)
      tap 960 888 8 ;; # Adult (18 or older)
    *"Choose Your Look"*|*Analytics*)
      tap 1600 1058 8 ;; # Continue
    *"Screen Time"*)
      tap 1300 1058 8 ;; # Set Up Later
    *"Mac Data Will Not Be"*)
      tap 1080 667 8 ;; # Confirm FileVault skip
    *"Your Mac is Ready for FileVault"*)
      tap 1300 1058 8 ;; # Not Now for disposable test VM
    *"Update Mac Automatically"*|*Siri*|*Location*)
      tap 1600 1058 8 ;; # Continue / default selection
    *"Liquid Glass"*|*Welcome*|*"Get Started"*)
      tap 960 845 15 # Get Started
      welcome_submitted=true ;;
    *)
      if $welcome_submitted || [[ "$screen" == *"Last login:"* ]]; then
        log "  Setup Assistant has exited to the desktop."
        exit 0
      fi
      die "Unrecognized macOS 27 Setup Assistant screen; screenshot: $image" ;;
  esac
done

die "macOS 27 Setup Assistant did not finish after 40 steps"
