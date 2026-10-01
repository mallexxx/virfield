#!/bin/bash
# Guest-only automation policy. Normal-boot SSH owns every mutation here.
# Paired Recovery owns SIP; never write host NVRAM or offline guest policy.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
mode="${1:?apply or verify}"
[[ "$mode" == apply || "$mode" == verify ]] || exit 64
[[ "$(id -u)" == 0 && "$(sysctl -n hw.model)" == VirtualMac* ]] || exit 64
[[ "$(csrutil status)" == 'System Integrity Protection status: disabled.' ]] || exit 65
major="$(sw_vers -productVersion)"; major="${major%%.*}"
case "$major" in 11|12|13|14|15|26|27) ;; *) exit 65 ;; esac
uid="$(id -u lume)"
[[ "$(stat -f %Su /dev/console)" == lume ]] || { echo 'Expected active lume desktop'; exit 65; }
system_db='/Library/Application Support/com.apple.TCC/TCC.db'
user_db='/Users/lume/Library/Application Support/com.apple.TCC/TCC.db'
# Newer macOS moves the user database into a protected container. Discover the
# exact file opened by this user's tccd, rather than assuming a host path.
pid="$(launchctl print "gui/$uid/com.apple.tccd" | awk '$1=="pid" && $2=="=" {print $3; exit}')"
[[ "$pid" =~ ^[0-9]+$ && "$(ps -p "$pid" -o uid= | tr -d ' ')" == "$uid" ]] || exit 66
opened="$(lsof -a -p "$pid" -Fn | sed -n 's/^n//p' | grep '/Library/Application Support/com.apple.TCC/TCC.db$' | sort -u)"
[[ -n "$opened" && "$opened" != *$'\n'* ]] || exit 66
case "$opened" in "$user_db"|/private/var/containers/Data/ProtectedSystem/*/Data/Library/Application\ Support/com.apple.TCC/TCC.db) user_db="$opened" ;; *) exit 66 ;; esac

if [[ "$mode" == apply ]]; then
  if (( major < 15 )); then
    spctl --master-disable
  else
    # macOS 15+ removed spctl's mutation interface. policydb reads this CFString.
    defaults write /var/db/SystemPolicyConfiguration/SystemPolicy-prefs enabled -string no
    chmod 644 /var/db/SystemPolicyConfiguration/SystemPolicy-prefs.plist
    if pgrep -x syspolicyd >/dev/null; then killall syspolicyd; fi
  fi
  # Preserve unrelated boot arguments; replace only a previous AMFI value.
  args="$(nvram boot-args 2>/dev/null | cut -f2- || true)"
  args="$(printf '%s\n' "$args" | awk '{for(i=1;i<=NF;i++) if($i !~ /^amfi_get_out_of_my_way=/) printf "%s ",$i}')"
  nvram boot-args="${args}amfi_get_out_of_my_way=1"
fi

work="$(mktemp -d /tmp/virfield-security.XXXXXX)"
trap 'rm -rf "$work"' EXIT
# As in upstream Lume's seed-tcc-guest.sh: derive the installed signed identity,
# generate a csreq blob, inspect the schema, and verify each grant after writing.
# No downloaded helper, Python runtime, or Homebrew is needed in the guest.
grant() {
  local db="$1" service="$2" client="$3" type="$4" target="$5" indirect="${6:-UNUSED}"
  local columns auth fields values requirement hex count
  [[ -f "$db" ]] || exit 66
  columns="$(sqlite3 -readonly "$db" 'PRAGMA table_info(access);')"
  if grep -q '|auth_value|' <<< "$columns"; then
    auth='auth_value=2'; fields='auth_value,auth_reason,auth_version'; values='2,2,1'
  elif grep -q '|allowed|' <<< "$columns" && grep -q '|prompt_count|' <<< "$columns"; then
    auth='allowed=1'; fields='allowed,prompt_count'; values='1,1'
  else
    echo 'Unsupported TCC schema'; exit 66
  fi
  codesign --verify --strict "$target"
  requirement="$(codesign -d -r- "$target" 2>&1 | sed -n 's/^designated => //p')"
  [[ -n "$requirement" ]] || exit 66
  printf '%s\n' "$requirement" > "$work/requirement"
  csreq -r "$work/requirement" -b "$work/csreq"
  hex="$(od -An -tx1 -v "$work/csreq" | tr -d ' \n')"
  [[ -n "$hex" ]] || exit 66
  if [[ "$mode" == apply ]]; then
    sqlite3 "$db" "BEGIN IMMEDIATE; INSERT OR REPLACE INTO access(service,client,client_type,$fields,csreq,flags,indirect_object_identifier_type,indirect_object_identifier,indirect_object_code_identity,last_modified) VALUES('$service','$client',$type,$values,X'$hex',0,0,'$indirect',NULL,strftime('%s','now')); COMMIT;"
  fi
  count="$(sqlite3 -readonly "$db" "SELECT count(*) FROM access WHERE service='$service' AND client='$client' AND client_type=$type AND $auth AND hex(csreq)=upper('$hex') AND indirect_object_identifier='$indirect';")"
  [[ "$count" == 1 ]] || { echo "TCC verification failed: $service $client"; exit 67; }
  printf 'TCC verified: %s %s\n' "$service" "$client"
}
for service in kTCCServiceAccessibility kTCCServiceScreenCapture kTCCServiceSystemPolicyAllFiles; do
  grant "$system_db" "$service" com.apple.Terminal 0 /System/Applications/Utilities/Terminal.app
  grant "$system_db" "$service" /usr/libexec/sshd-keygen-wrapper 1 /usr/libexec/sshd-keygen-wrapper
done
for service in kTCCServiceAppleEvents; do
  grant "$user_db" "$service" com.apple.Terminal 0 /System/Applications/Utilities/Terminal.app com.apple.systemevents
  grant "$user_db" "$service" /usr/libexec/sshd-keygen-wrapper 1 /usr/libexec/sshd-keygen-wrapper com.apple.systemevents
done
if [[ "$mode" == apply ]]; then
  if pgrep -x tccd >/dev/null; then killall tccd; fi
  pmset -a sleep 0 displaysleep 0
fi
assessment="$(spctl --status 2>&1 || true)"
[[ "$assessment" == 'assessments disabled' ]] || { echo "$assessment"; exit 67; }
if [[ "$mode" == verify ]]; then
  sysctl -n kern.bootargs | tr ' ' '\n' | grep -qx 'amfi_get_out_of_my_way=1'
fi
printf 'Guest policy %s complete; SIP disabled; Gatekeeper disabled\n' "$mode"
