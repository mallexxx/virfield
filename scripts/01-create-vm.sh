#!/usr/bin/env bash
# 01-create-vm.sh — Phase 1: Create base VM from IPSW.
#
# Usage:
#   01-create-vm.sh --ipsw PATH [options]
#   01-create-vm.sh --list-ipsw
#
# Run standalone or via build-golden-vm.sh.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_lib.sh"

STAGE_ID="01-create-vm"

# ── Defaults ──────────────────────────────────────────────────────────────────

VM_NAME=""
IPSW_PATH=""
CPU=4
MEMORY="8GB"
DISK="80GB"
DISPLAY_RES="1920x1080"
LIST_IPSW=false

# ── Help ──────────────────────────────────────────────────────────────────────

usage() {
  cat <<EOF
01-create-vm.sh — Phase 1: Create base macOS VM from IPSW

Usage:
  01-create-vm.sh --ipsw PATH|latest [options]
  01-create-vm.sh --list-ipsw

Options:
  --ipsw PATH|SPEC       Path to a local IPSW file, or a version spec:
                           latest         Newest version supported by lume
                           sequoia / 15   macOS 15 Sequoia (latest patch)
                           sonoma  / 14   macOS 14 Sonoma
                           ventura / 13   macOS 13 Ventura
                           monterey/ 12   macOS 12 Monterey
                           tahoe   / 26   macOS 26 Tahoe
                           15.6.1         Exact version
                         Named versions are resolved via api.ipsw.me and
                         downloaded with aria2c (resumable, 16 connections).
  --list-ipsw            List locally found IPSW files and remote catalog, then exit.
  --vm NAME              VM name (default: $VM_NAME)
  --cpu N                vCPUs (default: $CPU)
  --memory MEM           RAM, e.g. 8GB (default: $MEMORY)
  --disk SIZE            Disk size, e.g. 80GB (default: $DISK)
  --display WxH          Resolution (default: $DISPLAY_RES)
                         Must be 1920x1080 — WindowServer won't start at lower
                         resolutions in the Virtualization framework.
$COMMON_OPTIONS_HELP

Examples:
  01-create-vm.sh --ipsw ~/Downloads/UniversalMac.ipsw
  01-create-vm.sh --ipsw latest --vm my-test-vm --cpu 8 --memory 16GB
  01-create-vm.sh --list-ipsw
EOF
  exit 0
}

list_ipsw() {
  echo "Local IPSW files:"
  local found=0
  for dir in \
      "$HOME/Downloads" \
      "$HOME/VMShare" \
      "$HOME/Library/Application Support/VirtualBuddy/_Downloads" \
      "$HOME/Library/Caches"; do
    if [[ -d "$dir" ]]; then
      while IFS= read -r -d '' f; do
        local partial=""
        [[ -f "${f}.aria2" ]] && partial=" (incomplete — downloading)"
        echo "  $f$partial"
        found=1
      done < <(find "$dir" -maxdepth 2 -name "*.ipsw" -print0 2>/dev/null)
    fi
  done
  [[ $found -eq 0 ]] && echo "  (none found)"
  echo ""
  echo "Available versions (api.ipsw.me — VirtualMac2,1):"
  list_ipsw_catalog
  echo ""
  echo "Latest supported by lume:"
  lume ipsw 2>/dev/null | grep '^https://' | tail -1 || echo "  (lume ipsw failed)"
  echo ""
  echo "Cached OCI images (lume images):"
  lume images 2>/dev/null || echo "  (none)"
}

# ── Arg parsing ───────────────────────────────────────────────────────────────

[[ $# -eq 0 ]] && usage

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)     usage ;;
    --list-ipsw)   LIST_IPSW=true; shift ;;
    --ipsw)        IPSW_PATH="$2"; shift 2 ;;
    --vm)          VM_NAME="$2"; shift 2 ;;
    --cpu)         CPU="$2"; shift 2 ;;
    --memory)      MEMORY="$2"; shift 2 ;;
    --disk)        DISK="$2"; shift 2 ;;
    --display)     DISPLAY_RES="$2"; shift 2 ;;
    *) if parse_common_arg "$1" "${2:-}"; then shift "$_NARGS"
       else echo "Unknown option: $1" >&2; usage; fi ;;
  esac
done

if $LIST_IPSW; then list_ipsw; exit 0; fi
[[ -z "$IPSW_PATH" ]] && { echo "Error: --ipsw is required" >&2; usage; }

# ── Resolve version spec → local IPSW path ───────────────────────────────────
# Accepts: 'latest', named alias (sequoia/sonoma/…), major number (15/14/…),
# exact version (15.6.1), or a local file path.
#
# Named specs are resolved via api.ipsw.me; 'latest' via lume ipsw.
# Both are downloaded to VMShare via aria2c (resumable, 16 connections, no timeout)
# because lume create --ipsw latest has a hard internal timeout (~2.5 hrs) that
# is not enough for large IPSWs (macOS 26 = 18.3 GB).

