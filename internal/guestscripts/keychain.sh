#!/bin/bash
# Fixed VM-only installer/verification bridge. Credentials arrive on SSH stdin.
set +x
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
IFS= read -r mode
IFS= read -r user
IFS= read -r scope
IFS= read -r password
IFS= read -r nonce
IFS= read -r previous
[[ "$mode" == ensure || "$mode" == verify ]] || exit 64
[[ "$user" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ && "$user" == "$(id -un)" ]] || exit 64
[[ "$scope" =~ ^(image|lease)-[a-zA-Z0-9_.-]+$ && "$nonce" =~ ^keychain-[a-f0-9]+$ ]] || exit 64
[[ "$HOME" == "/Users/$user" ]] || exit 64
case "$(sysctl -n hw.model)" in VirtualMac*|iMacPro1,1) ;; *) exit 64 ;; esac
# Initial installation gets five minutes; post-reboot verification gets ten
# because SSH can precede the cold HDD Aqua login and its automatic native probe.
# All phases share that bound; reserve fifteen seconds for failure diagnostics.
# The caller's original operation/lease context still bounds the command.
deadline=$((SECONDS+285))
[[ "$mode" != verify ]] || deadline=$((SECONDS+585))
# The worker SSH bridge may already have waited for this boot's Aqua startup.
# Keep one command budget instead of giving the verification a second one.
bridge_elapsed="${VIRFIELD_GUI_WAIT_ELAPSED:-0}"
[[ "$bridge_elapsed" =~ ^[0-9]{1,3}$ ]] && (( bridge_elapsed <= 585 )) || exit 64
deadline=$((deadline-bridge_elapsed))
(( SECONDS < deadline )) || { echo 'GUI verification command budget was exhausted' >&2; exit 65; }
for ((attempt=0; attempt<120; attempt++)); do
  [[ "$(stat -f %Su /dev/console)" == "$user" ]] && break
  sleep 1
done
[[ "$(stat -f %Su /dev/console)" == "$user" ]] || { echo 'GUI console login did not become ready' >&2; exit 65; }
uid="$(id -u)"
dir="$HOME/.virfield-keychain"
label=ai.virfield.guest-keychain
plist="$HOME/Library/LaunchAgents/$label.plist"
boot="$(sysctl -n kern.bootsessionuuid)"
umask 077
[[ ! -L "$dir" ]] || exit 65
mkdir -p "$dir" "$HOME/Library/LaunchAgents"
chmod 700 "$dir"

if [[ "$mode" == ensure ]]; then
  [[ -n "$password" ]] || exit 64
  # Display sleep and screen-saver idle are independent. An idle saver can lock
  # the GUI and persist autoLoginUserScreenLocked across the next reboot.
  /usr/bin/defaults write com.apple.screensaver idleTime -int 0
  /usr/bin/defaults -currentHost write com.apple.screensaver idleTime -int 0
  # The lab's QEMU USB keyboard has no learned layout in the source golden.
  # Register only that fixed virtual ANSI device; preserve every other entry.
  if [[ "$(sysctl -n hw.model)" == iMacPro1,1 ]] && /usr/sbin/ioreg -r -c IOHIDDevice -l | grep -F '"Product" = "QEMU USB Keyboard"' >/dev/null; then
    for vf_keyboard in 1-1575-0 65535-1452-0; do
      printf '%s\n' "$password" | sudo -S -p '' /usr/bin/defaults write /Library/Preferences/com.apple.keyboardtype keyboardtype -dict-add "$vf_keyboard" -int 40
    done
  fi
  # Replace only Virfield's own agent; preserve all existing keychain files.
  launchctl bootout "gui/$uid/$label" 2>/dev/null || true
  printf '%s\n%s\n%s\n' "$scope" "$password" "$previous" > "$dir/identity.next"
  chmod 600 "$dir/identity.next"
  mv "$dir/identity.next" "$dir/identity"
  cat > "$dir/display.js" <<'DISPLAY'
