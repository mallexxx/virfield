#!/bin/bash
# vm-setup.sh — In-VM provisioning script for macOS UI-test VMs.
#
# Run inside the VM via SSH (uploaded + executed by 04-provision-vm.sh):
#   TOOLS=all HOST_PUBKEY="..." bash /tmp/vm-setup.sh
#
# Can also be run manually:
#   ssh lume@<VM_IP> TOOLS=all HOST_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" bash -s < vm-setup.sh
#
# TOOLS env var (comma-separated, default: all):
#   system            Disable Gatekeeper, sleep/screensaver/restore-windows
#   autologin         Auto-login as $VM_USER (default: lume)
#   ssh_key           Install host SSH public key (from HOST_PUBKEY env)
#   homebrew          Install Homebrew
#   screenresolution  brew install + set 1920×1080×32@60
#   xcbeautify        brew install xcbeautify
#   jq                brew install jq
#   socat             brew install socat
#   peekaboo          brew install steipete/tap/peekaboo
#   logging           Install debug-level Apple Unified Log config (bundled in scripts/)
#   peekaboo_agent    LaunchAgent: Terminal opens a socat TCP:4040 → peekaboo mcp tunnel at login
#   tcc               TCC grants for all 6 UI-test endpoints: 6 services + AppleEvents→SystemEvents
#   automation        Enable Xcode automation mode (no auth dialogs during UI test runs)
#

set -euo pipefail

TOOLS="${TOOLS:-all}"
VM_USER="${VM_USER:-lume}"
VMSHARE_MOUNT="/Volumes/My Shared Files"

echo "=== macOS UI-test VM: provisioning ==="
echo "    Tools: $TOOLS"

# ── Tool selection ────────────────────────────────────────────────────────────

want() {
  # Returns 0 if tool $1 is in the TOOLS list.
  # TOOLS=all means every default tool. "all" may appear alongside extras, e.g. "all,your_custom_tools".
  local t="$1"
  echo ",$TOOLS," | grep -q ",all," && return 0
  echo ",$TOOLS," | grep -q ",$t,"  && return 0
  return 1
}

# ── 1. System settings ────────────────────────────────────────────────────────

if want system; then
  echo "--- System settings ---"

  # Disable Gatekeeper. On macOS 15+, spctl --master-disable is MDM-gated.
  # Write to the prefs plist spctl actually reads (correct path confirmed via dtruss).
  sudo defaults write /var/db/SystemPolicyConfiguration/SystemPolicy-prefs EnableAssessment -bool false
  # Also open up the authority table: allow "No Matching Rule" (unsigned/dev-signed apps)
  # and allow unnotarized Developer ID apps — both blocked by default on macOS 15.
  sudo sqlite3 /var/db/SystemPolicyConfiguration/SystemPolicy \
    "UPDATE authority SET allow=1 WHERE label='No Matching Rule';" 2>/dev/null || true
  sudo sqlite3 /var/db/SystemPolicyConfiguration/SystemPolicy \
    "UPDATE authority SET allow=1 WHERE label='Unnotarized Developer ID';" 2>/dev/null || true
  sudo pkill -9 syspolicyd 2>/dev/null || true
  sleep 2
  echo "  Gatekeeper disabled."

  _spctl_status="$(spctl --status 2>&1 || true)"
  echo "  spctl status: $_spctl_status"

  # Disable AMFI (Apple Mobile File Integrity) enforcement so development-signed
  # apps (Apple Development cert, unnotarized) launch without provisioning profile checks.
  # Takes effect on next boot — the golden image carries this NVRAM setting.
  sudo nvram boot-args="amfi_get_out_of_my_way=1"
  echo "  AMFI: amfi_get_out_of_my_way=1 set in NVRAM (active after next boot)"

  # Disable sleep/screensaver (critical for Aqua session persistence in tests)
  sudo systemsetup -setsleep Never
  sudo systemsetup -setdisplaysleep Never
  sudo systemsetup -setharddisksleep Never
  defaults write com.apple.screensaver idleTime 0

  # Disable window restoration — prevent past sessions from reappearing.
  defaults write -g NSQuitAlwaysKeepsWindows -bool false    # per-app quit restore
  defaults write -g ApplePersistenceIgnoreState -bool true   # ignore saved window state entirely
  defaults write com.apple.loginwindow TALLogoutSavesState -bool false   # per-user: no restore after reboot
  # Per-app override for Terminal (it can override the global pref with its own domain).
  defaults write com.apple.Terminal NSQuitAlwaysKeepsWindows -bool false
  # System-level write — takes precedence over the per-user pref on modern macOS.
  sudo defaults write /Library/Preferences/com.apple.loginwindow TALLogoutSavesState -bool false
  # Clear any existing saved window state files so they don't restore on first boot.
  rm -rf ~/Library/Saved\ Application\ State/ 2>/dev/null || true

  # LaunchDaemon (runs as root at system boot, BEFORE autologin/LaunchAgents) that
  # wipes the lume user's saved-application-state directory.  This fires earlier than
  # any LaunchAgent, so no app can read stale state on the current boot cycle.
  sudo tee /Library/LaunchDaemons/com.uitest.clear-saved-state.plist > /dev/null << 'DAEMON_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.uitest.clear-saved-state</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>rm -rf /Users/lume/Library/Saved\ Application\ State 2>/dev/null; true</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <false/>
