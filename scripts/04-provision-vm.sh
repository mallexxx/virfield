#!/usr/bin/env bash
# 04-provision-vm.sh — Phase 4: SSH provisioning + Xcode install.
#
# Clones the nosip VM, boots it, runs vm-setup.sh via SSH,
# installs Xcode, smoke-tests, and stops the VM.
#
# Usage:
#   04-provision-vm.sh [--source-vm NAME] [--vm NAME] [options]
#
# Run standalone or via build-golden-vm.sh.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_lib.sh"

STAGE_ID="04-provision-vm"

# ── Defaults ──────────────────────────────────────────────────────────────────

VM_NAME=""
XCODE_PATH=""
TOOLS="all"

# ── Help ──────────────────────────────────────────────────────────────────────

usage() {
  cat <<EOF
04-provision-vm.sh — Phase 4: SSH provisioning + Xcode install

Installs all tools via SSH (vm-setup.sh), optionally installs Xcode,
smoke-tests, and stops the VM. Operates on the VM in-place — no cloning.

Usage:
  04-provision-vm.sh --vm NAME [options]

Options:
  --vm NAME          VM to provision (required)
  --xcode PATH       Path to Xcode.app or Xcode.xip.
                     Skip Xcode install if omitted.
  --tools LIST       Comma-separated tool IDs to install (default: all).
                     Available: system,autologin,ssh_key,homebrew,screenresolution,
                     xcbeautify,jq,socat,peekaboo,logging,peekaboo_agent,tcc,automation
                     Optional (not in default): add your own custom tool groups here
$COMMON_OPTIONS_HELP

Examples:
  04-provision-vm.sh --vm uitest-26.4.1-golden --xcode ~/VMShare/Xcode.app
  04-provision-vm.sh --vm macos-15-golden --xcode ~/VMShare/Xcode.app
  04-provision-vm.sh --vm my-golden --tools system,homebrew
EOF
  exit 0
}

# ── Arg parsing ───────────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)    usage ;;
    --source-vm)  shift 2 ;;  # ignored, kept for orchestrator compat
    --vm)         VM_NAME="$2"; shift 2 ;;
    --xcode)      XCODE_PATH="$2"; shift 2 ;;
    --tools)      TOOLS="$2"; shift 2 ;;
    --skip-clone) shift ;;    # ignored, no cloning
    *) if parse_common_arg "$1" "${2:-}"; then shift "$_NARGS"
       else echo "Unknown option: $1" >&2; usage; fi ;;
  esac
done

# ── Init ──────────────────────────────────────────────────────────────────────

[[ -z "$VM_NAME" ]] && { echo "Error: --vm NAME is required" >&2; usage; }
init_log_dir "$STAGE_ID" "$VM_NAME"

# Prevent concurrent runs for the same VM using a lock directory.
LOCK_DIR="/tmp/virfield-provision-${VM_NAME}.lock"
# Plain-file lock = stale legacy flock artifact; remove it so mkdir can proceed.
[[ -f "$LOCK_DIR" && ! -d "$LOCK_DIR" ]] && rm -f "$LOCK_DIR"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  LOCK_PID="$(cat "$LOCK_DIR/pid" 2>/dev/null || echo '?')"
  if [[ "$LOCK_PID" == "?" ]] || ! kill -0 "$LOCK_PID" 2>/dev/null; then
    # Stale lock (no pid file or dead process) — clear it
    rm -rf "$LOCK_DIR"
    mkdir "$LOCK_DIR"
  else
    die "Another instance of $STAGE_ID is already running for '$VM_NAME' (PID: $LOCK_PID). Aborting."
  fi
fi
echo "$$" > "$LOCK_DIR/pid"

state_stage_start "$STAGE_ID" "Install Xcode & tools" "Starting..." "$$" \
  "{\"vm\":\"$VM_NAME\",\"xcode\":\"$XCODE_PATH\",\"tools\":\"$TOOLS\"}"

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

step "Phase 4: Provision '$VM_NAME'"
log "  Tools:  $TOOLS"
log "  Xcode:  ${XCODE_PATH:-(skip)}"
log "  Log dir: $LOG_DIR"

vm_exists "$VM_NAME" || die "VM '$VM_NAME' not found."

log "  Booting '$VM_NAME'..."
LUME_RUN_PID="$(lume_run_bg "$VM_NAME" --shared-dir "$VMSHARE" --no-display)"
state_progress "$STAGE_ID" "Booting VM..." "" "$LUME_RUN_PID"
state_progress "$STAGE_ID" "Waiting for SSH..." "" "$LUME_RUN_PID"
wait_ssh "$VM_NAME" 300
state_set_vm_info "$VM_NAME"

# ── Bootstrap: NOPASSWD sudo + host SSH key ───────────────────────────────────