ObjC.import('AppKit'); ObjC.import('CoreGraphics');
ObjC.bindFunction('CGDisplayCopyAllDisplayModes', ['id', ['unsigned int', 'id']]);
ObjC.bindFunction('CGDisplayModeGetWidth', ['unsigned long', ['id']]);
ObjC.bindFunction('CGDisplayModeGetHeight', ['unsigned long', ['id']]);
function run() {
  var display = $.CGMainDisplayID(), options = $.NSDictionary.alloc.init;
  var modes = $.CGDisplayCopyAllDisplayModes(display, options), code = -1;
  for (var i = 0; i < Number(modes.count); i++) {
    var mode = modes.objectAtIndex(i);
    if (Number($.CGDisplayModeGetWidth(mode)) === 1920 && Number($.CGDisplayModeGetHeight(mode)) === 1080) {
      var configuration = Ref();
      code = Number($.CGBeginDisplayConfiguration(configuration));
      if (code === 0) {
        code = Number($.CGConfigureDisplayWithDisplayMode(configuration[0], display, mode, options));
        if (code === 0) {
          // kCGConfigurePermanently: a temporary mode reverts when osascript
          // exits, making an immediate success a false readiness signal.
          code = Number($.CGCompleteDisplayConfiguration(configuration[0], 2));
        } else {
          $.CGCancelDisplayConfiguration(configuration[0]);
        }
      }
      break;
    }
  }
  var size = $.NSScreen.mainScreen.frame.size;
  return JSON.stringify({width:size.width, height:size.height, code:code,
    ready:code === 0 && size.width === 1920 && size.height === 1080});
}
DISPLAY
  cat > "$dir/probe.js" <<'JXA'
ObjC.import('Foundation'); ObjC.import('Security');
// Override JXA's opaque CFTypeRef output metadata with an object reference.
ObjC.bindFunction('SecItemCopyMatching', ['int', ['id', 'id*']]);
// ai.virfield.console-state begin
function virfieldConsoleState(session) {
  var lock = session && session.CGSSessionScreenIsLocked;
  var available = !!session && session.kCGSSessionOnConsoleKey === true && session.kCGSessionLoginDoneKey === true &&
    (typeof lock === 'undefined' || typeof lock === 'boolean');
  return {ready:available && lock !== true, screen_locked:available ? lock === true : null};
}
// ai.virfield.console-state end
function run(argv) {
  var loginOnly = argv[3] === 'login';
  if (!loginOnly) {
    ObjC.import('AppKit'); ObjC.import('CoreGraphics');
    ObjC.bindFunction('CGWindowListCopyWindowInfo', ['id', ['unsigned int', 'unsigned int']]);
    ObjC.bindFunction('CGSessionCopyCurrentDictionary', ['id', []]);
  }
  $.SecKeychainSetUserInteractionAllowed(false);
  var sid = Ref(), attributes = Ref(), flags = Ref();
  var session = Number($.SessionGetInfo(4294967295, sid, attributes));
  var status = Number($.SecKeychainGetStatus(null, flags));
  var aqua = session === 0 && (Number(attributes[0]) & 16) !== 0;
  var unlocked = status === 0 && (Number(flags[0]) & 1) !== 0;
  var add = -1, read = -1, remove = -1, equal = false;
  if ((aqua || argv[3] === 'ssh') && unlocked) {
    // JXA does not bridge Security's CFString globals. These are their public
    // SecItem keys/values: generic password, service/account, WhenUnlocked.
    var query = $.NSMutableDictionary.alloc.init;
    query.setObjectForKey($('genp'), $('class'));
    query.setObjectForKey($('ai.virfield.keychain-readiness'), $('svce'));
    query.setObjectForKey($(argv[1]), $('acct'));
    query.setObjectForKey($('ak'), $('pdmn'));
    var data = $(argv[1]).dataUsingEncoding($.NSUTF8StringEncoding);
    query.setObjectForKey(data, $('v_Data'));
    add = Number($.SecItemAdd(query, null));
    query.removeObjectForKey($('v_Data'));
    // Match DDG's read/delete query: accessibility config belongs to Add, not
    // lookup. Legacy login keychains do not consistently match that attribute.
    query.removeObjectForKey($('pdmn'));
    query.setObjectForKey($.NSNumber.numberWithBool(true), $('r_Data'));
    var found = Ref();
    read = Number($.SecItemCopyMatching(query, found));
    equal = read === 0 && Boolean(data.isEqualToData(found[0]));
    query.removeObjectForKey($('r_Data'));
    // Delete only this request's unique probe, never an application item.
    remove = Number($.SecItemDelete(query));
  }
  var size = {width:0, height:0};
  var displayManaged = ObjC.unwrap($.NSProcessInfo.processInfo.environment.objectForKey('VIRFIELD_DISPLAY_MANAGED')) === '1';
  var keyboardSetup = false, securityDialog = false, snapshotAvailable = false;
  var consoleReady = false, screenLocked = null;
  if (!loginOnly) {
    size = $.NSScreen.mainScreen.frame.size;
    if (aqua) {
      var console = virfieldConsoleState(ObjC.deepUnwrap($.CGSessionCopyCurrentDictionary()));
      screenLocked = console.screen_locked; consoleReady = console.ready;
      var apps = $.NSWorkspace.sharedWorkspace.runningApplications, keyboardPIDs = {}, securityPIDs = {};
      for (var i = 0; i < Number(apps.count); i++) {
        var app = apps.objectAtIndex(i), bundle = String(ObjC.unwrap(app.bundleIdentifier)).toLowerCase();
        if (bundle === 'com.apple.keyboardsetupassistant') keyboardPIDs[String(Number(app.processIdentifier))] = true;
        if (bundle === 'com.apple.securityagent') securityPIDs[String(Number(app.processIdentifier))] = true;
      }
      var windows = ObjC.deepUnwrap($.CGWindowListCopyWindowInfo(17, 0));
      snapshotAvailable = Array.isArray(windows);
      if (snapshotAvailable) {
        for (var i = 0; i < windows.length; i++) {
          var window = windows[i], pid = String(window.kCGWindowOwnerPID), owner = String(window.kCGWindowOwnerName);
          if (Number(window.kCGWindowAlpha) <= 0) continue;
          if (keyboardPIDs[pid] || owner === 'KeyboardSetupAssistant' || owner === 'Keyboard Setup Assistant') keyboardSetup = true;
          if (securityPIDs[pid] || owner === 'SecurityAgent') securityDialog = true;
        }
      }
    }
  }
  var keychainReady = (aqua || argv[3] === 'ssh') && unlocked && add === 0 && read === 0 && remove === 0 && equal;
  return JSON.stringify({version:1, scope:argv[0], nonce:argv[1], boot:argv[2],
    display_width:size.width, display_height:size.height, display_managed:displayManaged,
    keyboard_setup_visible:keyboardSetup, security_dialog_visible:securityDialog,
    desktop_snapshot_available:snapshotAvailable, keychain_ready:keychainReady,
    console_ready:consoleReady, screen_locked:screenLocked,
    session_id:Number(sid[0]), aqua:aqua, unlocked:unlocked,
    add_status:add, read_status:read, delete_status:remove, value_equal:equal,
    // SSH may report a synthetic screen for its separate WindowServer session.
    // UI geometry is certified in Aqua; SSH only certifies keychain access.
    ready:!loginOnly && keychainReady && (!aqua || consoleReady && snapshotAvailable && !keyboardSetup && !securityDialog) && (!displayManaged || !aqua || size.width === 1920 && size.height === 1080)});
}
JXA
  cat > "$dir/login-auth.js" <<'AUTH'
