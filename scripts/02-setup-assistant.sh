#!/usr/bin/env bash
# 02-setup-assistant.sh — Phase 2: Automate macOS Setup Assistant.
#
# Runs 'lume setup --unattended <preset>', which manages the VM lifecycle
# internally (boot, reboots during setup, VNC automation). We run it in the
# background to surface step-by-step progress in the state JSON and build log,
# apply a hard timeout, and start the VNC recorder once the VM is up.
# Preset is 'tahoe' for macOS 26+, 'sequoia' for macOS 11–15 (default).
#
# After setup completes, boots the VM if needed, waits for SSH, then stops.
#
# Usage:
#   02-setup-assistant.sh [--vm NAME] [options]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_lib.sh"

STAGE_ID="02-setup-assistant"

# ── Defaults ──────────────────────────────────────────────────────────────────

VM_NAME=""
SETUP_TIMEOUT_S=1800   # 30 min hard limit for lume setup

# ── Help ──────────────────────────────────────────────────────────────────────

usage() {
  cat <<EOF
02-setup-assistant.sh — Phase 2: Automate macOS Setup Assistant

Runs 'lume setup --unattended <preset>' which boots the VM, automates the Setup
Assistant via VNC, and creates the 'lume' user with SSH enabled. lume setup
manages the VM lifecycle including mid-setup reboots.
Preset: 'tahoe' for macOS 26+ (167 steps), 'sequoia' for macOS 11–15.

Usage:
  02-setup-assistant.sh [options]

Options:
  --vm NAME    VM name (default: $VM_NAME)
$COMMON_OPTIONS_HELP

  --record     Records the VNC framebuffer during Setup Assistant automation.
  --viewer     Opens the VNC session in the system viewer for observation.

Examples:
  02-setup-assistant.sh
  02-setup-assistant.sh --vm my-base-vm --record
EOF
  exit 0
}

# ── Arg parsing ───────────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h) usage ;;
    --vm)      VM_NAME="$2"; shift 2 ;;
    *) if parse_common_arg "$1" "${2:-}"; then shift "$_NARGS"
       else echo "Unknown option: $1" >&2; usage; fi ;;
  esac
done

# ── Init ──────────────────────────────────────────────────────────────────────

[[ -z "$VM_NAME" ]] && { echo "Error: --vm NAME is required" >&2; usage; }
init_log_dir "$STAGE_ID" "$VM_NAME"
state_stage_start "$STAGE_ID" "Setup Assistant" "Starting lume setup..." "$$" \
  "{\"vm\":\"$VM_NAME\"}"

LUME_RUN_PID=""
SETUP_PID=""
TAIL_PID=""

cleanup() {
  local reason="${1:-interrupted}"
  kill "$SETUP_PID" 2>/dev/null || true
  kill "$TAIL_PID"  2>/dev/null || true
  stop_recorder
  [[ -n "$LUME_RUN_PID" ]] && lume_stop_vm "$VM_NAME" "$LUME_RUN_PID" || true
  state_stage_fail "$STAGE_ID" "$reason"
}
trap 'cleanup interrupted' INT TERM

# ── Preflight ─────────────────────────────────────────────────────────────────

step "Phase 2: Setup Assistant on '$VM_NAME'"
log "  Log dir: $LOG_DIR"

vm_exists "$VM_NAME" || die "VM '$VM_NAME' not found. Run 01-create-vm.sh first."

# Ensure VM is stopped — lume setup will boot it itself
if lume ls 2>/dev/null | grep -qE "^${VM_NAME}[[:space:]].*running"; then
  log "  VM is running — stopping before lume setup takes over..."
  lume stop "$VM_NAME" >> "$_LOG_FH" 2>&1 || true
  sleep 3
fi

# SETUP_PRESET is exported by build-golden-vm.sh (which detects the correct preset
# from the IPSW version).  When run standalone, default to 'sequoia' — the safer
# choice because it covers macOS 11–15; use SETUP_PRESET=tahoe or export it from
# the caller to build macOS 26+ VMs.
: "${SETUP_PRESET:=sequoia}"

# ── lume setup ───────────────────────────────────────────────────────────────
# lume setup boots the VM internally, handles mid-setup reboots, automates
# the Setup Assistant via VNC, and leaves the VM running when done.

SETUP_LOG="$LOG_DIR/lume-setup.log"
DEBUG_DIR="$LOG_DIR/debug-screenshots"
mkdir -p "$DEBUG_DIR"
log "  Running: lume setup $VM_NAME --unattended $SETUP_PRESET --debug --debug-dir $DEBUG_DIR"
log "  Setup log: $SETUP_LOG"
log "  Debug screenshots: $DEBUG_DIR"
state_progress "$STAGE_ID" "Starting Setup Assistant..." "" "$$"