</dict>
</plist>
DAEMON_EOF
  sudo chmod 644 /Library/LaunchDaemons/com.uitest.clear-saved-state.plist
  sudo chown root:wheel /Library/LaunchDaemons/com.uitest.clear-saved-state.plist
  echo "  LaunchDaemon installed: com.uitest.clear-saved-state (clears saved state before autologin)"

  # Belt-and-suspenders LaunchAgent: re-apply defaults on every GUI login in case
  # any pref got reset, and clear any state that may have been written mid-session.
  mkdir -p ~/Library/LaunchAgents
  cat > ~/Library/LaunchAgents/com.uitest.suppress-restore-windows.plist << 'RESTORE_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.uitest.suppress-restore-windows</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-c</string>
    <string>
      defaults write -g NSQuitAlwaysKeepsWindows -bool false
      defaults write -g ApplePersistenceIgnoreState -bool true
      defaults write com.apple.loginwindow TALLogoutSavesState -bool false
      defaults write com.apple.Terminal NSQuitAlwaysKeepsWindows -bool false
      rm -rf "$HOME/Library/Saved Application State/" 2>/dev/null
    </string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
RESTORE_EOF
  echo "  LaunchAgent installed: com.uitest.suppress-restore-windows"

  # Suppress the persistent "Sign In with Apple ID" dialog.
  # 1. System-wide managed pref — tells macOS the setup UI should be skipped.
  sudo defaults write /Library/Preferences/com.apple.SetupAssistant.managed SkipFirstRunUI -bool true
  # 2. Per-user defaults — mark all setup steps as already seen.
  defaults write com.apple.SetupAssistant.managed DidSeeAvatarSetup -bool true
  defaults write com.apple.SetupAssistant SkipFirstLoginOptimization -bool true
  defaults write com.apple.SetupAssistant DidSeeCloudSetup -bool true
  defaults write com.apple.SetupAssistant DidSeePrivacySetup -bool true
  defaults write com.apple.SetupAssistant DidSeeAvatarSetup -bool true
  defaults write com.apple.SetupAssistant DidSeeSiriSetup -bool true
  defaults write com.apple.SetupAssistant DidSeeApplePaySetup -bool true
  defaults write com.apple.SetupAssistant DidSeeScreenTimeSetup -bool true
  defaults write com.apple.SetupAssistant DidSeeActivationLock -bool true
  defaults write com.apple.SetupAssistant DidSeeSyncSetup -bool true
  defaults write com.apple.SetupAssistant DidSeeSyncSetup2 -bool true
  defaults write com.apple.SetupAssistant DidSeeTermsOfAddress -bool true
  defaults write com.apple.SetupAssistant DidSeeAppStore -bool true
  defaults write com.apple.SetupAssistant DidSeeLockdownMode -bool true
  defaults write com.apple.SetupAssistant DidSeeTouchIDSetup -bool true
  defaults write com.apple.SetupAssistant MiniBuddyShouldLaunchToResumeSetup -bool false
  defaults write com.apple.SetupAssistant MiniBuddyLaunchReason -int 0
  defaults write com.apple.iCloudHelper MaxPasswordAttempts -int 0
  # MiniBuddyLaunch in com.apple.loginwindow is the actual trigger that causes
  # Setup Assistant (MiniBuddy) to re-launch at every login regardless of
  # DidSee* keys. Must be explicitly disabled.
  defaults write com.apple.loginwindow MiniBuddyLaunch -bool false
  defaults write com.apple.loginwindow MiniBuddyLaunchCount -int 0
  # 3. Disable the Apple ID setup daemon and iCloud notification agents
  #    via launchctl so they never show the sign-in dialog at login.
  #
  #    IMPORTANT: launchctl disable "gui/<uid>/..." requires the Aqua (GUI)
  #    session to be bootstrapped. During SSH provisioning, the session may not
  #    be ready yet — wait up to 60 s for it before running the disables.
  UID_NUM="$(id -u)"
  _gui_wait=0
  until launchctl print "gui/${UID_NUM}" &>/dev/null || (( _gui_wait++ >= 30 )); do
    sleep 2
  done
  if launchctl print "gui/${UID_NUM}" &>/dev/null; then
    echo "  GUI session ready (gui/${UID_NUM}) — disabling Apple ID agents"
    launchctl disable "gui/${UID_NUM}/com.apple.appleidsetupd"          2>/dev/null || true
    launchctl disable "gui/${UID_NUM}/com.apple.iCloudUserNotificationsd" 2>/dev/null || true
    launchctl disable "gui/${UID_NUM}/com.apple.iCloudNotificationAgent"  2>/dev/null || true
    launchctl disable "gui/${UID_NUM}/com.apple.iCloudHelper"             2>/dev/null || true
    # Also stop any already-running instances (works when SIP is disabled).
    launchctl bootout "gui/${UID_NUM}/com.apple.appleidsetupd"           2>/dev/null || true
    launchctl bootout "gui/${UID_NUM}/com.apple.iCloudNotificationAgent" 2>/dev/null || true
    launchctl bootout "gui/${UID_NUM}/com.apple.iCloudHelper"            2>/dev/null || true
    echo "  Apple ID agents disabled in launchd database"
  else
    echo "  WARNING: GUI session not found after 60 s — installing login-time suppressor LaunchAgent"
  fi
  # Belt-and-suspenders: LaunchAgent that re-disables + stops Apple ID agents
  # at every GUI login. Catches the case where the launchd database wasn't
  # writable during SSH provisioning.
  mkdir -p ~/Library/LaunchAgents
  cat > ~/Library/LaunchAgents/com.uitest.suppress-appleid.plist << 'SUPPRESS_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.uitest.suppress-appleid</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-c</string>
    <string>
      U=$(id -u)
      launchctl disable "gui/$U/com.apple.appleidsetupd"           2>/dev/null
      launchctl disable "gui/$U/com.apple.iCloudNotificationAgent" 2>/dev/null
      launchctl disable "gui/$U/com.apple.iCloudHelper"            2>/dev/null
      launchctl disable "gui/$U/com.apple.iCloudUserNotificationsd" 2>/dev/null
      launchctl bootout "gui/$U/com.apple.appleidsetupd"           2>/dev/null
      launchctl bootout "gui/$U/com.apple.iCloudNotificationAgent" 2>/dev/null
      launchctl bootout "gui/$U/com.apple.iCloudHelper"            2>/dev/null
      pkill -x appleidsetupd           2>/dev/null
      pkill -x iCloudNotificationAgent 2>/dev/null
    </string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