ObjC.import('Foundation'); ObjC.import('Security');
ObjC.bindFunction('SecKeychainOpen', ['int', ['char *', 'id *']]);
ObjC.bindFunction('SecKeychainUnlock', ['int', ['id', 'unsigned int', 'char *', 'bool']]);
ObjC.bindFunction('SecKeychainChangePassword', ['int', ['id', 'unsigned int', 'char *', 'unsigned int', 'char *']]);
function run(argv) {
  // This applies to this native caller, not a separately spawned security CLI.
  // A wrong old password must return an error instead of opening a GUI prompt.
  $.SecKeychainSetUserInteractionAllowed(false);
  var text = $.NSString.stringWithContentsOfFileEncodingError(argv[1], $.NSUTF8StringEncoding, null);
  if (!text) throw new Error('Private login identity is unavailable');
  var identity = String(ObjC.unwrap(text)).split('\n'), password = identity[1];
  var candidate = argv[2] === 'current' ? password : argv[2] === 'previous' ? identity[2] :
    argv[2] === 'user' ? String(ObjC.unwrap($.NSUserName())) : null;
  if (!password || candidate === null) throw new Error('Invalid login authentication request');
  if (!candidate) return JSON.stringify({authenticated:false, try_next:true, skipped:true});
  function result(status, retry) { return JSON.stringify({authenticated:status === 0, try_next:retry, status:status}); }
  function length(value) { return Number($(value).lengthOfBytesUsingEncoding($.NSUTF8StringEncoding)); }
  var reference = Ref(), status = Number($.SecKeychainOpen(argv[0], reference));
  if (status !== 0) return result(status, false);
  var keychain = reference[0];
  status = Number($.SecKeychainUnlock(keychain, length(candidate), candidate, true));
  if (status !== 0) return result(status, status === -25293);
  // ChangePassword authenticates the old secret even if already unlocked.
  status = Number($.SecKeychainChangePassword(keychain, length(candidate), candidate, length(password), password));
  if (status !== 0) return result(status, status === -25293);
  status = Number($.SecKeychainUnlock(keychain, length(password), password, true));
  return result(status, false);
}
AUTH
  cat > "$dir/session.sh" <<'AGENT'
