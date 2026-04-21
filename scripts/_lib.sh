#!/usr/bin/env bash
# _lib.sh — shared library for macOS UI-test VM build scripts.
# Source this file at the top of each stage script:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "$SCRIPT_DIR/_lib.sh"
#
# Provides: logging, prerequisite checks, lume helpers, VNC recording, SSH helpers, state JSON tracking.

# ── Defaults (override before sourcing or via CLI flags) ──────────────────────

: "${VM_USER:=lume}"
: "${VM_PASS:=lume}"
: "${VMSHARE:=$HOME/VMShare}"
: "${LOG_BASE:=$HOME/Developer/virfield/logs}"
: "${STATE_DIR:=$HOME/Developer/virfield/state}"
: "${RECORDINGS_DIR:=$HOME/Developer/virfield/recordings}"
: "${RECORD:=false}"
: "${VIEWER:=false}"
: "${RECORD_OUTPUT:=}"
: "${LOG_DIR:=}"       # set by caller; auto-set by init_log_dir if empty
: "${STATE_FILE:=}"    # set by caller or init_log_dir

# ── Logging ───────────────────────────────────────────────────────────────────

_LOG_FH=""

init_log_dir() {
  local stage="$1" vm="$2"
  # LOG_DIR is stable per VM — all runs share one folder, recorded in the state JSON.
  if [[ -z "$LOG_DIR" ]]; then
    LOG_DIR="$LOG_BASE/$vm"
  fi
  mkdir -p "$LOG_DIR" "$STATE_DIR" "$RECORDINGS_DIR"
  if [[ -z "$STATE_FILE" ]]; then
    # State file is keyed by VM name so it is always findable without knowing the log dir.
    STATE_FILE="$STATE_DIR/${vm}.json"
  fi
  # Each run gets its own timestamped log file inside LOG_DIR.
  local ts; ts="$(date +%Y%m%d-%H%M%S)"
  _LOG_FH="$LOG_DIR/${ts}-${stage}.log"
  # latest.log → current run's file for easy tailing
  ln -sfn "$(basename "$_LOG_FH")" "$LOG_DIR/latest.log" 2>/dev/null || true
}

log()  {
  local ts; ts="$(date '+%Y-%m-%dT%H:%M:%S')"
  local line="[$ts] $*"
  echo "$line"
  # Use 'if' not '&&' — '[[ -n "" ]] && ...' exits 1 under set -e when _LOG_FH is empty.
  if [[ -n "$_LOG_FH" ]]; then
    echo "$line" >> "$_LOG_FH"
  fi
}
die()  { log "FATAL: $*"; exit 1; }
step() {
  log ""
  log "════════════════════════════════════════"
  log "  $*"
  log "════════════════════════════════════════"
}

# ── Prerequisite checks ───────────────────────────────────────────────────────
# check_prerequisites [--install]
#   Without --install: prints missing tools and exits with an error.
#   With    --install: installs missing tools via Homebrew (brew must be in PATH).
#
# Checked tools:
#   lume      — VM lifecycle (required by all phases)
#   python3   — VNC key injection + state JSON (required by all phases)
#   ffmpeg    — VNC recording (required only when --record is set)
#   openssl   — OpenSSL 3 with legacy provider, for VNC DES auth (Phase 3)

