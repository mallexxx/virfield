#!/usr/bin/env bash
# build-golden-vm.sh — Orchestrator: build a complete golden macOS UI-test VM.
#
# Runs all four phases in sequence:
#   Phase 1 — Create base VM from IPSW         (01-create-vm.sh)
#   Phase 2 — Automate Setup Assistant          (02-setup-assistant.sh)
#   Phase 3 — Disable SIP via recovery mode    (03-disable-sip.sh)
#   Phase 4 — SSH provisioning + Xcode         (04-provision-vm.sh)
#
# Each phase script can also be run individually.
#
# Usage:
#   build-golden-vm.sh run --ipsw PATH [options]
#   build-golden-vm.sh list-ipsw
#   build-golden-vm.sh help [phase]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_lib.sh"

# ── Defaults ──────────────────────────────────────────────────────────────────

IPSW_PATH=""
XCODE_PATH=""
TOOLS="all"
VM_NAME=""
SKIP_PHASES=()
START_PHASE=1
INSTALL_MISSING=false
# VM hardware options (passed to Phase 1)
VM_CPU=4
VM_MEMORY="8GB"
VM_DISK="80GB"
VM_DISPLAY="1920x1080"

# ── Help ──────────────────────────────────────────────────────────────────────

usage() {
  cat <<'TOPHELP'
build-golden-vm.sh — Build a complete macOS UI-test golden VM

Subcommands:
  run            Build the complete golden VM (default)
  list-ipsw      List locally found IPSW files, lume cached images, and VirtualBuddy images
  help [phase]   Show this help, or detailed help for a phase (1–4)

Usage:
  build-golden-vm.sh run --ipsw PATH|latest [options]
  build-golden-vm.sh list-ipsw
  build-golden-vm.sh help [1|2|3|4]

Pipeline options (run subcommand):
  --ipsw PATH|SPEC       macOS IPSW file path or version spec. Required.
                         Specs: latest, sequoia/15, sonoma/14, ventura/13,
                                monterey/12, tahoe/26, or exact (15.6.1)
  --xcode PATH           Xcode.app or Xcode.xip path (skip Xcode install if omitted)
  --tools LIST           Comma-separated tool IDs (default: all)
                         See 'build-golden-vm.sh help 4' for the full tool list.
  --vm NAME              Golden VM name (default: uitest-golden).
                         Intermediate VMs are derived: <name>-base, <name>-nosip.
  --cpu N                vCPUs for Phase 1   (default: 4)
  --memory MEM           RAM for Phase 1     (default: 8GB)
  --disk SIZE            Disk for Phase 1    (default: 80GB)
  --display WxH          Resolution          (default: 1920x1080)
  --skip-phase N         Skip phase N (repeatable: --skip-phase 1 --skip-phase 2)
  --start-phase N        Start from phase N (1–4); assumes prior phases done
  --install-missing      Install missing prerequisites via Homebrew without prompting

TOPHELP
  echo "$COMMON_OPTIONS_HELP"
  cat <<'EXAMPLES'

Examples:
  # Full build
  build-golden-vm.sh run --ipsw ~/Downloads/UniversalMac.ipsw --xcode ~/VMShare/Xcode.app

  # Full build with recording
  build-golden-vm.sh run --ipsw latest --xcode ~/VMShare/Xcode.app --record

  # Start from phase 4 (phases 1–3 already done)
  build-golden-vm.sh run --start-phase 4 --ipsw none --xcode ~/VMShare/Xcode.app

  # Re-run provisioning only, skip Xcode
  build-golden-vm.sh run --start-phase 4 --ipsw none --tools all

  # See tool list
  build-golden-vm.sh help 4
EXAMPLES
  exit 0
}

phase_help() {
  local n="$1"
  case "$n" in
    1) exec "$SCRIPT_DIR/01-create-vm.sh" --help ;;
    2) exec "$SCRIPT_DIR/02-setup-assistant.sh" --help ;;
    3) exec "$SCRIPT_DIR/03-disable-sip.sh" --help ;;
    4) exec "$SCRIPT_DIR/04-provision-vm.sh" --help ;;
    *) echo "Unknown phase: $n (valid: 1 2 3 4)" >&2; exit 1 ;;
  esac
}