SUPPRESS_EOF
  echo "  Suppressor LaunchAgent installed: com.uitest.suppress-appleid"
fi

# ── 2. Auto-login ─────────────────────────────────────────────────────────────

if want autologin; then
  echo "--- Auto-login ---"
  sudo sysadminctl -autologin set -userName "$VM_USER" -password "$VM_USER"
fi

# ── 3. SSH authorized_keys ────────────────────────────────────────────────────

if want ssh_key; then
  echo "--- SSH authorized_keys ---"
  mkdir -p ~/.ssh && chmod 700 ~/.ssh
  if [[ -n "${HOST_PUBKEY:-}" ]]; then
    echo "$HOST_PUBKEY" >> ~/.ssh/authorized_keys
    sort -u ~/.ssh/authorized_keys -o ~/.ssh/authorized_keys
    chmod 600 ~/.ssh/authorized_keys
    echo "Host public key added to authorized_keys"
  else
    echo "WARNING: HOST_PUBKEY not set — skipping authorized_keys setup"
  fi
fi

# ── 4. Xcode license pre-flight ──────────────────────────────────────────────
# If xcode-select points at Xcode.app, accept the license before Homebrew runs.
# Homebrew's git invokes xcrun which will abort with a license prompt otherwise.
# If the Xcode.app is broken/incomplete, reset xcode-select to CLT so Homebrew
# can use the CLT git uninterrupted.

