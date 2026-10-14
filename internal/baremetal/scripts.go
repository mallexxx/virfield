package baremetal

// Fixed node scripts. They run as the node account over pinned SSH with
// `sh -c <script> sh <vm> [keep-awake-file]`; the VM name is a validated
// lease name and the keep-awake file a validated absolute path. Every script
// touches only $HOME/virfield/leases/<vm> and authorized_keys lines tagged
// "virfield-lease-<vm>", and is idempotent.

// probeScript reports the booted environment and lease markers. On macOS it
// also reports whether XCUITest can run unattended: Developer Mode
// (DevToolsSecurity) must be enabled and Automation Mode must not require
// authentication, or testmanagerd asks for an admin password on the console,
// and the node account must own the console session.
const probeScript = `set -eu
if [ "$(uname -s)" = Darwin ]; then
  echo "version=$(sw_vers -productVersion)"
  echo "build=$(sw_vers -buildVersion)"
  echo "volume=$(diskutil info / | awk -F': *' '/Volume Name/ {print $2; exit}')"
  echo "cpu=$(sysctl -n hw.ncpu)"
  echo "memory=$(sysctl -n hw.memsize)"
  if ! DevToolsSecurity -status 2>/dev/null | grep -q "currently enabled"; then
    echo "ui_testing=devtools_disabled"
  elif [ -x /usr/bin/automationmodetool ] && /usr/bin/automationmodetool 2>/dev/null | grep -q "requires user authentication"; then
    echo "ui_testing=automation_mode_requires_auth"
  elif [ "$(stat -f %Su /dev/console 2>/dev/null)" != "$(id -un)" ]; then
    echo "ui_testing=no_console_session"
  else
    echo "ui_testing=ready"
  fi
else
  . /etc/os-release
  echo "version=${VERSION_ID:-}"
  echo "build=${VERSION:-${VERSION_ID:-}}"
  echo "volume=${ID:-linux}"
  echo "cpu=$(nproc)"
  echo "memory=$(( $(awk '/^MemTotal:/ {print $2}' /proc/meminfo) * 1024 ))"
fi
mkdir -p "$HOME/virfield/leases"
for d in "$HOME"/virfield/leases/*/; do
  [ -f "$d/state" ] || continue
  [ -f "$d/kind" ] && continue
  echo "lease=$(basename "$d") $(cat "$d/state")"
done
`

// cloneScript reserves the lease workspace on the node.
const cloneScript = `set -eu
d="$HOME/virfield/leases/$1"
umask 077
mkdir -p "$HOME/virfield/leases"
lock="$HOME/virfield/leases/.host.lock"
tries=0
until mkdir "$lock" 2>/dev/null; do
  tries=$((tries + 1))
  [ "$tries" -lt 30 ] || exit 3
  sleep 1
done
trap 'rmdir "$lock"' EXIT
for other in "$HOME"/virfield/leases/*/; do
  [ -f "$other/state" ] || continue
  [ -f "$other/kind" ] && continue
  [ "$(basename "$other")" = "$1" ] || { echo "physical host reserved" >&2; exit 3; }
done
mkdir -p "$d/workspace"
[ -f "$d/state" ] || printf stopped > "$d/state"
`

// startScript keeps the node awake for the lease: caffeinate on macOS, and
// the node's keep-awake file (created only if absent) where configured.
const startScript = sleepHoldFunctions + `set -eu
d="$HOME/virfield/leases/$1"
[ -d "$d" ]
[ -f "$d/kind" ] && { echo "node VM folder" >&2; exit 3; }
if command -v caffeinate >/dev/null 2>&1; then
  if [ ! -f "$d/inhibitor.pid" ] || ! kill -0 "$(cat "$d/inhibitor.pid")" 2>/dev/null; then
    nohup caffeinate -dimsu </dev/null >/dev/null 2>&1 &
    echo $! > "$d/inhibitor.pid"
  fi
fi
vf_hold "$d" "${2:-}"
printf running > "$d/state"
`

// stopScript releases what startScript took, never another owner's hold.
const stopScript = sleepHoldFunctions + `set -eu
d="$HOME/virfield/leases/$1"
[ -f "$d/kind" ] && { echo "node VM folder" >&2; exit 3; }
if [ -f "$d/inhibitor.pid" ]; then
  kill "$(cat "$d/inhibitor.pid")" 2>/dev/null || true
  rm -f "$d/inhibitor.pid"
fi
vf_unhold "$d" "${2:-}"
[ ! -d "$d" ] || printf stopped > "$d/state"
`

// deleteScript revokes the lease key and removes the lease workspace. The
// authorized_keys file is rewritten in place to keep its owner and mode.
const deleteScript = stopScript + `k="$HOME/.ssh/authorized_keys"
if [ -f "$k" ]; then
  grep -v " virfield-lease-$1\$" "$k" > "$k.virfield-tmp" || true
  cat "$k.virfield-tmp" > "$k"
  rm -f "$k.virfield-tmp"
fi
rm -rf "$d"
`

// prepareScript authorizes the lease's client key (canonical, from stdin)
// and prints how many tagged lines exist, which must be exactly one.
const prepareScript = `set -eu
IFS= read -r key
d="$HOME/virfield/leases/$1"
[ -d "$d" ]
[ -f "$d/kind" ] && { echo "node VM folder" >&2; exit 3; }
umask 077
mkdir -p "$HOME/.ssh"
k="$HOME/.ssh/authorized_keys"
touch "$k"
line="$key virfield-lease-$1"
grep -qxF "$line" "$k" || printf '%s\n' "$line" >> "$k"
grep -cxF "$line" "$k"
`