state_progress "$STAGE_ID" "Bootstrapping sudo..." "" "$LUME_RUN_PID"
log "  Bootstrapping NOPASSWD sudo..."
lume_ssh "$VM_NAME" \
  "echo $VM_PASS | sudo -S bash -c 'echo \"$VM_USER ALL=(ALL) NOPASSWD:ALL\" > /etc/sudoers.d/lume-nopasswd && chmod 440 /etc/sudoers.d/lume-nopasswd && echo sudoers_done'"

HOST_PUBKEY="$(cat ~/.ssh/id_ed25519.pub 2>/dev/null || cat ~/.ssh/id_rsa.pub 2>/dev/null || echo "")"
if [[ -n "$HOST_PUBKEY" ]]; then
  log "  Installing host SSH public key..."
  lume_ssh "$VM_NAME" \
    "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '$HOST_PUBKEY' >> ~/.ssh/authorized_keys && sort -u ~/.ssh/authorized_keys -o ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && echo key_added"
fi

# ── Upload + run vm-setup.sh ──────────────────────────────────────────────────

state_progress "$STAGE_ID" "Uploading setup scripts..." "" "$LUME_RUN_PID"
log "  Uploading vm-setup.sh and supporting files (via VMShare)..."
# lume ssh does not support stdin redirection — copy via shared dir instead.
cp "$SCRIPT_DIR/vm-setup.sh" "$VMSHARE/vm-setup.sh"
cp "$SCRIPT_DIR/com.apple.system.logging.plist" "$VMSHARE/com.apple.system.logging.plist"
lume_ssh "$VM_NAME" \
  "cp '/Volumes/My Shared Files/vm-setup.sh' /tmp/vm-setup.sh && chmod +x /tmp/vm-setup.sh && echo upload_ok"
lume_ssh "$VM_NAME" \
  "cp '/Volumes/My Shared Files/com.apple.system.logging.plist' /tmp/com.apple.system.logging.plist && echo plist_ok"

state_progress "$STAGE_ID" "Running vm-setup.sh (tools: $TOOLS)..." "" "$LUME_RUN_PID"
log "  Running vm-setup.sh (tools: $TOOLS, timeout: 15 min)..."
VM_IP="$(lume get "$VM_NAME" --format json 2>/dev/null | python3 -c \
  "import sys,json; d=json.load(sys.stdin); e=d[0] if isinstance(d,list) else d; print(e.get('ipAddress',''))" 2>/dev/null | tr -d '\r\n')"
if [[ -n "$VM_IP" ]]; then
  log "  SSH direct to $VM_IP for streaming output..."
  ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      -o ConnectTimeout=10 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 \
    "${VM_USER}@${VM_IP}" "TOOLS='$TOOLS' bash /tmp/vm-setup.sh" 2>&1 | tee -a "$_LOG_FH"
else
  log "  WARNING: no VM IP — falling back to lume ssh (output may be buffered)..."
  lume ssh "$VM_NAME" --timeout 900 "TOOLS='$TOOLS' bash /tmp/vm-setup.sh" 2>&1 | tee -a "$_LOG_FH"
fi

# ── Xcode install ─────────────────────────────────────────────────────────────

if [[ -n "$XCODE_PATH" ]]; then
  state_progress "$STAGE_ID" "Installing Xcode (may take 30+ min)..." "" "$LUME_RUN_PID"
  log "  Installing Xcode from: $XCODE_PATH (timeout: 60 min)"

  # Write the install script to VMShare to avoid all nested-quoting issues
  # (same pattern as smoke-test.sh). Use direct SSH with generous ServerAlive
  # settings so the connection holds through the large ditto copy.
  xcode_name="$(basename "$XCODE_PATH")"
  if [[ "$XCODE_PATH" == *.xip ]]; then
    cat > "$VMSHARE/xcode-install.sh" << XCODE_EOF
#!/bin/bash
set -euo pipefail
XIP="/Volumes/My Shared Files/${xcode_name}"
[[ -f "\$XIP" ]] || { echo "ERROR: ${xcode_name} not found in VMShare"; exit 1; }
echo "Extracting \$XIP..."
cd /Applications && xip -x "\$XIP"
sudo xcode-select -s /Applications/Xcode.app
sudo xcodebuild -license accept
sudo xcodebuild -runFirstLaunch
echo "Installed: \$(xcodebuild -version | head -1)"
XCODE_EOF
  elif [[ "$XCODE_PATH" == *.app ]]; then
    cat > "$VMSHARE/xcode-install.sh" << XCODE_EOF
#!/bin/bash
set -euo pipefail
SRC="/Volumes/My Shared Files/${xcode_name}"
[[ -d "\$SRC" ]] || { echo "ERROR: ${xcode_name} not found in VMShare"; exit 1; }
# Always remove any existing Xcode.app: a partial copy from a failed previous
# run looks valid but breaks git (license prompts) and other tools.
if [[ -d /Applications/Xcode.app ]]; then
  echo "Removing existing Xcode.app for a clean install..."
  sudo rm -rf /Applications/Xcode.app
  sudo xcode-select --reset