if xcode-select -p 2>/dev/null | grep -q 'Xcode.app'; then
  echo "--- Xcode license pre-flight ---"
  # Use a 30s timeout: a broken/partial Xcode.app can cause xcodebuild to hang.
  if timeout 30 sudo xcodebuild -license accept 2>/dev/null; then
    echo "  Xcode license accepted"
  else
    echo "  WARNING: xcodebuild -license accept failed or timed out — resetting to CLT"
    sudo xcode-select --reset
  fi
fi

# ── 5. Homebrew ───────────────────────────────────────────────────────────────

if want homebrew || want screenresolution || want xcbeautify || want jq || want socat || want peekaboo; then
  echo "--- Homebrew ---"
  if ! command -v brew &>/dev/null; then
    NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    eval "$(/opt/homebrew/bin/brew shellenv)"
    echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile
  else
    echo "Homebrew already installed: $(brew --version | head -1)"
  fi
fi

# Ensure brew is in PATH for rest of script
eval "$(/opt/homebrew/bin/brew shellenv 2>/dev/null || /usr/local/bin/brew shellenv 2>/dev/null || true)"

# ── 6. Brew tools ─────────────────────────────────────────────────────────────

BREW_PKGS=()
want screenresolution && BREW_PKGS+=(screenresolution)
want xcbeautify       && BREW_PKGS+=(xcbeautify)
want jq               && BREW_PKGS+=(jq)
want socat            && BREW_PKGS+=(socat)