check_prerequisites() {
  local install=false
  [[ "${1:-}" == "--install" ]] && install=true

  local missing=()

  command -v lume    &>/dev/null || missing+=("lume")
  command -v python3 &>/dev/null || missing+=("python3")

  if [[ "$RECORD" == "true" ]]; then
    command -v ffmpeg &>/dev/null || missing+=("ffmpeg")
  fi

  # openssl with legacy provider is required for VNC DES auth in Phase 3.
  # The system openssl on macOS does not ship the legacy provider;
  # Homebrew openssl@3 does.
  if ! echo "" | openssl enc -des-ecb -provider legacy -provider default \
       -pass pass:test -nosalt 2>/dev/null >/dev/null; then
    missing+=("openssl-with-legacy-provider")
  fi

  [[ ${#missing[@]} -eq 0 ]] && return 0

  log "Missing prerequisites: ${missing[*]}"
  if $install; then
    _install_prerequisites "${missing[@]}"
  else
    log "Run the orchestrator with --install-missing to install automatically."
    die "Prerequisites not met."
  fi
}

_install_prerequisites() {
  local brew_pkgs=()
  for p in "$@"; do
    case "$p" in
      lume)                         brew_pkgs+=("trycua/tap/lume") ;;
      python3)                      brew_pkgs+=("python3") ;;
      ffmpeg)                       brew_pkgs+=("ffmpeg") ;;
      openssl-with-legacy-provider) brew_pkgs+=("openssl@3") ;;
    esac
  done
  if [[ ${#brew_pkgs[@]} -gt 0 ]]; then
    log "Installing via Homebrew: ${brew_pkgs[*]}"
    brew install "${brew_pkgs[@]}" 2>&1 | tee -a "${_LOG_FH:-/dev/stderr}"
  fi
}

# ── State JSON ────────────────────────────────────────────────────────────────
# Stored at STATE_DIR/<vm-name>.json — keyed by VM name so a frontend can
# always locate it without knowing which log run produced the VM.
#
# Schema:
# {
#   "vm":       "<vm-name>",
#   "status":   "running|done|failed",
#   "stage":    "<human-readable current stage label, or null>",
#   "substage": "<human-readable current activity, or null>",
#   "percent":  <0-100 for stages that track progress, or null>,
#   "pid":      <PID to kill to stop the current activity, or null>,
#   "updated":  "<ISO8601>",
#   "log":      "<path to build log dir>",
#   "recordings": ["<path>", ...],
#   "hostname": "<vm.local or null>",
#   "ip":       "<vm-ip or null>",
#   "stages": {
#     "00-download-ipsw": {
#       "status": "done|running|failed|pending|skipped",
#       "label":  "Download macOS IPSW",
#       "started":  "<ISO8601>",
#       "finished": "<ISO8601>"
#     },
#     "01-create-vm":       { "status": ..., "label": "Create base VM",        ... },
#     "02-setup-assistant": { "status": ..., "label": "Setup Assistant",        ... },
#     "03-disable-sip":     { "status": ..., "label": "Disable SIP",            ... },
#     "04-provision-vm":    { "status": ..., "label": "Install Xcode & tools",  ... }
#   }
# }

_state_ts() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }

# _state_read FILE — read JSON from FILE, return {} on any error (corrupt/race).
# _state_write FILE DATA — atomic write via temp file + mv.
# Used by all state functions to avoid crashes from concurrent writes.
_STATE_PY_HELPERS='
import json, sys, os, tempfile

def state_read(path):
    try:
        return json.loads(open(path).read())
    except Exception:
        return {}

def state_write(path, data):
    d = os.path.dirname(path)
    fd, tmp = tempfile.mkstemp(dir=d or ".", prefix=".state-tmp-")
    try:
        os.write(fd, json.dumps(data, indent=2).encode())
        os.close(fd)
        os.replace(tmp, path)
    except Exception:
        try: os.unlink(tmp)
        except: pass
        raise
'

# state_stage_start STAGE_ID LABEL [SUBSTAGE] [PID] [OPTIONS_JSON]
# Mark a stage as running; sets top-level stage/substage/pid for the UI.
state_stage_start() {
  local stage_id="$1"
  local label="${2:-$stage_id}"
  local substage="${3:-}"
  local pid="${4:-}"
  local options_json="${5:-{}}"
  [[ -z "$STATE_FILE" ]] && return
  local ts; ts="$(_state_ts)"
  python3 - "$STATE_FILE" "$stage_id" "$label" "$substage" "$pid" "$ts" "$options_json" "$LOG_DIR" <<EOF
$_STATE_PY_HELPERS
state_file, stage_id, label, substage, pid, ts, options_json, log_dir = sys.argv[1:]
d = state_read(state_file)
try: opts = json.loads(options_json)
except Exception: opts = {}
d.setdefault('stages', {})[stage_id] = {
    'status': 'running', 'label': label, 'started': ts, 'options': opts,
}
d['stage']    = label
d['substage'] = substage or None
d['pid']      = int(pid) if pid else None
d['percent']  = None
d['status']   = 'running'
d['updated']  = ts
d['log']      = log_dir
d.setdefault('recordings', [])
state_write(state_file, d)
EOF
}

# state_progress STAGE_ID SUBSTAGE [PERCENT] [PID]
# Update the current activity description mid-stage. Call as often as useful.
state_progress() {
  local stage_id="$1"
  local substage="${2:-}"
  local percent="${3:-}"
  local pid="${4:-}"
  [[ -z "$STATE_FILE" ]] && return
  local ts; ts="$(_state_ts)"
  python3 - "$STATE_FILE" "$stage_id" "$substage" "$percent" "$pid" "$ts" <<EOF
$_STATE_PY_HELPERS
state_file, stage_id, substage, percent, pid, ts = sys.argv[1:]
d = state_read(state_file)
if substage: d['substage'] = substage
d['updated'] = ts
if percent != '': d['percent'] = int(float(percent))
if pid != '': d['pid'] = int(pid)
s = d.setdefault('stages', {}).setdefault(stage_id, {})
if substage: s['substage'] = substage
s['updated'] = ts
state_write(state_file, d)
EOF
}

state_stage_done() {
  local stage_id="$1"
  [[ -z "$STATE_FILE" ]] && return
  local ts; ts="$(_state_ts)"
  python3 - "$STATE_FILE" "$stage_id" "$ts" <<EOF
$_STATE_PY_HELPERS
state_file, stage_id, ts = sys.argv[1:]
d = state_read(state_file)
d.setdefault('stages', {}).setdefault(stage_id, {}).update({'status': 'done', 'finished': ts})
d['status'] = 'done'; d['substage'] = None; d['pid'] = None; d['percent'] = None
d['updated'] = ts
state_write(state_file, d)
EOF
}

state_stage_fail() {
  local stage_id="$1" reason="${2:-}"
  [[ -z "$STATE_FILE" ]] && return
  local ts; ts="$(_state_ts)"
  python3 - "$STATE_FILE" "$stage_id" "$ts" "$reason" <<EOF
$_STATE_PY_HELPERS
state_file, stage_id, ts, reason = sys.argv[1:]
d = state_read(state_file)
d.setdefault('stages', {}).setdefault(stage_id, {}).update(
    {'status': 'failed', 'finished': ts, 'error': reason})
d['status'] = 'failed'; d['substage'] = None; d['pid'] = None; d['percent'] = None
d['updated'] = ts
state_write(state_file, d)
EOF
}

state_add_recording() {
  # Appends a recording path to the recordings array (multiple phases may record).
  local path="$1"
  [[ -z "$STATE_FILE" ]] && return
  python3 - "$STATE_FILE" "$path" <<EOF
$_STATE_PY_HELPERS
state_file, path = sys.argv[1:]
d = state_read(state_file)
recs = d.get('recordings', [])
if path not in recs:
    recs.append(path)
d['recordings'] = recs
state_write(state_file, d)
EOF
}

state_set_vm_info() {
  # Call after SSH is available to record hostname and IP into state.json.
  local vm="$1"
  [[ -z "$STATE_FILE" ]] && return
  local hostname ip
  hostname="$(lume ssh "$vm" --timeout 10 'scutil --get LocalHostName 2>/dev/null || hostname' 2>/dev/null | tr -d '\r\n' || true)"
  ip="$(lume get "$vm" --format json 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
e=d[0] if isinstance(d,list) else d
print(e.get('ipAddress') or e.get('ip') or '')
" 2>/dev/null | tr -d '\r\n' || true)"
  python3 - "$STATE_FILE" "$hostname" "$ip" <<EOF
$_STATE_PY_HELPERS
state_file, hostname, ip = sys.argv[1:]
d = state_read(state_file)
if hostname: d['hostname'] = hostname + ('.local' if not hostname.endswith('.local') else '')
if ip:       d['ip'] = ip
state_write(state_file, d)
EOF
}

state_finalize() {
  local status="$1"   # done | failed
  [[ -z "$STATE_FILE" ]] && return
  local ts; ts="$(_state_ts)"
  python3 - "$STATE_FILE" "$status" "$ts" <<EOF
$_STATE_PY_HELPERS
state_file, status, ts = sys.argv[1:]
d = state_read(state_file)
d['status']   = status
d['substage'] = None
d['pid']      = None
d['percent']  = None
d['updated']  = ts
state_write(state_file, d)
EOF
}

# ── IPSW / macOS version helpers ─────────────────────────────────────────────

macos_version_from_ipsw() {
  # Extract macOS version string from an IPSW filename or path.
  # UniversalMac_15.4_24E5238a_Restore.ipsw → "15.4"
  # Returns empty string if not parseable.
  local ipsw="$1"
  basename "$ipsw" .ipsw | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true
}

# Friendly name → major version number mapping (bash 3.2-compatible; no declare -A).
_ipsw_major_for_spec() {
  # Resolve a version spec to a major version number, or return empty string.
  # Accepts: named alias (sequoia/tahoe/…), major number (15/26), partial version (15.6).
  local spec
  spec="$(echo "$1" | tr '[:upper:]' '[:lower:]')"
  [[ -z "$spec" ]] && return
  # Named alias — case statement replaces associative array (bash 3.2 compat)
  case "$spec" in
    tahoe)            echo 26; return ;;
    sequoia|15)       echo 15; return ;;
    sonoma|14)        echo 14; return ;;
    ventura|13)       echo 13; return ;;
    monterey|12)      echo 12; return ;;
    big_sur|bigsur|11) echo 11; return ;;
  esac
  # Bare major number or version string — extract leading digits
  local major; major="$(echo "$spec" | grep -oE '^[0-9]+' || true)"
  [[ -n "$major" ]] && echo "$major"
}