lume setup "$VM_NAME" --unattended "$SETUP_PRESET" --debug --debug-dir "$DEBUG_DIR" > "$SETUP_LOG" 2>&1 &
SETUP_PID=$!
state_progress "$STAGE_ID" "lume setup starting..." "" "$SETUP_PID"

# Tail setup log into build log with [lume] prefix so it's distinct from
# our own timestamped log() lines (lume uses UTC; our log() uses local time).
tail -f "$SETUP_LOG" | sed -u 's/^/[lume] /' >> "$_LOG_FH" &
TAIL_PID=$!

_setup_start=$SECONDS
_recorder_started=false
_prev_idx=""
_prev_sub=""

while kill -0 "$SETUP_PID" 2>/dev/null; do
  sleep 5

  # ── Hard timeout ──────────────────────────────────────────────────────────
  if (( SECONDS - _setup_start > SETUP_TIMEOUT_S )); then
    log "  ERROR: lume setup timed out after ${SETUP_TIMEOUT_S}s"
    cleanup "lume setup timed out after ${SETUP_TIMEOUT_S}s"
    exit 1
  fi

  # ── Start recorder once VM is up ──────────────────────────────────────────
  if [[ "$_recorder_started" == "false" ]]; then
    if lume ls 2>/dev/null | grep -qE "^${VM_NAME}[[:space:]].*running"; then
      open_vnc_viewer "$VM_NAME"
      start_recorder "$VM_NAME"
      _recorder_started=true
    fi
  fi

  # ── Surface step progress in state JSON ───────────────────────────────────
  # Parse "index=X/N" from setup log for percentage + human-readable progress.
  _idx_line="$(grep -oE 'index=[0-9]+/[0-9]+' "$SETUP_LOG" 2>/dev/null | tail -1 || true)"
  if [[ -n "$_idx_line" && "$_idx_line" != "$_prev_idx" ]]; then
    _prev_idx="$_idx_line"
    _cur="$(echo "$_idx_line" | cut -d= -f2 | cut -d/ -f1)"
    _tot="$(echo "$_idx_line" | cut -d= -f2 | cut -d/ -f2)"
    _pct=$(( _cur * 100 / _tot ))
    state_progress "$STAGE_ID" "Setup wizard: step ${_cur}/${_tot}" "$_pct" "$SETUP_PID"
  elif [[ -z "$_idx_line" ]]; then
    # Before step execution — show last meaningful log line for substage
    _last="$(grep -v 'Socket error\|retrying\|Executing boot command' "$SETUP_LOG" 2>/dev/null | tail -1 | sed 's/.*INFO: //' | cut -c1-100 || true)"
    if [[ -n "$_last" && "$_last" != "$_prev_sub" ]]; then
      _prev_sub="$_last"
      state_progress "$STAGE_ID" "Setup: $_last" "" "$SETUP_PID"
    fi
  fi
done

kill "$TAIL_PID" 2>/dev/null || true
TAIL_PID=""

wait "$SETUP_PID" && _setup_exit=0 || _setup_exit=$?
SETUP_PID=""

if [[ $_setup_exit -ne 0 ]]; then
  log "  ERROR: lume setup exited with code $_setup_exit"
  cleanup "lume setup failed (exit $_setup_exit)"
  exit 1
fi

stop_recorder
log "  lume setup completed successfully."

# ── Wait for SSH ──────────────────────────────────────────────────────────────
# lume setup usually leaves the VM running. If not, boot it for SSH check.

state_progress "$STAGE_ID" "Checking VM state after setup..." "" ""

if ! lume ls 2>/dev/null | grep -qE "^${VM_NAME}[[:space:]].*running"; then
  log "  VM is stopped after setup — booting for SSH check..."
  LUME_RUN_PID="$(lume_run_bg "$VM_NAME" --no-display)"
  state_progress "$STAGE_ID" "Booting VM for SSH check..." "" "$LUME_RUN_PID"
  sleep 10
fi

log "  Waiting for SSH (up to 3 min)..."
state_progress "$STAGE_ID" "Waiting for SSH..." "" "${LUME_RUN_PID:-}"
wait_ssh "$VM_NAME" 180
state_set_vm_info "$VM_NAME"

# ── Stop VM ───────────────────────────────────────────────────────────────────

log "  Stopping VM..."
state_progress "$STAGE_ID" "Stopping VM..." "" "${LUME_RUN_PID:-}"
if [[ -n "$LUME_RUN_PID" ]]; then
  lume_stop_vm "$VM_NAME" "$LUME_RUN_PID"
  LUME_RUN_PID=""
else
  lume stop "$VM_NAME" >> "$_LOG_FH" 2>&1 || true
fi

state_stage_done "$STAGE_ID"
log "Phase 2 complete — '$VM_NAME' has lume user + SSH enabled (stopped)."
log "State: $STATE_FILE"