# ── Subcommand dispatch ───────────────────────────────────────────────────────

SUBCMD="${1:-run}"
case "$SUBCMD" in
  help)
    shift
    [[ $# -gt 0 ]] && phase_help "$1" || usage
    ;;
  list-ipsw)
    # Delegate to 01-create-vm.sh
    exec "$SCRIPT_DIR/01-create-vm.sh" --list-ipsw
    ;;
  run)
    shift
    ;;
  --help|-h)
    usage
    ;;
  *)
    # If no subcommand given, treat as 'run'
    ;;
esac

# ── Arg parsing (run) ─────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)        usage ;;
    --ipsw)           IPSW_PATH="$2"; shift 2 ;;
    --xcode)          XCODE_PATH="$2"; shift 2 ;;
    --tools)          TOOLS="$2"; shift 2 ;;
    --vm)             VM_NAME="$2"; shift 2 ;;
    --cpu)            VM_CPU="$2"; shift 2 ;;
    --memory)         VM_MEMORY="$2"; shift 2 ;;
    --disk)           VM_DISK="$2"; shift 2 ;;
    --display)        VM_DISPLAY="$2"; shift 2 ;;
    --skip-phase)     SKIP_PHASES+=("$2"); shift 2 ;;
    --start-phase)    START_PHASE="$2"; shift 2 ;;
    --install-missing) INSTALL_MISSING=true; shift ;;
    *) if parse_common_arg "$1" "${2:-}"; then shift "$_NARGS"
       else echo "Unknown option: $1" >&2; usage; fi ;;
  esac
done

# ── Derive intermediate VM names ─────────────────────────────────────────────
BASE_VM="${VM_NAME}-base"
NOSIP_VM="${VM_NAME}-nosip"
GOLDEN_VM="${VM_NAME}"

# ── Prerequisites ─────────────────────────────────────────────────────────────

if $INSTALL_MISSING; then
  check_prerequisites --install
else
  check_prerequisites
fi

# ── Validate ──────────────────────────────────────────────────────────────────

