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
#   tcc               TCC grants for Terminal: ScreenCapture, Accessibility, DeveloperTool
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
  # System-level write — takes precedence over the per-user pref on modern macOS.
  sudo defaults write /Library/Preferences/com.apple.loginwindow TALLogoutSavesState -bool false
  # Clear any existing saved window state files so they don't restore on first boot.
  rm -rf ~/Library/Saved\ Application\ State/ 2>/dev/null || true

  # Belt-and-suspenders LaunchAgent: clear saved window state on every GUI login
  # so it can never accumulate between reboots.
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
      rm -rf "$HOME/Library/Saved Application State/" 2>/dev/null
    </string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
RESTORE_EOF
  echo "  Suppress-restore-windows LaunchAgent installed: com.uitest.suppress-restore-windows"

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

# ── 11. TCC permissions for Terminal.app ──────────────────────────────────────
# Requires SIP disabled (done in Phase 3).
# Grants Terminal screen recording + accessibility + developer tool so that
# peekaboo (child process of Terminal) inherits those permissions.

if want tcc; then
  echo "--- TCC: Terminal + sshd-keygen-wrapper screen recording + accessibility ---"
  TCC_DB="/Library/Application Support/com.apple.TCC/TCC.db"
  NOW=$(date +%s)

  # Terminal: screen recording, accessibility, developer tool
  for SVC in kTCCServiceScreenCapture kTCCServiceAccessibility kTCCServiceDeveloperTool; do
    sudo sqlite3 "$TCC_DB" \
      "INSERT OR REPLACE INTO access
         (service,client,client_type,auth_value,auth_reason,auth_version,
          csreq,policy_id,indirect_object_identifier_type,
          indirect_object_identifier,indirect_object_code_identity,
          flags,last_modified,pid,pid_version,boot_uuid,last_reminded)
       VALUES(\"$SVC\",\"com.apple.Terminal\",0,2,4,1,
              NULL,NULL,NULL,\"UNUSED\",NULL,0,$NOW,NULL,NULL,\"UNUSED\",$NOW);" 2>/dev/null \
      && echo "  $SVC → Terminal: granted" \
      || echo "  WARNING: $SVC grant failed — SIP may still be enabled"
  done

  # sshd-keygen-wrapper: accessibility + control (client_type=1 = path-based)
  for SVC in kTCCServiceAccessibility kTCCServiceScreenCapture; do
    sudo sqlite3 "$TCC_DB" \
      "INSERT OR REPLACE INTO access
         (service,client,client_type,auth_value,auth_reason,auth_version,
          csreq,policy_id,indirect_object_identifier_type,
          indirect_object_identifier,indirect_object_code_identity,
          flags,last_modified,pid,pid_version,boot_uuid,last_reminded)
       VALUES(\"$SVC\",\"/usr/libexec/sshd-keygen-wrapper\",1,2,4,1,
              NULL,NULL,NULL,\"UNUSED\",NULL,0,$NOW,NULL,NULL,\"UNUSED\",$NOW);" 2>/dev/null \
      && echo "  $SVC → sshd-keygen-wrapper: granted" \
      || echo "  WARNING: $SVC grant failed for sshd-keygen-wrapper"
  done

  sudo killall tccd 2>/dev/null || true
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
echo "=== VM provisioning complete ==="