fi
# Use ditto: cp -a fails on VirtioFS/VMShare when copying xattrs through
# Xcode's git-core deep symlink chains. ditto handles app bundles correctly.
echo "Copying Xcode.app via ditto (this may take 10-30 min)..."
ditto "\$SRC" /Applications/Xcode.app
sudo xcode-select -s /Applications/Xcode.app
sudo xcodebuild -license accept
sudo xcodebuild -runFirstLaunch
echo "Installed: \$(xcodebuild -version | head -1)"
XCODE_EOF
  else
    log "  WARNING: unrecognised Xcode path format (expected .app or .xip): $XCODE_PATH"
    XCODE_PATH=""
  fi

  if [[ -n "$XCODE_PATH" ]]; then
    # Remove any existing (possibly broken) Xcode.app on the VM first.
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=10 \
      "${VM_USER}@${VM_IP}" \
      "[[ -d /Applications/Xcode.app ]] && { sudo rm -rf /Applications/Xcode.app; sudo xcode-select --reset; echo 'Removed old Xcode.app'; } || true" \
      2>&1 | tee -a "$_LOG_FH"

    # rsync from HOST → VM directly, bypassing VMShare.
    # VirtioFS (VMShare) can't resolve Xcode's deep git-core symlink chains;
    # rsync over SSH reads symlinks on the host where they work, and creates
    # them on the VM filesystem where they also resolve correctly.
    log "  rsync Xcode.app from host → VM (direct SSH, up to 60 min)..."
    rsync -rl --delete --progress \
      -e "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o ServerAliveInterval=30 -o ServerAliveCountMax=120" \
      "$XCODE_PATH/" \
      "${VM_USER}@${VM_IP}:/Applications/Xcode.app/" \
      2>&1 | tee -a "$_LOG_FH"

    log "  Accepting Xcode license and running first launch..."
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=10 -o ServerAliveInterval=30 -o ServerAliveCountMax=60 \
      "${VM_USER}@${VM_IP}" \
      "sudo xcode-select -s /Applications/Xcode.app && sudo xcodebuild -license accept && sudo xcodebuild -runFirstLaunch && xcodebuild -version" \
      2>&1 | tee -a "$_LOG_FH"
  fi
else
  log "  Skipping Xcode install (--xcode not provided)"
fi

# ── Smoke test ────────────────────────────────────────────────────────────────

state_progress "$STAGE_ID" "Smoke test..." "" "$LUME_RUN_PID"
log "  Smoke test..."
# Write smoke test to VMShare to avoid nested-quote issues with lume_ssh.
cat > "$VMSHARE/smoke-test.sh" << 'SMOKE_EOF'
#!/bin/bash
export PATH=/opt/homebrew/bin:$PATH
ok=true
check() { printf "  %-22s " "$1:"; command -v "$2" &>/dev/null && echo ok || { echo MISSING; ok=false; }; }
check brew             brew
check xcbeautify       xcbeautify
check peekaboo         peekaboo
check screenresolution screenresolution
check socat            socat
check jq               jq
printf "  %-22s " "SIP:"; csrutil status
printf "  %-22s " "sudo NOPASSWD:"; sudo whoami
$ok && echo smoke_pass || echo smoke_FAIL
SMOKE_EOF
lume_ssh "$VM_NAME" \
  "cp '/Volumes/My Shared Files/smoke-test.sh' /tmp/smoke-test.sh && chmod +x /tmp/smoke-test.sh && echo smoke_upload_ok"
VM_IP="$(lume get "$VM_NAME" --format json 2>/dev/null | python3 -c \
  "import sys,json; d=json.load(sys.stdin); e=d[0] if isinstance(d,list) else d; print(e.get('ipAddress',''))" 2>/dev/null | tr -d '\r\n')"
if [[ -n "$VM_IP" ]]; then
  log "  SSH direct to $VM_IP for smoke test output..."
  ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      -o ConnectTimeout=10 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 \
    "${VM_USER}@${VM_IP}" "bash /tmp/smoke-test.sh" 2>&1 | tee -a "$_LOG_FH"
else
  lume_ssh "$VM_NAME" "bash /tmp/smoke-test.sh"
fi

state_progress "$STAGE_ID" "Stopping VM..." "" "$LUME_RUN_PID"
log "  Stopping VM..."
lume_stop_vm "$VM_NAME" "$LUME_RUN_PID"
LUME_RUN_PID=""
rm -rf "$LOCK_DIR" 2>/dev/null || true
trap - EXIT INT TERM

state_stage_done "$STAGE_ID"
log "Phase 4 complete — '$VM_NAME' is fully provisioned and stopped."
log "State: $STATE_FILE"