#!/bin/bash
set +x
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
umask 077
dir="$HOME/.virfield-keychain"
[[ ! -L "$dir/identity" && "$(stat -f %u:%Lp "$dir/identity")" == "$(id -u):600" ]] || exit 65
{ IFS= read -r scope; IFS= read -r password; IFS= read -r previous || true; } < "$dir/identity"
[[ "$scope" =~ ^(image|lease)-[a-zA-Z0-9_.-]+$ && -n "$password" ]] || exit 64
boot="$(sysctl -n kern.bootsessionuuid)"
export VIRFIELD_DISPLAY_MANAGED=0
[[ "$(sysctl -n hw.model)" != VirtualMac* ]] || export VIRFIELD_DISPLAY_MANAGED=1
phase() { printf '%s %s\n' "$(/bin/date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" > "$dir/phase"; }
mode=ensure
nonce="login-$boot"
explicit_request=false
if [[ -f "$dir/request" && ! -L "$dir/request" ]]; then
  { IFS= read -r requested_mode; IFS= read -r requested_nonce; IFS= read -r requested_boot; } < "$dir/request"
  rm "$dir/request"
  # A request from the previous boot must not suppress automatic GUI unlock.
  if [[ "$requested_boot" == "$boot" ]]; then
    [[ "$requested_mode" == ensure || "$requested_mode" == verify ]] || exit 64
    [[ "$requested_nonce" =~ ^keychain-[a-f0-9]+$ ]] || exit 64
    mode="$requested_mode"; nonce="$requested_nonce"; explicit_request=true
  fi
fi
aqua_session="$(/usr/bin/osascript -l JavaScript -e "ObjC.import('Security'); function run() { var sid=Ref(), attributes=Ref(); if (Number($.SessionGetInfo(4294967295, sid, attributes)) !== 0 || (Number(attributes[0]) & 16) === 0) throw new Error('Aqua session unavailable'); return String(Number(sid[0])); }")"
[[ "$aqua_session" =~ ^[0-9]+$ ]] || exit 65
# ai.virfield.agent-startup-once begin
# A demand start can race a RunAtLoad instance that just completed. Reuse only
# that same boot's automatic proof; explicit native requests always run anew.
if [[ "$explicit_request" == false && -f "$dir/startup" && ! -L "$dir/startup" && "$(cat "$dir/startup")" == "$(printf '%s\n%s' "$scope" "$boot")" && -f "$dir/startup-session" && ! -L "$dir/startup-session" && "$(cat "$dir/startup-session")" == "$aqua_session" ]]; then
  exit 0
