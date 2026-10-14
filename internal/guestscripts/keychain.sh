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
for ((attempt=0; attempt<30; attempt++)); do
  [[ "$(stat -f %Su /dev/console)" == "$user" ]] && break
  sleep 1
done
[[ "$(stat -f %Su /dev/console)" == "$user" ]] || exit 65
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
ObjC.import('Foundation'); ObjC.import('Security'); ObjC.import('AppKit');
// Override JXA's opaque CFTypeRef output metadata with an object reference.
ObjC.bindFunction('SecItemCopyMatching', ['int', ['id', 'id*']]);
function run(argv) {
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
  var size = $.NSScreen.mainScreen.frame.size;
  var displayManaged = ObjC.unwrap($.NSProcessInfo.processInfo.environment.objectForKey('VIRFIELD_DISPLAY_MANAGED')) === '1';
  return JSON.stringify({version:1, scope:argv[0], nonce:argv[1], boot:argv[2],
    display_width:size.width, display_height:size.height, display_managed:displayManaged,
    session_id:Number(sid[0]), aqua:aqua, unlocked:unlocked,
    add_status:add, read_status:read, delete_status:remove, value_equal:equal,
    // SSH may report a synthetic screen for its separate WindowServer session.
    // UI geometry is certified in Aqua; SSH only certifies keychain access.
    ready:(aqua || argv[3] === 'ssh') && unlocked && add === 0 && read === 0 && remove === 0 && equal && (!displayManaged || !aqua || size.width === 1920 && size.height === 1080)});
}
JXA
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
mode=ensure
nonce="login-$boot"
if [[ -f "$dir/request" && ! -L "$dir/request" ]]; then
  { IFS= read -r requested_mode; IFS= read -r requested_nonce; IFS= read -r requested_boot; } < "$dir/request"
  rm "$dir/request"
  # A request from the previous boot must not suppress automatic GUI unlock.
  if [[ "$requested_boot" == "$boot" ]]; then
    [[ "$requested_mode" == ensure || "$requested_mode" == verify ]] || exit 64
    [[ "$requested_nonce" =~ ^keychain-[a-f0-9]+$ ]] || exit 64
    mode="$requested_mode"; nonce="$requested_nonce"
  fi
fi
keychain="$HOME/Library/Keychains/virfield-$scope.keychain-db"
[[ ! -L "$keychain" ]] || exit 65
if [[ "$mode" == ensure ]]; then
  if [[ "$VIRFIELD_DISPLAY_MANAGED" == 1 ]]; then
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
    [[ ! -L "$login" ]] || exit 65
    ready=false
    attempt=0
    for candidate in "$password" "$previous" "$(id -un)"; do
      [[ -n "$candidate" ]] || continue
      attempt=$((attempt+1))
      /usr/bin/security lock-keychain "$login"
      /usr/bin/security unlock-keychain -p "$candidate" "$login" 2>/dev/null || continue
      /usr/bin/security default-keychain -d user -s "$login"
      /usr/bin/security list-keychains -d user -s "$login" /Library/Keychains/System.keychain
      /usr/bin/osascript -l JavaScript "$dir/probe.js" "$scope" "$nonce-login-$attempt" "$boot" > "$dir/login-result"
      grep -q '"ready":true' "$dir/login-result" || continue
      if [[ "$candidate" != "$password" ]]; then
        /usr/bin/security set-keychain-password -o "$candidate" -p "$password" "$login"
      fi
      # A wrong-password unlock may report success on newer macOS. Lock again,
      # authenticate with the NEW password, then require application-style I/O.
      /usr/bin/security lock-keychain "$login"
      /usr/bin/security unlock-keychain -p "$password" "$login"
      /usr/bin/osascript -l JavaScript "$dir/probe.js" "$scope" "$nonce-login-new" "$boot" > "$dir/login-result"
      grep -q '"ready":true' "$dir/login-result" || exit 65
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
    /usr/bin/security create-keychain -p "$password" "$keychain"
  fi
  chmod 600 "$keychain"
  # Settings can themselves ask SecurityAgent to unlock a locked keychain.
  # Authenticate first, including on every subsequent GUI login/reboot.
  /usr/bin/security unlock-keychain -p "$password" "$keychain"
  # Equivalent to CI's no-sleep/no-timeout-lock policy. The private password
  # belongs to this image/lease, never the public CI password "default".
  /usr/bin/security set-keychain-settings "$keychain"
  /usr/bin/security default-keychain -d user -s "$keychain"
  if [[ -e "$login" ]]; then
    /usr/bin/security list-keychains -d user -s "$keychain" "$login" /Library/Keychains/System.keychain
  else
    /usr/bin/security list-keychains -d user -s "$keychain" /Library/Keychains/System.keychain
  fi
fi
default="$(/usr/bin/security default-keychain -d user)"
[[ "$default" == *\""$keychain"\" ]] || exit 65
[[ "$(stat -f %u:%Lp "$keychain")" == "$(id -u):600" ]] || exit 65
/usr/bin/osascript -l JavaScript "$dir/probe.js" "$scope" "$nonce" "$boot" > "$dir/result.next"
chmod 600 "$dir/result.next"
mv "$dir/result.next" "$dir/result"
grep -q '"ready":true' "$dir/result" || exit 65
if [[ "$mode" == ensure ]]; then
  printf '%s\n%s\n' "$scope" "$boot" > "$dir/startup.next"
  mv "$dir/startup.next" "$dir/startup"
fi
AGENT
  chmod 600 "$dir/display.js" "$dir/probe.js" "$dir/session.sh"
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
/usr/bin/security unlock-keychain -p "$password" "$keychain" >/dev/null
if [[ -e "$HOME/Library/Keychains/login.keychain-db" ]]; then
  /usr/bin/security unlock-keychain -p "$password" "$HOME/Library/Keychains/login.keychain-db" >/dev/null
fi
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
  expected="$(printf '%s\n%s' "$scope" "$boot")"
  for ((attempt=0; attempt<60; attempt++)); do
    [[ -f "$dir/startup" && "$(cat "$dir/startup")" == "$expected" ]] && break
    sleep 1
  done
  [[ -f "$dir/startup" && "$(cat "$dir/startup")" == "$expected" ]] || exit 65
fi
[[ ! -L "$dir/result" && ! -L "$dir/request" && ! -L "$dir/agent.log" ]] || exit 65
: > "$dir/agent.log"; chmod 600 "$dir/agent.log"
rm -f "$dir/result"
printf '%s\n%s\n%s\n' "$mode" "$nonce" "$boot" > "$dir/request.next"
chmod 600 "$dir/request.next"
mv "$dir/request.next" "$dir/request"
if [[ "$mode" == ensure ]]; then
  launchctl bootstrap "gui/$uid" "$plist"
else
  # startup is written just before the login agent exits. Restart this owned
  # agent even in that brief overlap, so a verify request cannot be dropped.
  launchctl kickstart -k "gui/$uid/$label"
fi
for ((attempt=0; attempt<45; attempt++)); do
  if [[ -f "$dir/result" ]]; then
    cat "$dir/result"
    exit 0
  fi
  sleep 1
done
echo 'GUI keychain agent did not complete; inspect its private agent.log' >&2
exit 65
