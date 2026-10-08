package baremetal

// Node VM scripts. They run as the node account with
// `sh -c <script> sh <scope> <vm> <args...>`: scope is leases or images, the
// VM name is a validated lease or image name, and every other argument is a
// validated configuration value. Each script touches only
// $HOME/virfield/<scope>/<vm>, the container virfield-<vm> and, for commit,
// one new file in the goldens directory, and each is idempotent.

const vmDirs = `set -eu
d="$HOME/virfield/$1/$2"
c="virfield-$2"
`

// vmCloneScript: $3 golden, $4 goldens dir, $5 image, $6 port base, $7 max.
// Ports are allocated under a lock so concurrent clones never share one.
const vmCloneScript = vmDirs + `g="$4/$3.qcow2"
[ -f "$g" ] || { echo "golden $3 not found" >&2; exit 3; }
mkdir -p "$HOME/virfield/$1"
lock="$HOME/virfield/$1/.ports.lock"
tries=0
until mkdir "$lock" 2>/dev/null; do
  tries=$((tries + 1))
  [ "$tries" -lt 30 ] || { echo "port lock busy" >&2; exit 3; }
  sleep 1
done
trap 'rmdir "$lock"' EXIT
if [ ! -f "$d/port" ]; then
  i=0
  while [ "$i" -lt "$7" ]; do
    p=$(($6 + i))
    grep -qxs "$p" "$HOME"/virfield/"$1"/*/port || break
    i=$((i + 1))
  done
  [ "$i" -lt "$7" ] || { echo "no free VM port" >&2; exit 3; }
  umask 077
  mkdir -p "$d/storage"
  chattr +C "$d/storage" 2>/dev/null || true
  printf vm > "$d/kind"
  printf '%s' "$3" > "$d/golden"
  printf '%s' "$p" > "$d/port"
fi
[ -f "$d/storage/data.qcow2" ] || podman run --rm --entrypoint qemu-img -v "$4:$4:ro,z" -v "$d/storage:/out:Z" "$5" create -q -f qcow2 -b "$g" -F qcow2 /out/data.qcow2
[ -f "$d/state" ] || printf stopped > "$d/state"
`

// vmStartScript: $3 goldens dir, $4 image, $5 publish address, $6 cpu,
// $7 memory GB, $8 disk size, $9 keep-awake file (may be empty).
const vmStartScript = vmDirs + `[ -f "$d/port" ]
g=$(cat "$d/golden")
p=$(cat "$d/port")
if ! podman inspect -f '{{.State.Running}}' "$c" 2>/dev/null | grep -qx true; then
  podman rm -f "$c" >/dev/null 2>&1 || true
  podman run -d --name "$c" --device /dev/kvm --device /dev/net/tun --cap-add NET_ADMIN \
    -e DISK_FMT=qcow2 -e "DISK_SIZE=$8" -e "RAM_SIZE=$7G" -e "CPU_CORES=$6" -e USER_PORTS=22 \
    -p "$5:$p:22" -v "$d/storage:/storage:Z" -v "$3:$3:ro,z" -v "$3/$g.opencore.plist:/custom.plist:ro,z" \
    --stop-timeout 30 "$4" >/dev/null
fi
if [ -n "${9:-}" ] && [ ! -e "$9" ]; then
  : > "$9"
  : > "$d/hold-created"
fi
printf running > "$d/state"
`

// vmStopScript: $3 keep-awake file. macOS clones can hang at the end of a
// guest shutdown, so stop is ACPI with a bounded wait, then kill.
const vmStopScript = vmDirs + `[ -d "$d" ] || exit 0
podman stop -t 30 "$c" >/dev/null 2>&1 || true
podman kill "$c" >/dev/null 2>&1 || true
podman rm -f "$c" >/dev/null 2>&1 || true
if [ -f "$d/hold-created" ]; then
  [ -n "${3:-}" ] && rm -f "$3"
  rm -f "$d/hold-created"
fi
printf stopped > "$d/state"
`

const vmDeleteScript = vmStopScript + `rm -rf "$d"
`

// vmCommitScript: $3 goldens dir, $4 image. Writes <goldens>/<vm>.qcow2 once,
// with the source golden's OpenCore config, then removes the image clone.
const vmCommitScript = vmDirs + `[ -f "$d/storage/data.qcow2" ]
out="$3/$2.qcow2"
[ ! -e "$out" ] || { echo "golden $2 exists" >&2; exit 3; }
podman run --rm --entrypoint qemu-img -v "$3:$3:z" -v "$d/storage:/src:ro,Z" "$4" convert -O qcow2 /src/data.qcow2 "$3/.$2.tmp"
mv "$3/.$2.tmp" "$out"
cp "$3/$(cat "$d/golden").opencore.plist" "$3/$2.opencore.plist"
chmod 444 "$out" "$3/$2.opencore.plist"
rm -rf "$d"
`

// vmProbeLines lists goldens and VM folders with $1 the goldens directory.
const vmProbeLines = `for f in "$1"/*.qcow2; do
  [ -f "$f" ] && echo "golden=$(basename "$f" .qcow2)"
done
for s in leases images; do
  for d in "$HOME"/virfield/$s/*/; do
    [ -f "$d/kind" ] || continue
    n=$(basename "$d")
    st=stopped
    podman inspect -f '{{.State.Running}}' "virfield-$n" 2>/dev/null | grep -qx true && st=running
    echo "vm=$s $n $st $(cat "$d/port") $(cat "$d/golden")"
  done
done
true
`