fi
# ai.virfield.agent-startup-once end
phase starting
keychain="$HOME/Library/Keychains/virfield-$scope.keychain-db"
[[ ! -L "$keychain" ]] || exit 65
if [[ "$mode" == ensure ]]; then
  if [[ "$(sysctl -n hw.model)" == iMacPro1,1 ]]; then
    phase virtual-keyboard
    /usr/bin/killall KeyboardSetupAssistant 2>/dev/null || true
  fi
  if [[ "$VIRFIELD_DISPLAY_MANAGED" == 1 ]]; then
    phase display
    # Tart's display size is a hint; a cached guest mode can remain 1024x768.
    # Select and verify the logical mode inside Aqua on every login/prepare.
    /usr/bin/osascript -l JavaScript "$dir/display.js" > "$dir/display-result"
    grep -q '"ready":true' "$dir/display-result" || exit 65
  fi
  # OS services (e.g. ScopedBookmarkAgent) retain explicit login-keychain
  # references. Preserve its items, rekey to the current VM password, and prove
  # native access; merely choosing another default leaves a blocking dialog.
  login="$HOME/Library/Keychains/login.keychain-db"
  if [[ -e "$login" ]]; then
    phase login-unlock
    [[ ! -L "$login" ]] || exit 65
    ready=false
    attempt=0
    for candidate in current previous user; do
      attempt=$((attempt+1))
      # Changing the password authenticates the old secret even when unlocked.
      # Never lock the real login keychain just to test a candidate: OS clients
      # can leave a persistent modal unlock request during that artificial gap.
      phase login-rekey
      /usr/bin/osascript -l JavaScript "$dir/login-auth.js" "$login" "$dir/identity" "$candidate" > "$dir/login-auth-result"
      if ! grep -q '"authenticated":true' "$dir/login-auth-result"; then
        grep -q '"try_next":true' "$dir/login-auth-result" && continue
        exit 65
      fi
      /usr/bin/security default-keychain -d user -s "$login"
      /usr/bin/security list-keychains -d user -s "$login" /Library/Keychains/System.keychain
      phase login-new-native-io
      /usr/bin/osascript -l JavaScript "$dir/probe.js" "$scope" "$nonce-login-new" "$boot" login > "$dir/login-result"
      # Desktop overlays do not make an authenticated password incorrect.
      # An I/O failure after successful rekey is held, never a password fallback.
      grep -q '"keychain_ready":true' "$dir/login-result" || exit 65
      chmod 600 "$login"
      /usr/bin/security set-keychain-settings "$login"
      ready=true; break
    done
    [[ "$ready" == true ]] || exit 65
    # Do not retain the inherited image password in a prepared worker.
    printf '%s\n%s\n' "$scope" "$password" > "$dir/identity.next"
    mv "$dir/identity.next" "$dir/identity"
  fi
  if [[ ! -e "$keychain" ]]; then
    phase scoped-create
    /usr/bin/security create-keychain -p "$password" "$keychain"
  fi
  chmod 600 "$keychain"
  # Settings can themselves ask SecurityAgent to unlock a locked keychain.
  # Authenticate first, including on every subsequent GUI login/reboot.
  /usr/bin/security unlock-keychain -p "$password" "$keychain"
  phase scoped-settings
  # Equivalent to CI's no-sleep/no-timeout-lock policy. The private password
  # belongs to this image/lease, never the public CI password "default".
  /usr/bin/security set-keychain-settings "$keychain"
  # ai.virfield.default-keychain-policy begin
  # loginwindow automatically unlocks login, not an arbitrary custom default.
  # Leaving the scoped keychain as default makes early OS clients (Spotlight)
  # queue password dialogs before this demand-started Aqua agent can unlock it.
  # Keep the preserved/rekeyed login default across boots; the scoped keychain
  # remains private, unlocked and explicitly searchable for scoped consumers.
  if [[ -e "$login" ]]; then
    /usr/bin/security default-keychain -d user -s "$login"
    /usr/bin/security list-keychains -d user -s "$login" "$keychain" /Library/Keychains/System.keychain
  else
    # A VM account without a login keychain cannot satisfy automatic GUI access.
    exit 65
  fi
  # ai.virfield.default-keychain-policy end