# Return the friendly name for a major version number (e.g. 15 → sequoia).
_ipsw_name_for_major() {
  case "$1" in
    26) echo tahoe ;;
    15) echo sequoia ;;
    14) echo sonoma ;;
    13) echo ventura ;;
    12) echo monterey ;;
    11) echo big_sur ;;
    *)  echo "$1" ;;
  esac
}

_ipsw_is_version_spec() {
  # Returns 0 if $1 looks like a version spec (not a file path).
  local s="$1"
  # Explicit file paths and IPSW filenames are not version specs
  [[ "$s" == /* || "$s" == ~* || "$s" == ./* || "$s" == *.ipsw ]] && return 1
  [[ "$s" == "latest" ]] && return 0
  [[ -n "$(_ipsw_major_for_spec "$s")" ]] && return 0
  return 1
}

# Fetch the IPSW URL for a named macOS version from the ipsw.me catalog.
# Usage: resolve_ipsw_version "sequoia"   → prints URL to stdout
#        resolve_ipsw_version "15"        → same
#        resolve_ipsw_version "15.6.1"    → exact version match
# Returns 1 on failure.
resolve_ipsw_version() {
  local spec="$1"
  local major; major="$(_ipsw_major_for_spec "$spec")"
  [[ -z "$major" ]] && { echo "Unknown macOS version spec: $spec" >&2; return 1; }

  local json
  json="$(curl -fsSL "https://api.ipsw.me/v4/device/VirtualMac2,1" 2>/dev/null)" \
    || { echo "Failed to fetch IPSW catalog from api.ipsw.me" >&2; return 1; }

  local url
  url="$(echo "$json" | python3 -c "
import json, sys
data = json.load(sys.stdin)
major = '$major'
spec  = '${spec,,}'
fws = data.get('firmwares', [])
# Prefer exact version match, then latest patch for the major
exact = [f for f in fws if f.get('version','') == spec]
if exact:
    print(exact[0]['url']); sys.exit()
major_fws = [f for f in fws if f.get('version','').split('.')[0] == major]
major_fws.sort(key=lambda f: [int(x) for x in f['version'].split('.')], reverse=True)
if major_fws:
    print(major_fws[0]['url'])
" 2>/dev/null || true)"

  [[ -z "$url" ]] && { echo "No IPSW found in catalog for macOS $major (spec: $spec)" >&2; return 1; }
  echo "$url"
}

# Print the ipsw.me catalog for VirtualMac2,1 — one entry per major version.
list_ipsw_catalog() {
  local json
  json="$(curl -fsSL "https://api.ipsw.me/v4/device/VirtualMac2,1" 2>/dev/null)" \
    || { echo "  (failed to fetch catalog from api.ipsw.me)" >&2; return 1; }

  echo "$json" | python3 -c "
import json, sys
data = json.load(sys.stdin)
fws = data.get('firmwares', [])
fws.sort(key=lambda f: [int(x) for x in f.get('version','0').split('.')], reverse=True)
seen = {}
for f in fws:
    ver   = f.get('version','')
    major = ver.split('.')[0]
    if major not in seen:
        seen[major] = f
names = {
    '26':'tahoe','15':'sequoia','14':'sonoma',
    '13':'ventura','12':'monterey','11':'big_sur',
}
for major, f in sorted(seen.items(), key=lambda kv: int(kv[0]), reverse=True):
    ver   = f.get('version','?')
    build = f.get('buildid','')
    size  = f.get('filesize',0)
    size_s = f'{size/1024**3:.1f} GB' if size else '?'
    name  = names.get(major, '')
    alias = f'  ({name})' if name else ''
    print(f'  macOS {major:3s}  {ver:10s}  {build:12s}  {size_s:8s}{alias}')
" 2>/dev/null
}

# ── lume helpers ──────────────────────────────────────────────────────────────

lume_vnc_info() {
  # Print "host port password" for the named VM (one per line). Returns empty if not running.
  local vm="$1"
  lume get "$vm" --format json 2>/dev/null | python3 -c '
import sys, json, re
d = json.load(sys.stdin)
entry = d[0] if isinstance(d, list) else d
vnc = entry.get("vncUrl") or ""
m = re.match(r"vnc://:([^@]*)@([^:]+):(\d+)", vnc)
if m:
    print(m.group(2)); print(m.group(3)); print(m.group(1))
' 2>/dev/null || true
}

vm_exists() {
  lume get "$1" --format json 2>/dev/null | \
    python3 -c "import sys,json; d=json.load(sys.stdin); sys.exit(0 if d else 1)" 2>/dev/null
}

lume_kill_orphans() {
  # Kill any processes holding the VM's auxiliary storage (nvram.bin) lock,
  # including orphaned "lume run <vm>" processes and the Virtualization.framework
  # XPC service that macOS keeps alive after lume exits.
  local vm="$1"
  local nvram="$HOME/.lume/${vm}/nvram.bin"
  local killed=false

  # 1. Graceful lume stop first (no-op if already stopped).
  # lume stop prints INFO messages to stdout when the VM is running — suppress
  # both stdout and stderr so they don't leak into $(lume_run_bg) subshell captures.
  lume stop "$vm" &>/dev/null || true

  # 2. Kill orphaned "lume run <vm>" processes
  local lume_pids
  lume_pids="$(pgrep -f "lume run ${vm}( |$)" 2>/dev/null || true)"
  if [[ -n "$lume_pids" ]]; then
    log "  Killing orphaned lume run processes for '$vm': $lume_pids" >&2
    echo "$lume_pids" | xargs kill 2>/dev/null || true
    killed=true
  fi

  # 3. Kill any Virtualization.framework XPC service holding nvram.bin open
  if [[ -f "$nvram" ]]; then
    local xpc_pids
    xpc_pids="$(lsof "$nvram" 2>/dev/null | awk 'NR>1 {print $2}' | sort -u || true)"
    if [[ -n "$xpc_pids" ]]; then
      log "  Killing Virtualization XPC processes holding nvram.bin: $xpc_pids" >&2
      echo "$xpc_pids" | xargs kill 2>/dev/null || true
      killed=true
    fi
  fi

  # Give macOS a moment to release the auxiliary storage lock
  $killed && sleep 2 || true
}

lume_run_bg() {
  # Run lume in the background and echo only the PID to stdout.
  # All log output goes to stderr so callers using $() capture only the PID.
  local vm="$1"; shift
  lume_kill_orphans "$vm"
  log "  + lume run $vm $*" >&2
  nohup lume run "$vm" "$@" >> "${_LOG_FH:-/dev/null}" 2>&1 &
  echo $!
}

lume_stop_vm() {
  local vm="$1" pid="${2:-}"
  log "  + lume stop $vm"
  lume stop "$vm" >> "${_LOG_FH:-/dev/stderr}" 2>&1 || true
  [[ -n "$pid" ]] && { kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }
  # lume stop is async — the VirtualMachine process may still be alive for a few seconds.
  # Poll until lume confirms the VM is stopped (up to 30s) so callers see consistent state.
  local deadline=$(( $(date +%s) + 30 ))
  while (( $(date +%s) < deadline )); do
    local vm_status
    vm_status="$(lume get "$vm" --format json 2>/dev/null \
      | python3 -c "import sys,json; d=json.load(sys.stdin); e=d[0] if isinstance(d,list) else d; print(e.get('status',''))" \
      2>/dev/null || echo '')"
    [[ "$vm_status" == "stopped" || -z "$vm_status" ]] && return 0
    sleep 2
  done
  log "  Warning: VM '$vm' did not confirm stopped within 30s"
}

lume_clone() {
  local src="$1" dst="$2"
  log "  + lume clone $src → $dst"
  lume clone "$src" "$dst" 2>&1 | tee -a "$_LOG_FH"
}

wait_ssh() {
  local vm="$1" timeout="${2:-180}"
  log "  Waiting for SSH on $vm (up to ${timeout}s)..."
  local deadline=$(( $(date +%s) + timeout ))
  while (( $(date +%s) < deadline )); do
    if lume ssh "$vm" --timeout 5 'echo ssh_ok' 2>/dev/null | grep -q ssh_ok; then
      # Wait for macOS to fully settle — SSH can become available before the
      # system finishes initialization, causing subsequent commands to fail.
      log "  SSH ready (waiting 10s for system to settle...)"
      sleep 10
      log "  SSH stable"
      return 0
    fi
    sleep 5
  done
  die "SSH not available on $vm after ${timeout}s"
}

lume_ssh() {
  local vm="$1"; shift
  lume ssh "$vm" "$@" 2>&1 | tee -a "$_LOG_FH"
}

# ── VNC recording ─────────────────────────────────────────────────────────────

_REC_PID=""
_REC_OUT=""

start_recorder() {
  local vm="$1"
  local stage="${2:-}"          # optional stage name, e.g. "setup-assistant"
  if [[ "$RECORD" != "true" ]]; then return; fi
  local out
  if [[ -n "$RECORD_OUTPUT" ]]; then
    out="$RECORD_OUTPUT"
  else
    local ts; ts="$(date +%Y%m%d-%H%M%S)"
    if [[ -n "$stage" ]]; then
      out="$RECORDINGS_DIR/${ts}-${vm}-${stage}-recording.mp4"
    else
      out="$RECORDINGS_DIR/${ts}-${vm}-recording.mp4"
    fi
  fi
  _REC_OUT="$out"
  log "  Starting recorder → $out"
  python3 "$SCRIPT_DIR/vnc-record.py" "$vm" "$out" \
    >> "$_LOG_FH" 2>&1 &
  _REC_PID=$!
  state_add_recording "$out"
  sleep 2   # give recorder time to connect before sending keys
}

stop_recorder() {
  if [[ -z "$_REC_PID" ]] || ! kill -0 "$_REC_PID" 2>/dev/null; then return; fi
  log "  Stopping recorder (PID $_REC_PID)..."
  kill "$_REC_PID" 2>/dev/null || true
  wait "$_REC_PID" 2>/dev/null || true
  _REC_PID=""
  log "  Recording saved → $_REC_OUT"
}

# ── VNC viewer ────────────────────────────────────────────────────────────────

open_vnc_viewer() {
  local vm="$1"
  if [[ "$VIEWER" != "true" ]]; then return; fi
  local info; info="$(lume_vnc_info "$vm")"
  local host port pass
  host="$(echo "$info" | sed -n '1p')"
  port="$(echo "$info" | sed -n '2p')"
  pass="$(echo "$info" | sed -n '3p')"
  if [[ -n "$port" ]]; then
    local url="vnc://:${pass}@${host}:${port}"
    log "  Opening VNC viewer: $url"
    open "$url" || true
  else
    log "  WARNING: no VNC URL available yet for $vm"
  fi
}

# ── Common arg-parsing helpers ────────────────────────────────────────────────
# Each script calls parse_common_arg "$1" "${2:-}" in its while loop.
# Sets _NARGS to the number of argv slots consumed (1 for flags, 2 for value args).
# Returns 0 if consumed, 1 if unknown (script should handle or error).
#
# Usage pattern in each script:
#   *) if parse_common_arg "$1" "${2:-}"; then shift "$_NARGS"
#      else echo "Unknown option: $1" >&2; usage; fi ;;

_NARGS=0
parse_common_arg() {
  _NARGS=0
  case "$1" in
    --log-dir)   LOG_DIR="$2";         _NARGS=2; return 0 ;;
    --record)    RECORD="true";         _NARGS=1; return 0 ;;
    --no-record) RECORD="false";        _NARGS=1; return 0 ;;
    --output)    RECORD_OUTPUT="$2";    _NARGS=2; return 0 ;;
    --viewer)    VIEWER="true";         _NARGS=1; return 0 ;;
    --no-viewer) VIEWER="false";        _NARGS=1; return 0 ;;
    --vm-user)   VM_USER="$2";          _NARGS=2; return 0 ;;
    --vm-pass)   VM_PASS="$2";          _NARGS=2; return 0 ;;
    --vmshare)   VMSHARE="$2";          _NARGS=2; return 0 ;;
  esac
  return 1
}

COMMON_OPTIONS_HELP="
Common options:
  --log-dir DIR          Log directory (default: ~/Developer/virfield/logs/<timestamp>-<stage>-<vm>)
  --record               Record VNC framebuffer to video (default: off)
  --no-record            Disable recording (default)
  --output PATH          Recording output path (default: ~/Developer/virfield/recordings/<timestamp>-<vm>-recording.mp4)
  --viewer               Open VNC session in system viewer (default: off)
  --vm-user NAME         VM macOS username (default: lume)
  --vm-pass PASS         VM macOS password (default: lume)
  --vmshare DIR          Host virtiofs share directory (default: ~/VMShare)"
