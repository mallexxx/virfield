#!/usr/bin/env bash
# 03-disable-sip.sh — Phase 3: Disable SIP via recovery mode VNC.
#
# Clones the base VM, boots the clone in recovery mode, and runs
# vnc-send-keys.py to inject the csrutil disable sequence.
#
# Usage:
#   03-disable-sip.sh [--source-vm NAME] [--vm NAME] [options]
#
# Run standalone or via build-golden-vm.sh.
#
# Prerequisites:
#   - Source VM has completed Setup Assistant (lume user exists).
#   - vnc-send-keys.py is in the same directory as this script.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_lib.sh"

STAGE_ID="03-disable-sip"

# ── Defaults ──────────────────────────────────────────────────────────────────

VM_NAME=""

# ── Help ──────────────────────────────────────────────────────────────────────

usage() {
  cat <<EOF
03-disable-sip.sh — Phase 3: Disable SIP via recovery mode VNC

Boots the VM in recovery mode and injects the VNC key sequence to disable SIP.
Operates on the VM in-place — no cloning.

Usage:
  03-disable-sip.sh --vm NAME [options]

Options:
  --vm NAME          VM to disable SIP on (required)
$COMMON_OPTIONS_HELP

  --record           Records VNC framebuffer during SIP disable sequence.
  --viewer           Opens VNC in system viewer (default: off).
                     WARNING: Screen Sharing kills lume's VNC proxy in
                     recovery mode. Use a compatible viewer.

Examples:
  03-disable-sip.sh --vm uitest-26.4.1-golden --record
  03-disable-sip.sh --vm macos-15-golden-nosip --viewer
EOF
  exit 0
}

# ── Arg parsing ───────────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)    usage ;;
    --source-vm)  shift 2 ;;  # ignored, kept for orchestrator compat
    --vm)         VM_NAME="$2"; shift 2 ;;
    --skip-clone) shift ;;    # ignored, no cloning
    *) if parse_common_arg "$1" "${2:-}"; then shift "$_NARGS"
       else echo "Unknown option: $1" >&2; usage; fi ;;
  esac
done

# ── Init ──────────────────────────────────────────────────────────────────────

[[ -z "$VM_NAME" ]] && { echo "Error: --vm NAME is required" >&2; usage; }
init_log_dir "$STAGE_ID" "$VM_NAME"

LOCK_DIR="/tmp/virfield-provision-${VM_NAME}.lock"
# Plain-file lock = stale legacy flock artifact; remove it so mkdir can proceed.
[[ -f "$LOCK_DIR" && ! -d "$LOCK_DIR" ]] && rm -f "$LOCK_DIR"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  LOCK_PID="$(cat "$LOCK_DIR/pid" 2>/dev/null || echo '?')"
  if [[ "$LOCK_PID" == "?" ]] || ! kill -0 "$LOCK_PID" 2>/dev/null; then
    rm -rf "$LOCK_DIR"
    mkdir "$LOCK_DIR"
  else
    die "Another instance of $STAGE_ID is already running for '$VM_NAME' (PID: $LOCK_PID). Aborting."
  fi
fi
echo "$$" > "$LOCK_DIR/pid"

state_stage_start "$STAGE_ID" "Disable SIP" "Starting..." "$$" \
  "{\"vm\":\"$VM_NAME\"}"

LUME_RUN_PID=""
_EXITING=false
cleanup() {
  $_EXITING && return; _EXITING=true
  rm -rf "$LOCK_DIR" 2>/dev/null || true
  [[ -n "$LUME_RUN_PID" ]] && lume_stop_vm "$VM_NAME" "$LUME_RUN_PID" || true
  state_stage_fail "$STAGE_ID" "interrupted"
}
trap cleanup EXIT INT TERM

# ── Run ───────────────────────────────────────────────────────────────────────

step "Phase 3: Disable SIP on '$VM_NAME'"
log "  Log dir: $LOG_DIR"

vm_exists "$VM_NAME" || die "VM '$VM_NAME' not found."

log "  Booting '$VM_NAME' in recovery mode..."
LUME_RUN_PID="$(lume_run_bg "$VM_NAME" --recovery-mode true --no-display)"
state_progress "$STAGE_ID" "Booting into recovery mode..." "" "$LUME_RUN_PID"

# Build vnc-send-keys.py args
VNC_ARGS=("$VM_NAME")
[[ "$RECORD" == "true" ]] && VNC_ARGS+=("--record")
[[ -n "$RECORD_OUTPUT" ]] && VNC_ARGS+=("--output" "$RECORD_OUTPUT")
[[ "$VIEWER" == "true" ]] && VNC_ARGS+=("--viewer")
VNC_ARGS+=("--log-dir" "$LOG_DIR")

log "  Running vnc-send-keys.py ${VNC_ARGS[*]}"
state_progress "$STAGE_ID" "Sending VNC key sequence (csrutil disable)..." "" "$LUME_RUN_PID"
VNC_USERNAME="$VM_USER" VNC_PASSWORD="$VM_PASS" \
  python3 "$SCRIPT_DIR/vnc-send-keys.py" "${VNC_ARGS[@]}" 2>&1 | tee -a "$_LOG_FH"

# vnc-send-keys.py halts the VM and calls lume stop internally,
# but call stop again to ensure clean state.
state_progress "$STAGE_ID" "Verifying SIP disabled, stopping VM..." "" "$LUME_RUN_PID"
lume_stop_vm "$VM_NAME" "$LUME_RUN_PID" 2>/dev/null || true
LUME_RUN_PID=""
rm -rf "$LOCK_DIR" 2>/dev/null || true
trap - EXIT INT TERM

state_stage_done "$STAGE_ID"
log "Phase 3 complete — '$VM_NAME' has SIP disabled (stopped)."
log "State: $STATE_FILE"