if [[ ${#BREW_PKGS[@]} -gt 0 ]]; then
  echo "--- Brew tools: ${BREW_PKGS[*]} ---"
  brew install "${BREW_PKGS[@]}"
fi

if want peekaboo; then
  echo "--- Peekaboo ---"
  brew tap steipete/tap 2>/dev/null || true
  brew install steipete/tap/peekaboo
fi

# ── 6. Screen resolution ──────────────────────────────────────────────────────

if want screenresolution; then
  echo "--- Screen resolution ---"
  screenresolution set 1920x1080x32@60 \
    || echo "WARNING: screenresolution set failed (may need active display)"
fi

# ── 7. System logging config ──────────────────────────────────────────────────
# Enables debug- and info-level Apple Unified Log messages and exposes private
# data in log output — critical for diagnosing UI test failures from system events.
# The plist is bundled in scripts/ and uploaded to /tmp by 04-provision-vm.sh.

if want logging; then
  echo "--- System logging config ---"
  LOGGING_PLIST="/tmp/com.apple.system.logging.plist"
  if [[ -f "$LOGGING_PLIST" ]]; then
    sudo mkdir -p /Library/Preferences/Logging
    # Use || true: on macOS 15.6.1+ the Logging dir is SIP-protected even with
    # csrutil disabled; a failure here is non-fatal — log verbosity just stays
    # at the default level rather than debug/info.
    sudo cp "$LOGGING_PLIST" /Library/Preferences/Logging/com.apple.system.logging.plist \
      2>/dev/null || true
    sudo killall logd 2>/dev/null || true
    echo "System logging configured (debug+info levels enabled)"
  else
    echo "WARNING: com.apple.system.logging.plist not found at /tmp — skipping"
  fi
fi

# ── 9. Xcode license + first launch ──────────────────────────────────────────
# Xcode itself is installed by 04-provision-vm.sh (separate step with --xcode arg).
# This step accepts the license and runs first launch if Xcode is already present.

if [[ -d /Applications/Xcode.app ]]; then
  echo "--- Xcode license + first launch ---"
  sudo xcodebuild -license accept 2>&1 || echo "WARNING: xcodebuild -license accept failed"
  sudo xcodebuild -runFirstLaunch 2>&1 || echo "WARNING: xcodebuild -runFirstLaunch failed"
fi

# ── 10. Peekaboo MCP LaunchAgent ─────────────────────────────────────────────

if want peekaboo_agent; then
  echo "--- Peekaboo MCP launcher + LaunchAgent ---"
  cat > ~/run-peekaboo-mcp.sh << 'PEEKABOO_EOF'
#!/bin/bash
/opt/homebrew/bin/socat TCP-LISTEN:4040,reuseaddr,fork EXEC:"/opt/homebrew/bin/peekaboo mcp"
PEEKABOO_EOF
  chmod +x ~/run-peekaboo-mcp.sh

  mkdir -p ~/Library/LaunchAgents
  # Use variable expansion (no quotes on PLIST_EOF) so $HOME is substituted.
  cat > ~/Library/LaunchAgents/com.uitest.peekaboo-mcp.plist << PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.uitest.peekaboo-mcp</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/open</string>
    <string>-a</string>
    <string>Terminal</string>
    <string>$HOME/run-peekaboo-mcp.sh</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
PLIST_EOF
  echo "LaunchAgent installed: com.uitest.peekaboo-mcp (Terminal → socat TCP:4040 → peekaboo mcp)"
fi

# ── 11. TCC permissions ────────────────────────────────────────────────────────
# Requires SIP disabled (done in Phase 3).
# flags=0 on kTCCServiceScreenCapture = full/direct access (bypasses the macOS
# private window picker introduced in macOS 14). flags=1 would be limited/picker.
#
# All 6 UI-test control endpoints receive equal grants across all 7 services:
#   Accessibility, ScreenCapture, RemoteDesktop, PostEvent,
#   DeveloperTool, SystemPolicyAllFiles, AppleEvents→SystemEvents
#
# Endpoints (system DB):
#   com.apple.Terminal              (bundle, type=0) — hosts socat/peekaboo MCP
#   boo.peekaboo.peekaboo           (bundle, type=0) — peekaboo app bundle
#   /usr/libexec/sshd-keygen-wrapper (path, type=1) — SSH sessions
#   /opt/homebrew/bin/peekaboo       (path, type=1) — peekaboo binary
#   /bin/bash                        (path, type=1) — shell via SSH
#   /bin/zsh                         (path, type=1) — shell via SSH
#
# User DB: AppleEvents → com.apple.systemevents for all endpoints
# (enables osascript System Events control — needed for coords-based click).

if want tcc; then
  echo "--- TCC: equalized grants for all 6 UI-test endpoints ---"
  TCC_SYS_DB="/Library/Application Support/com.apple.TCC/TCC.db"
  TCC_USR_DB="$HOME/Library/Application Support/com.apple.TCC/TCC.db"
  NOW=$(date +%s)

  # TCC schema varies by macOS version — detect available columns at runtime so
  # the same script works on macOS 11 through 26 without hard-coding version checks.
  #
  # macOS 11 (Big Sur):   uses 'allowed'/'prompt_count'; no auth_value/auth_reason/auth_version
  # macOS 12–13:          uses auth_value/auth_reason/auth_version; no boot_uuid/last_reminded
  # macOS 14+ (Sonoma+):  adds pid/pid_version/boot_uuid/last_reminded
  #
  # kTCCServiceRemoteDesktop (bypass-picker alert) is recognised from macOS 14+.
  # On older macOS the row is harmlessly ignored by tccd, so we still insert it.
  _HAS_AUTH="$(sudo sqlite3 "$TCC_SYS_DB" \
    "SELECT COUNT(*) FROM pragma_table_info('access') WHERE name='auth_value';" 2>/dev/null || echo 0)"
  _HAS_BOOT="$(sudo sqlite3 "$TCC_SYS_DB" \
    "SELECT COUNT(*) FROM pragma_table_info('access') WHERE name='boot_uuid';" 2>/dev/null || echo 0)"

  # Generate a binary csreq blob from a code-signing requirement string.
  # macOS 14+ requires a valid csreq for kTCCServiceRemoteDesktop to be honored;
  # without it tccd ignores the row and shows the permission dialog anyway.
  _csreq_hex() {
    python3 - "$1" 2>/dev/null <<'PYEOF'
import ctypes, sys
req_str_in = sys.argv[1].encode()
Security = ctypes.CDLL("/System/Library/Frameworks/Security.framework/Security")
CF = ctypes.CDLL("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
CF.CFStringCreateWithCString.restype = ctypes.c_void_p
CF.CFStringCreateWithCString.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_uint32]
CF.CFDataGetLength.restype = ctypes.c_long
CF.CFDataGetBytePtr.restype = ctypes.c_void_p
Security.SecRequirementCreateWithString.restype = ctypes.c_int32
Security.SecRequirementCreateWithString.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.POINTER(ctypes.c_void_p)]
Security.SecRequirementCopyData.restype = ctypes.c_int32
Security.SecRequirementCopyData.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.POINTER(ctypes.c_void_p)]
cf_str = CF.CFStringCreateWithCString(None, req_str_in, 0x08000100)
req_ref = ctypes.c_void_p()
if Security.SecRequirementCreateWithString(cf_str, 0, ctypes.byref(req_ref)) != 0: sys.exit(1)
data_ref = ctypes.c_void_p()
if Security.SecRequirementCopyData(req_ref, 0, ctypes.byref(data_ref)) != 0: sys.exit(1)
length = CF.CFDataGetLength(data_ref)
ptr = CF.CFDataGetBytePtr(data_ref)
blob = (ctypes.c_uint8 * length).from_address(ptr)
print(bytes(blob).hex())
PYEOF
  }

  _TERMINAL_CSREQ_HEX="$(_csreq_hex 'identifier "com.apple.Terminal" and anchor apple')"
  _PEEKABOO_CSREQ_HEX="$(_csreq_hex 'identifier "boo.peekaboo.peekaboo"')"
  _SSHD_CSREQ_HEX="$(_csreq_hex 'identifier "com.apple.sshd-keygen-wrapper" and anchor apple')"
  [[ -n "$_TERMINAL_CSREQ_HEX" ]] && echo "  csreq: Terminal (${#_TERMINAL_CSREQ_HEX} hex chars)" \
                                   || echo "  WARNING: failed to generate Terminal csreq"
  [[ -n "$_PEEKABOO_CSREQ_HEX" ]] && echo "  csreq: peekaboo bundle (${#_PEEKABOO_CSREQ_HEX} hex chars)" \
                                   || echo "  WARNING: failed to generate peekaboo bundle csreq"
  [[ -n "$_SSHD_CSREQ_HEX" ]]     && echo "  csreq: sshd-keygen-wrapper (${#_SSHD_CSREQ_HEX} hex chars)" \
                                   || echo "  WARNING: failed to generate sshd-keygen-wrapper csreq"

  # tcc_grant DB SVC CLIENT CLIENT_TYPE [CSREQ_HEX [INDIRECT_OBJ]]
  # INDIRECT_OBJ is used for kTCCServiceAppleEvents (the target bundle being controlled).
  tcc_grant() {
    local DB="$1" SVC="$2" CLIENT="$3" CLIENT_TYPE="$4" CSREQ_HEX="${5:-}" INDIRECT_OBJ="${6:-UNUSED}"
    local CSREQ_SQL="NULL"
    [[ -n "$CSREQ_HEX" ]] && CSREQ_SQL="X'${CSREQ_HEX}'"
    # indirect_object_identifier_type: NULL for non-AE services; 0 (bundle) when an
    # indirect object is present (covers both specific bundles and the '0' wildcard).
    local _indirect_type="NULL"
    [[ "$INDIRECT_OBJ" != "UNUSED" ]] && _indirect_type="0"
    local _ok=false
    local _sudo=""
    [[ "$DB" == "$TCC_SYS_DB" ]] && _sudo="sudo"
    if [[ "$_HAS_BOOT" == "1" ]]; then
      # macOS 14+ full schema
      $_sudo sqlite3 "$DB" \
        "INSERT OR REPLACE INTO access
           (service,client,client_type,auth_value,auth_reason,auth_version,
            csreq,policy_id,indirect_object_identifier_type,
            indirect_object_identifier,indirect_object_code_identity,
            flags,last_modified,pid,pid_version,boot_uuid,last_reminded)
         VALUES(\"$SVC\",\"$CLIENT\",$CLIENT_TYPE,2,4,1,
                ${CSREQ_SQL},NULL,${_indirect_type},\"${INDIRECT_OBJ}\",NULL,0,$NOW,NULL,NULL,\"UNUSED\",$NOW);" 2>/dev/null \
        && _ok=true
    elif [[ "$_HAS_AUTH" == "1" ]]; then
      # macOS 12–13 schema (no pid/boot_uuid/last_reminded columns)
      $_sudo sqlite3 "$DB" \
        "INSERT OR REPLACE INTO access
           (service,client,client_type,auth_value,auth_reason,auth_version,
            csreq,policy_id,indirect_object_identifier_type,
            indirect_object_identifier,indirect_object_code_identity,
            flags,last_modified)
         VALUES(\"$SVC\",\"$CLIENT\",$CLIENT_TYPE,2,4,1,
                ${CSREQ_SQL},NULL,${_indirect_type},\"${INDIRECT_OBJ}\",NULL,0,$NOW);" 2>/dev/null \
        && _ok=true
    else
      # macOS 11 schema (uses 'allowed'/'prompt_count' instead of auth_* columns)
      $_sudo sqlite3 "$DB" \
        "INSERT OR REPLACE INTO access
           (service,client,client_type,allowed,prompt_count,
            csreq,policy_id,indirect_object_identifier_type,
            indirect_object_identifier,indirect_object_code_identity,
            flags,last_modified)
         VALUES(\"$SVC\",\"$CLIENT\",$CLIENT_TYPE,1,0,
                ${CSREQ_SQL},NULL,${_indirect_type},\"${INDIRECT_OBJ}\",NULL,0,$NOW);" 2>/dev/null \
        && _ok=true
    fi
    $_ok && echo "  $SVC → $CLIENT: granted" \
         || echo "  WARNING: $SVC → $CLIENT grant failed (SIP enabled?)"
  }

  # ── System DB grants ───────────────────────────────────────────────────────
  # Services granted to all endpoints (union of what any endpoint currently has).
  # PostEvent:            allows CGEventPost / keyboard+mouse injection
  # DeveloperTool:        allows spawning debuggers, instruments, xcrun
  # SystemPolicyAllFiles: full disk access (needed for reading arbitrary test artifacts)
  ALL_SVCS=(
    kTCCServiceAccessibility
    kTCCServiceScreenCapture
    kTCCServiceRemoteDesktop
    kTCCServicePostEvent
    kTCCServiceDeveloperTool
    kTCCServiceSystemPolicyAllFiles
  )

  echo "  [system DB] Terminal (bundle, type=0)"
  for SVC in "${ALL_SVCS[@]}"; do
    tcc_grant "$TCC_SYS_DB" "$SVC" "com.apple.Terminal" 0 "$_TERMINAL_CSREQ_HEX"
  done

  echo "  [system DB] peekaboo bundle (type=0)"
  for SVC in "${ALL_SVCS[@]}"; do
    tcc_grant "$TCC_SYS_DB" "$SVC" "boo.peekaboo.peekaboo" 0 "$_PEEKABOO_CSREQ_HEX"
  done

  echo "  [system DB] sshd-keygen-wrapper (path, type=1)"
  for SVC in "${ALL_SVCS[@]}"; do
    tcc_grant "$TCC_SYS_DB" "$SVC" "/usr/libexec/sshd-keygen-wrapper" 1 "$_SSHD_CSREQ_HEX"
  done

  echo "  [system DB] peekaboo binary (path, type=1)"
  for SVC in "${ALL_SVCS[@]}"; do
    tcc_grant "$TCC_SYS_DB" "$SVC" "/opt/homebrew/bin/peekaboo" 1 ""
  done

  echo "  [system DB] /bin/bash (path, type=1)"
  for SVC in "${ALL_SVCS[@]}"; do
    tcc_grant "$TCC_SYS_DB" "$SVC" "/bin/bash" 1 ""
  done

  echo "  [system DB] /bin/zsh (path, type=1)"
  for SVC in "${ALL_SVCS[@]}"; do
    tcc_grant "$TCC_SYS_DB" "$SVC" "/bin/zsh" 1 ""
  done

  # ── User DB grants — AppleEvents → System Events ───────────────────────────
  # Required for `osascript -e 'tell application "System Events"...'` to work
  # from SSH sessions without triggering a permission dialog.
  # The client is whichever process sends the Apple Events; granting all our
  # endpoints covers every execution path.
  echo "  [user DB] AppleEvents → com.apple.systemevents for all endpoints"
  mkdir -p "$(dirname "$TCC_USR_DB")"

  AE_CLIENTS=(
    "com.apple.Terminal:0:$_TERMINAL_CSREQ_HEX"
    "boo.peekaboo.peekaboo:0:$_PEEKABOO_CSREQ_HEX"
    "/usr/libexec/sshd-keygen-wrapper:1:$_SSHD_CSREQ_HEX"
    "/opt/homebrew/bin/peekaboo:1:"
    "/bin/bash:1:"
    "/bin/zsh:1:"
  )
  for _entry in "${AE_CLIENTS[@]}"; do
    _client="${_entry%%:*}"; _rest="${_entry#*:}"; _type="${_rest%%:*}"; _csreq="${_rest#*:}"
    tcc_grant "$TCC_USR_DB" "kTCCServiceAppleEvents" "$_client" "$_type" "$_csreq" "com.apple.systemevents"
  done

  # Wildcard AppleEvents grant: indirect_object_identifier='0' lets each client
  # send Apple Events to ANY app without a per-target TCC prompt (e.g. osascript
  # targeting DuckDuckGo, Safari, or any other app via SSH).
  echo "  [user DB] AppleEvents → wildcard (any app) for all endpoints"
  for _entry in "${AE_CLIENTS[@]}"; do
    _client="${_entry%%:*}"; _rest="${_entry#*:}"; _type="${_rest%%:*}"; _csreq="${_rest#*:}"
    tcc_grant "$TCC_USR_DB" "kTCCServiceAppleEvents" "$_client" "$_type" "$_csreq" "0"
  done

  # ── macOS 15+ ScreenCaptureKit bypass-picker hint suppression ────────────────
  # On macOS 15 (Sequoia) and later, the first time an app uses ScreenCaptureKit
  # with the legacy SCShareableContent API (bypassing the new "window picker"),
  # macOS shows a one-time dialog: "is requesting to bypass the system private
  # window picker and directly access your screen and audio."
  # Even with kTCCServiceScreenCapture granted in TCC.db, this dialog appears on
  # first use and blocks the UI. The approval is stored in ScreenCaptureApprovals.plist
  # inside group.com.apple.replayd. Pre-populating it with a far-future hint date
  # suppresses the dialog on fresh VM clones.
  if [[ "$(sw_vers -productVersion | cut -d. -f1)" -ge 15 ]]; then
    echo "  [macOS 15+] Pre-approving ScreenCaptureKit bypass for Terminal + Peekaboo"
    python3 - <<'SCPY'
import plistlib, datetime, os
plist_dir  = os.path.expanduser("~/Library/Group Containers/group.com.apple.replayd")
plist_path = os.path.join(plist_dir, "ScreenCaptureApprovals.plist")
os.makedirs(plist_dir, exist_ok=True)
far_future = datetime.datetime(2099, 1, 1, tzinfo=datetime.timezone.utc)
now        = datetime.datetime.now(datetime.timezone.utc)
try:
    with open(plist_path, "rb") as f:
        data = plistlib.load(f)
except Exception:
    data = {}
for bundle in ("com.apple.Terminal", "boo.peekaboo.peekaboo"):
    entry = data.get(bundle, {})
    entry["kScreenCapturePrivacyHintDate"]     = far_future
    entry["kScreenCapturePrivacyHintPolicy"]   = 315360000
    entry["kScreenCaptureApprovalLastAlerted"] = entry.get("kScreenCaptureApprovalLastAlerted", now)
    entry["kScreenCaptureApprovalLastUsed"]    = now
    entry["kScreenCaptureAlertableUsageCount"] = 0
    data[bundle] = entry
with open(plist_path, "wb") as f:
    plistlib.dump(data, f, fmt=plistlib.FMT_XML)
print(f"  ScreenCaptureApprovals.plist: hint suppressed until 2099 for {list(data)}")
SCPY
  fi

  sudo killall tccd 2>/dev/null || true
  echo "  tccd restarted — TCC grants active"
fi

# ── 12. Automation mode ───────────────────────────────────────────────────────

if want automation; then
  echo "--- Automation mode ---"
  # Enables Xcode automation mode so UI test runners can launch and control
  # the tested app without interactive authorization dialogs (no password prompts).
  echo "$VM_USER" | sudo automationmodetool enable-automationmode-without-authentication \
    && echo "  Automation mode enabled: UI test runners can control apps without auth prompts" \
    || echo "  WARNING: automationmodetool failed — UI tests may prompt for authorization"
fi

echo ""
# Final cleanup: clear any saved application state that accumulated during this
# provisioning run (Terminal windows opened by this script, etc.) so the golden
# image is clean before it is stopped and snapshotted.
rm -rf ~/Library/Saved\ Application\ State/ 2>/dev/null || true

echo "=== VM provisioning complete ==="