if _ipsw_is_version_spec "$IPSW_PATH"; then
  _ipsw_url=""
  if [[ "$IPSW_PATH" == "latest" ]]; then
    log "Resolving latest IPSW URL (lume ipsw)..."
    _ipsw_url="$(lume ipsw 2>/dev/null | grep '^https://' | tail -1 || true)"
    [[ -z "$_ipsw_url" ]] && die "Failed to resolve IPSW URL from 'lume ipsw'"
  else
    log "Resolving IPSW URL for '$IPSW_PATH' (api.ipsw.me)..."
    _ipsw_url="$(resolve_ipsw_version "$IPSW_PATH")" \
      || die "Could not resolve IPSW URL for version spec: $IPSW_PATH"
  fi

  _ipsw_filename="$(basename "$_ipsw_url")"
  _ipsw_local="$VMSHARE/$_ipsw_filename"
  log "  URL:   $_ipsw_url"
  log "  Local: $_ipsw_local"

  if [[ -f "$_ipsw_local" ]] && [[ ! -f "${_ipsw_local}.aria2" ]]; then
    log "  IPSW already downloaded — using cached copy."
  else
    log "  Downloading IPSW via aria2c (16 connections, resumable)..."
    if ! command -v aria2c &>/dev/null; then
      log "  aria2c not found — installing via brew..."
      brew install aria2 2>&1 | tail -3
    fi
    mkdir -p "$VMSHARE"
    _ipsw_size_total=0
    # Get expected file size from HTTP Content-Length before starting download.
    _ipsw_size_total="$(curl -sI "$_ipsw_url" 2>/dev/null \
      | grep -i '^content-length:' | tail -1 | tr -d '[:space:]' | cut -d: -f2 || true)"

    aria2c \
      -x 16 -s 16 --max-connection-per-server=16 \
      --retry-wait=5 --max-tries=0 \
      --continue=true \
      --file-allocation=none \
      --quiet \
      -d "$VMSHARE" \
      -o "$_ipsw_filename" \
      "$_ipsw_url" \
      >> "${_LOG_FH:-/dev/stderr}" 2>&1 &
    _aria2c_pid=$!

    # Update state to show download in progress (STATE_FILE may be set by orchestrator).
    # Use state_stage_start here so the filename is visible from the very first write.
    state_stage_start "00-download-ipsw" "Download macOS IPSW" "Downloading $_ipsw_filename..." "$_aria2c_pid"

    # Log progress every 30s while aria2c runs.
    # Note: do NOT use 'local' here — this loop is at script scope (not inside a
    # function), and 'local' outside a function exits 1, which triggers set -e.
    while kill -0 "$_aria2c_pid" 2>/dev/null; do
      sleep 30
      kill -0 "$_aria2c_pid" 2>/dev/null || break
      _sz="$(stat -f%z "$_ipsw_local" 2>/dev/null || echo 0)"
      _mib=$(( _sz / 1048576 ))
      if [[ -n "$_ipsw_size_total" && "$_ipsw_size_total" -gt 0 ]]; then
        _total_mib=$(( _ipsw_size_total / 1048576 ))
        _pct=$(( _sz * 100 / _ipsw_size_total ))
        log "  Download progress: ${_mib}/${_total_mib} MiB (${_pct}%)"
        state_progress "00-download-ipsw" "${_mib}/${_total_mib} MiB (${_pct}%)" "$_pct" "$_aria2c_pid"
      else
        log "  Download progress: ${_mib} MiB"
        state_progress "00-download-ipsw" "${_mib} MiB downloaded..." "" "$_aria2c_pid"
      fi
    done
    wait "$_aria2c_pid"
    [[ -f "$_ipsw_local" ]] || die "IPSW download failed — file not found: $_ipsw_local"
    state_progress "00-download-ipsw" "Download complete" "100" ""
  fi
  IPSW_PATH="$_ipsw_local"
fi

# Guard: refuse to use a partially-downloaded IPSW (aria2c control file present).
if [[ -f "${IPSW_PATH}.aria2" ]]; then
  die "IPSW download still in progress (${IPSW_PATH}.aria2 exists). Wait for the download to complete before creating a VM."
fi

[[ -z "$VM_NAME" ]] && die "--vm NAME is required"

# ── Init ──────────────────────────────────────────────────────────────────────

init_log_dir "$STAGE_ID" "$VM_NAME"
state_stage_start "$STAGE_ID" "Create base VM" "Initializing..." "$$" \
  "{\"vm\":\"$VM_NAME\",\"ipsw\":\"$IPSW_PATH\",\"cpu\":$CPU,\"memory\":\"$MEMORY\",\"disk\":\"$DISK\",\"display\":\"$DISPLAY_RES\"}"

trap 'state_stage_fail "$STAGE_ID" "interrupted"' INT TERM

# ── Run ───────────────────────────────────────────────────────────────────────

step "Phase 1: Create VM '$VM_NAME'"
log "  IPSW:    $IPSW_PATH"
log "  CPU:     $CPU  Memory: $MEMORY  Disk: $DISK  Display: $DISPLAY_RES"
log "  Log dir: $LOG_DIR"

if vm_exists "$VM_NAME"; then
  die "VM '$VM_NAME' already exists. Delete it first: lume delete $VM_NAME"
fi

log "  + lume create $VM_NAME ..."
state_progress "$STAGE_ID" "Running lume create (installs macOS, 5–15 min)..." "" "$$"
lume create "$VM_NAME" \
  --ipsw "$IPSW_PATH" \
  --cpu "$CPU" \
  --memory "$MEMORY" \
  --disk-size "$DISK" \
  --display "$DISPLAY_RES" \
  2>&1 | tee -a "$_LOG_FH"

state_stage_done "$STAGE_ID"
log "Phase 1 complete — VM '$VM_NAME' created (stopped)."
log "State: $STATE_FILE"