phase_skipped() { local p="$1"; [[ ${#SKIP_PHASES[@]} -eq 0 ]] && return 1; printf '%s\n' "${SKIP_PHASES[@]}" | grep -qx "$p"; }
phase_active()  { local p="$1"; [[ $p -ge $START_PHASE ]] && ! phase_skipped "$p"; }

if [[ -z "$VM_NAME" ]]; then
  echo "Error: --vm NAME is required" >&2; usage
fi

if phase_active 1 && [[ -z "$IPSW_PATH" ]]; then
  echo "Error: --ipsw is required (or use --start-phase 2+ to skip phase 1)" >&2
  usage
fi

# ── Select Setup Assistant preset ────────────────────────────────────────────
# lume has two built-in presets: 'sequoia' (macOS ≤ 15.x) and 'tahoe' (macOS 26+).
# Version-specific YAML overrides take priority (e.g. tahoe-26.4.1.yaml).
_lume_preset_for_major() {
  # Map a macOS major version number to the correct built-in lume preset name.
  if [[ "$1" -ge 26 ]] 2>/dev/null; then echo tahoe; else echo sequoia; fi
}
SETUP_PRESET="sequoia"
if [[ -n "$IPSW_PATH" ]] && [[ "$IPSW_PATH" != "none" ]] && [[ "$IPSW_PATH" != "latest" ]]; then
  if _ipsw_is_version_spec "$IPSW_PATH"; then
    # Version spec (e.g. 'sequoia', 'tahoe', '15', '26') — resolve to lume preset.
    _major="$(_ipsw_major_for_spec "$IPSW_PATH")"
    [[ -n "$_major" ]] && SETUP_PRESET="$(_lume_preset_for_major "$_major")"
  else
    # File path — extract version, look for a version-specific YAML override first.
    _ver="$(macos_version_from_ipsw "$IPSW_PATH")"
    _major="${_ver%%.*}"
    _base_preset="$(_lume_preset_for_major "${_major:-15}")"
    _custom="$SCRIPT_DIR/${_base_preset}-${_ver}.yaml"
    if [[ -n "$_ver" ]] && [[ -f "$_custom" ]]; then
      SETUP_PRESET="$_custom"         # e.g. tahoe-26.4.1.yaml
    elif [[ -n "$_major" ]]; then
      SETUP_PRESET="$(_lume_preset_for_major "$_major")"
    fi
  fi
fi
export SETUP_PRESET

# ── Init shared log dir + state file ─────────────────────────────────────────
# State file is keyed by GOLDEN_VM name so all phases (1-4) and standalone
# console runs write to one unified file (e.g. uitest-26.4.1-golden.json).
# When restarting from a later phase (--start-phase N > 1), reuse the existing
# log dir from the state file so all phases write to one continuous build.log.

_existing_log_dir=""
if [[ $START_PHASE -gt 1 ]]; then
  _existing_log_dir="$(python3 -c "
import json,os,sys
try:
  f='$STATE_DIR/${GOLDEN_VM}.json'
  d=json.loads(open(f).read())
  p=d.get('log','')
  print(p if p and os.path.isdir(p) else '')
except: print('')
" 2>/dev/null || true)"
fi

if [[ -n "$_existing_log_dir" ]]; then
  # Continue in the same log dir — keeps build.log continuous across restarts.
  LOG_DIR="$_existing_log_dir"
  mkdir -p "$LOG_DIR" "$STATE_DIR" "$RECORDINGS_DIR"
  if [[ -z "$STATE_FILE" ]]; then
    STATE_FILE="$STATE_DIR/${GOLDEN_VM}.json"
  fi
  _LOG_FH="$LOG_DIR/build.log"
  ln -sfn "$LOG_DIR" "$LOG_BASE/${GOLDEN_VM}-latest" 2>/dev/null || true
else
  init_log_dir "build-golden" "$GOLDEN_VM"
fi

# Export so all phase sub-scripts share this one state file.
export STATE_FILE
log ""
if [[ $START_PHASE -gt 1 ]]; then
  log "=== build-golden-vm.sh (continuing from phase $START_PHASE) ==="
else
  log "=== build-golden-vm.sh ==="
fi
log "  Base VM:   $BASE_VM"
log "  NoSIP VM:  $NOSIP_VM"
log "  Golden VM: $GOLDEN_VM"
log "  IPSW:      ${IPSW_PATH:-(skipped)}"
log "  Xcode:     ${XCODE_PATH:-(skipped)}"
log "  Tools:     $TOOLS"
log "  CPU: $VM_CPU  Memory: $VM_MEMORY  Disk: $VM_DISK  Display: $VM_DISPLAY"
log "  Log dir:   $LOG_DIR"
log "  State:     $STATE_FILE"

# Initialise state with all stages.
# When --start-phase N > 1, preserve "done"/"failed" status of earlier stages
# from any existing state file instead of overwriting them with "skipped".
python3 - "$STATE_FILE" "$GOLDEN_VM" "$LOG_DIR" \
         "${SKIP_PHASES[*]:-}" "$START_PHASE" "$IPSW_PATH" <<EOF
$_STATE_PY_HELPERS
state_file, vm, log_dir, skip_str, start_str, ipsw_path = sys.argv[1:]
ts = __import__('datetime').datetime.now(__import__('datetime').timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
skipped = set(skip_str.split()) if skip_str else set()
start = int(start_str)

PHASE_LABELS = {
    "01-create-vm":       "Create base VM",
    "02-setup-assistant": "Setup Assistant",
    "03-disable-sip":     "Disable SIP",
    "04-provision-vm":    "Install Xcode & tools",
}

# Load existing state to preserve completed stage history.
prev = state_read(state_file)
prev_stages = prev.get('stages', {})

def phase_status(n, key):
    if str(n) in skipped:
        return "skipped"
    if n < start:
        # Preserve "done" or "failed" from a previous run; otherwise "skipped"
        prev_st = prev_stages.get(key, {}).get('status', '')
        return prev_st if prev_st in ('done', 'failed') else 'skipped'
    return "pending"

stages = {}
for n, (k, v) in enumerate(PHASE_LABELS.items(), 1):
    st = phase_status(n, k)
    entry = {"status": st, "label": v}
    # Carry over timestamps from previous run for completed stages
    if st in ('done', 'failed') and k in prev_stages:
        for f in ('started', 'finished', 'error', 'options'):
            if f in prev_stages[k]:
                entry[f] = prev_stages[k][f]
    stages[k] = entry

# Add download stage when IPSW must be fetched (not a local file / not skipped).
specs = {"latest","sequoia","sonoma","ventura","monterey","tahoe","bigsur","big_sur",
         "15","14","13","12","11","26"}
is_spec = ipsw_path in specs or (ipsw_path.replace(".","").isdigit() and "." in ipsw_path)
if is_spec and start <= 1 and "1" not in skipped:
    stages = {"00-download-ipsw": {"status": "pending", "label": "Download macOS IPSW"}, **stages}

data = {
    "vm": vm, "status": "running",
    "stage": None, "substage": None, "percent": None, "pid": None,
    "updated": ts, "log": log_dir,
    "recordings": prev.get('recordings', []),
    "stages": stages,
}
state_write(state_file, data)
EOF

overall_fail() { state_finalize "failed"; exit 1; }
trap overall_fail ERR

# ── Common flags passed through to each phase script ─────────────────────────

COMMON_ARGS=(--log-dir "$LOG_DIR" --vm-user "$VM_USER" --vm-pass "$VM_PASS" --vmshare "$VMSHARE")
[[ "$RECORD"        == "true" ]] && COMMON_ARGS+=(--record)
[[ -n "$RECORD_OUTPUT"        ]] && COMMON_ARGS+=(--output "$RECORD_OUTPUT")
[[ "$VIEWER"        == "true" ]] && COMMON_ARGS+=(--viewer)

# ── Phase 1: Create base VM ───────────────────────────────────────────────────

_log_phase_skip() {
  local n="$1" label="$2" key="$3"
  local prev_status; prev_status="$(python3 -c "
import json,sys,os
try:
  d=json.loads(open('$STATE_FILE').read())
  print(d.get('stages',{}).get('$key',{}).get('status','skipped'))
except: print('skipped')
" 2>/dev/null)"
  log "Phase $n ($label): $prev_status (not running this session)"
}

if phase_active 1; then
  "$SCRIPT_DIR/01-create-vm.sh" \
    --vm "$BASE_VM" --ipsw "$IPSW_PATH" \
    --cpu "$VM_CPU" --memory "$VM_MEMORY" --disk "$VM_DISK" --display "$VM_DISPLAY" \
    "${COMMON_ARGS[@]}"
else
  _log_phase_skip 1 "Create base VM" "01-create-vm"
fi

# ── Phase 2: Setup Assistant ──────────────────────────────────────────────────

if phase_active 2; then
  "$SCRIPT_DIR/02-setup-assistant.sh" \
    --vm "$BASE_VM" \
    "${COMMON_ARGS[@]}"
else
  _log_phase_skip 2 "Setup Assistant" "02-setup-assistant"
fi

# ── Phase 3: Disable SIP ─────────────────────────────────────────────────────

if phase_active 3; then
  "$SCRIPT_DIR/03-disable-sip.sh" \
    --vm "$GOLDEN_VM" \
    "${COMMON_ARGS[@]}"
else
  _log_phase_skip 3 "Disable SIP" "03-disable-sip"
fi

# ── Phase 4: Provision ────────────────────────────────────────────────────────

if phase_active 4; then
  PHASE4_ARGS=("${COMMON_ARGS[@]}" --tools "$TOOLS")
  [[ -n "$XCODE_PATH" ]] && PHASE4_ARGS+=(--xcode "$XCODE_PATH")
  "$SCRIPT_DIR/04-provision-vm.sh" \
    --vm "$GOLDEN_VM" \
    "${PHASE4_ARGS[@]}"
else
  log "Phase 4: skipped"
fi

# ── Done ──────────────────────────────────────────────────────────────────────

state_finalize "done"
step "BUILD COMPLETE"
log "  Golden VM:  $GOLDEN_VM (stopped, ready for UI tests)"
log "  State:      $STATE_FILE"
log "  Log dir:    $LOG_DIR"
log "  Symlink:    $LOG_BASE/${GOLDEN_VM}-latest"