fi
default="$(/usr/bin/security default-keychain -d user)"
[[ "$default" == *\""$HOME/Library/Keychains/login.keychain-db"\" ]] || exit 65
[[ "$(stat -f %u:%Lp "$keychain")" == "$(id -u):600" ]] || exit 65
phase default-native-io
/usr/bin/osascript -l JavaScript "$dir/probe.js" "$scope" "$nonce" "$boot" > "$dir/result.next"
# A virtual keyboard assistant can appear after the early login dismissal.
# Retry only a completed, fully cleaned-up I/O probe blocked by that known UI.
if [[ "$(sysctl -n hw.model)" == iMacPro1,1 ]] && grep -q '"keychain_ready":true' "$dir/result.next" && grep -q '"keyboard_setup_visible":true' "$dir/result.next"; then
  phase virtual-keyboard-late
  /usr/bin/killall KeyboardSetupAssistant 2>/dev/null || true
  /usr/bin/osascript -l JavaScript "$dir/probe.js" "$scope" "$nonce" "$boot" > "$dir/result.next"
fi
# ai.virfield.desktop-protocol begin
if [[ "$scope" == lease-* ]]; then
  # A listening launchd socket does not attest a cold Screen Sharing process.
  # Warm and verify it in Aqua on every worker login/reboot and fresh probe.
  # Image qualification does not enable this worker-only service.
  phase desktop-protocol
  desktop_deadline=$((SECONDS+90))
  desktop_ready=false
  while (( SECONDS < desktop_deadline )); do
    if { exec 3<>/dev/tcp/127.0.0.1/5900; } 2>/dev/null; then
      remaining=$((desktop_deadline-SECONDS))
      if (( remaining > 0 )) && IFS= read -r -n 12 -t "$remaining" greeting <&3 && [[ "$greeting" =~ ^RFB\ [0-9]{3}\.[0-9]{3}$ ]]; then
        desktop_ready=true
        printf '%s\n%s\n%s\n' "$scope" "$boot" "$greeting" > "$dir/desktop-result"
        chmod 600 "$dir/desktop-result"
      fi
      exec 3<&-; exec 3>&-
      break
    fi
    sleep 1
  done
  [[ "$desktop_ready" == true ]] || { echo 'Guest Screen Sharing did not return its RFB greeting' >&2; exit 65; }
fi
# ai.virfield.desktop-protocol end
chmod 600 "$dir/result.next"
mv "$dir/result.next" "$dir/result"
grep -q '"ready":true' "$dir/result" || exit 65
if [[ "$mode" == ensure ]]; then
  printf '%s\n' "$aqua_session" > "$dir/startup-session.next"
  mv "$dir/startup-session.next" "$dir/startup-session"
  printf '%s\n%s\n' "$scope" "$boot" > "$dir/startup.next"
  mv "$dir/startup.next" "$dir/startup"
fi
phase ready
AGENT
  chmod 600 "$dir/display.js" "$dir/probe.js" "$dir/login-auth.js" "$dir/session.sh"
  cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$label</string>
<key>ProgramArguments</key><array><string>/bin/bash</string><string>$dir/session.sh</string></array>
<key>LimitLoadToSessionType</key><string>Aqua</string>
<key>RunAtLoad</key><true/>
<key>StandardOutPath</key><string>$dir/agent.log</string>
<key>StandardErrorPath</key><string>$dir/agent.log</string>
</dict></plist>
PLIST
  chmod 600 "$plist"
  # sshd starts a separate Security audit session. Unlock immediately before
  # each client command, in that same session, as the CI runner does.
  cat > "$dir/ssh-session.sh" <<'SSH'
#!/bin/bash
set +x
set -euo pipefail
dir="$HOME/.virfield-keychain"
[[ ! -L "$dir/identity" && "$(/usr/bin/stat -f %u:%Lp "$dir/identity")" == "$(/usr/bin/id -u):600" ]] || exit 65
{ IFS= read -r scope; IFS= read -r password; } < "$dir/identity"
[[ "$scope" =~ ^(image|lease)-[a-zA-Z0-9_.-]+$ && -n "$password" ]] || exit 65
keychain="$HOME/Library/Keychains/virfield-$scope.keychain-db"
[[ ! -L "$keychain" && "$(/usr/bin/stat -f %u:%Lp "$keychain")" == "$(/usr/bin/id -u):600" ]] || exit 65
# ai.virfield.ssh-gui-startup begin
gui_started=$SECONDS
export VIRFIELD_GUI_WAIT_ELAPSED=0
if [[ "$scope" == lease-* ]]; then
  boot="$(/usr/sbin/sysctl -n kern.bootsessionuuid)"
  expected="$(printf '%s\n%s' "$scope" "$boot")"
  if [[ ! -f "$dir/startup" || -L "$dir/startup" || "$(cat "$dir/startup")" != "$expected" ]]; then
    # RunAtLoad may remain registered with runs=0 as a speculative spawn after
    # login. Demand the owned Aqua agent without killing an existing instance.
    gui_deadline=$((SECONDS+585))
    agent_requested=false
    target="gui/$(/usr/bin/id -u)/ai.virfield.guest-keychain"
    while (( SECONDS < gui_deadline )); do
      [[ -f "$dir/startup" && ! -L "$dir/startup" && "$(cat "$dir/startup")" == "$expected" ]] && break
      # SSH may answer before loginwindow creates its Aqua service domain.
      # Wait for registration, then demand this instance once without -k.
      if [[ "$agent_requested" == false ]] && /bin/launchctl print "$target" >/dev/null 2>&1; then
        /bin/launchctl kickstart "$target"
        agent_requested=true
      fi
      sleep 1
    done
    [[ -f "$dir/startup" && ! -L "$dir/startup" && "$(cat "$dir/startup")" == "$expected" ]] || { echo 'GUI startup proof is unavailable' >&2; exit 65; }
  fi
fi
export VIRFIELD_GUI_WAIT_ELAPSED=$((SECONDS-gui_started))
# ai.virfield.ssh-gui-startup end
if [[ -e "$HOME/Library/Keychains/login.keychain-db" ]]; then
  /usr/bin/security unlock-keychain -p "$password" "$HOME/Library/Keychains/login.keychain-db" >/dev/null
fi
/usr/bin/security unlock-keychain -p "$password" "$keychain" >/dev/null
unset password
export VIRFIELD_DISPLAY_MANAGED=0
[[ "$(/usr/sbin/sysctl -n hw.model)" != VirtualMac* ]] || export VIRFIELD_DISPLAY_MANAGED=1
if [[ -z "${SSH_ORIGINAL_COMMAND:-}" ]]; then
  exec "${SHELL:-/bin/zsh}" -l
elif [[ "$SSH_ORIGINAL_COMMAND" == internal-sftp ]]; then
  exec /usr/libexec/sftp-server
else
  exec "${SHELL:-/bin/zsh}" -c "$SSH_ORIGINAL_COMMAND"
fi
SSH
  chmod 600 "$dir/ssh-session.sh"
  printf 'ForceCommand /bin/bash %s/ssh-session.sh\n' "$dir" > "$dir/sshd.conf"
  printf '%s\n' "$password" | sudo -S -p '' /usr/bin/install -o root -g wheel -m 600 "$dir/sshd.conf" /etc/ssh/sshd_config.d/001-virfield-keychain.conf
  printf '%s\n' "$password" | sudo -S -p '' /usr/sbin/sshd -t
else
  # Wait for this boot's automatic agent. Old golden evidence is insufficient.
  # ai.virfield.verify-startup begin
  expected="$(printf '%s\n%s' "$scope" "$boot")"
  # RunAtLoad is a non-demand condition and can remain speculative indefinitely.
  # Without -k, an already running startup is left intact.
  startup_requested=false
  # Startup and the requested proof share one command deadline; neither phase
  # restarts that budget. Keep the exact scope/boot match on the automatic agent.
  while (( SECONDS < deadline )); do
    [[ -f "$dir/startup" && ! -L "$dir/startup" && "$(cat "$dir/startup")" == "$expected" ]] && break
    if [[ "$startup_requested" == false ]] && launchctl print "gui/$uid/$label" >/dev/null 2>&1; then
      launchctl kickstart "gui/$uid/$label"
      startup_requested=true
    fi
    sleep 1
  done
  [[ -f "$dir/startup" && ! -L "$dir/startup" && "$(cat "$dir/startup")" == "$expected" ]] || { echo 'GUI agent did not publish fresh startup proof' >&2; exit 65; }
  # ai.virfield.verify-startup end
fi
(( SECONDS < deadline )) || { echo 'GUI verification command budget was exhausted' >&2; exit 65; }
[[ ! -L "$dir/result" && ! -L "$dir/request" && ! -L "$dir/agent.log" ]] || exit 65
: > "$dir/agent.log"; chmod 600 "$dir/agent.log"
rm -f "$dir/result"
printf '%s\n%s\n%s\n' "$mode" "$nonce" "$boot" > "$dir/request.next"
chmod 600 "$dir/request.next"
mv "$dir/request.next" "$dir/request"
if [[ "$mode" == ensure ]]; then
  launchctl bootstrap "gui/$uid" "$plist"
  launchctl kickstart "gui/$uid/$label"
else
  # startup is written just before the login agent exits. Restart this owned
  # agent even in that brief overlap, so a verify request cannot be dropped.
  launchctl kickstart -k "gui/$uid/$label"
fi
while (( SECONDS < deadline )); do
  if [[ -f "$dir/result" ]]; then
    cat "$dir/result"
    exit 0
  fi
  sleep 1
done
echo 'GUI keychain agent did not complete; inspect its private agent.log' >&2
exit 65
