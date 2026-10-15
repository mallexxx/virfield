package baremetal

// Node VM scripts. They run as the node account with
// `sh -c <script> sh <scope> <vm> <args...>`: scope is leases or images, the
// VM name is a validated lease or image name, and every other argument is a
// validated configuration value. Each script touches only
// ${VF_VM_HOME:-$HOME/virfield}/<scope>/<vm>, the container virfield-<vm> and, for commit,
// one new file in the goldens directory, and each is idempotent.

const vmDirs = `set -eu
vf_vm_root="${VF_VM_HOME:-$HOME/virfield}"
d="$vf_vm_root/$1/$2"
c="virfield-$2"
`

// The pinned OCI image confuses VMX TSC scaling with invariant guest TSC.
// Override that old detection only from actual host stability signals; a
// missing/unstable clock never receives an invented invariant-TSC capability.
const vmCPUFunctions = `
vf_tsc_flags() {
  if [ -r "$1" ] && [ -r "$2" ] && [ "$(cat "$2")" = tsc ] &&
    awk '/^flags[[:space:]]*:/ {
      seen=1; constant=0; nonstop=0
      for (i=2; i<=NF; i++) {
        if ($i == "constant_tsc") constant=1
        if ($i == "nonstop_tsc") nonstop=1
      }
      if (!constant || !nonstop) bad=1
    } END { exit !(seen && !bad) }' "$1"; then
    printf '%s' '+invtsc'
  else
    printf '%s' '-invtsc'
  fi
}
`

// vmCloneScript: $3 golden, $4 goldens dir, $5 image, $6 port base, $7 max.
// Ports are allocated under a lock so concurrent clones never share one.
const vmCloneScript = vmDirs + `g="$4/$3.qcow2"
[ -f "$g" ] || { echo "golden $3 not found" >&2; exit 3; }
umask 077
mkdir -p "$vf_vm_root/$1"
lock="$vf_vm_root/$1/.ports.lock"
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
    grep -qxs "$p" "$vf_vm_root/$1"/*/port || break
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
// $7 memory GB, $8 disk size, $9 keep-awake file (may be empty), $10 desktop
// publish address (empty for none). passt forwards guest SSH and Screen
// Sharing; QEMU's own VNC moves off 5900 and is never published.
const vmStartScript = sleepHoldFunctions + vmContainerFunctions + vmCPUFunctions + vmDirs + `[ -f "$d/port" ]
g=$(cat "$d/golden")
p=$(cat "$d/port")
running=false
if vf_container_exists; then
  running=$(podman inspect -f '{{.State.Running}}' "$c")
  case "$running" in true|false) ;; *) echo "invalid container state" >&2; exit 3;; esac
fi
if [ "$running" != true ]; then
  if vf_container_exists; then podman rm -f "$c" >/dev/null; fi
  vf_hold "$d" "${9:-}"
  vf_flags=$(vf_tsc_flags /proc/cpuinfo /sys/devices/system/clocksource/clocksource0/current_clocksource)
  podman run -d --name "$c" --device /dev/kvm --device /dev/net/tun --cap-add NET_ADMIN \
    -e DISK_FMT=qcow2 -e "DISK_SIZE=$8" -e "RAM_SIZE=$7G" -e "CPU_CORES=$6" -e USER_PORTS=22,5900 -e VNC_PORT=5950 \
    -e DISK_CACHE=writeback -e DISK_IO=threads \
    -e "CPU_FLAGS=$vf_flags" \
    -p "$5:$p:22" ${10:+-p "${10}:5900"} -v "$d/storage:/storage:Z" -v "$3:$3:ro,z" -v "$3/$g.opencore.plist:/custom.plist:ro,z" \
    --stop-timeout 30 "$4" >/dev/null
fi
vf_hold "$d" "${9:-}"
printf running > "$d/state"
`

// vmStopScript: $3 keep-awake file. macOS clones can hang at the end of a
// guest shutdown, so stop is ACPI with a bounded wait, then kill.
const vmContainerFunctions = `
vf_container_exists() {
  vf_status=0
  podman container exists "$c" || vf_status=$?
  case "$vf_status" in 0) return 0;; 1) return 1;; *) echo "container observation failed" >&2; exit "$vf_status";; esac
}
vf_stop_container() {
  if vf_container_exists; then
    podman stop -t 30 "$c" >/dev/null 2>&1 || podman kill "$c" >/dev/null 2>&1
    podman rm -f "$c" >/dev/null
    if vf_container_exists; then echo "container cleanup not confirmed" >&2; exit 3; fi
  fi
}
`

const vmStopScript = sleepHoldFunctions + vmContainerFunctions + vmDirs + `vf_stop_container
vf_unhold "$d" "${3:-}"
[ ! -d "$d" ] || printf stopped > "$d/state"
`

const vmDeleteScript = vmStopScript + `rm -rf "$d"
`

// The controller authorizes an exact unused image name. Both completed golden
// files and an interrupted image clone are removed, never a backing image.
const vmImageDeleteScript = sleepHoldFunctions + vmContainerFunctions + vmDirs + `
for scope in leases images; do
  for other in "$vf_vm_root/$scope"/*/; do
    [ -f "$other/golden" ] || continue
    [ "$(cat "$other/golden")" != "$2" ] || { echo "golden has an overlay" >&2; exit 3; }
  done
done
vf_stop_container
vf_unhold "$d" "${4:-}"
rm -rf "$d"
rm -f "$3/$2.qcow2" "$3/$2.opencore.plist" "$3/.$2.tmp"
`

// vmCommitScript: $3 goldens dir, $4 image. Writes <goldens>/<vm>.qcow2 once,
// with the source golden's OpenCore config, then removes the image clone.
const vmCommitScript = vmDirs + `[ -f "$d/storage/data.qcow2" ]
umask 077
out="$3/$2.qcow2"
[ ! -e "$out" ] || { echo "golden $2 exists" >&2; exit 3; }
temporary="$3/.$2.tmp"
[ ! -e "$temporary" ] && [ ! -L "$temporary" ] || { echo "golden staging file exists; inspect before recovery" >&2; exit 3; }
# The OCI process does not inherit the SSH shell's umask. Protect the disk from
# its first byte, including when the operator's golden directory is traversable.
podman run --rm --umask 0077 --entrypoint qemu-img -v "$3:$3:z" -v "$d/storage:/src:ro,Z" "$4" convert -O qcow2 /src/data.qcow2 "$temporary"
chmod 400 "$temporary"
mv "$temporary" "$out"
cp "$3/$(cat "$d/golden").opencore.plist" "$3/$2.opencore.plist"
chmod 400 "$out" "$3/$2.opencore.plist"
rm -rf "$d"
`

// vmProbeLines lists goldens and VM folders with $1 the goldens directory.
// The port forward accepts TCP before the guest boots, so a running VM's SSH
// counts only once the guest's banner is read through the node loopback.
const vmProbeLines = `
vf_vm_root="${VF_VM_HOME:-$HOME/virfield}"
if [ "$vf_vm_root" != "$HOME/virfield" ]; then
  for vf_scope in leases images; do
    for vf_old in "$HOME/virfield/$vf_scope"/*/; do
      [ ! -f "$vf_old/kind" ] || { echo 'Legacy VM overlays must be resolved before changing storage_dir' >&2; exit 3; }
    done
  done
fi
umask 077
mkdir -p "$vf_vm_root/leases" "$vf_vm_root/images"
vf_disk_size() {
  [ -f "$1" ] && [ "$(od -An -tx1 -N4 "$1" | tr -d ' \n')" = 514649fb ] || { echo 0; return; }
  od -An -tu1 -j24 -N8 "$1" | awk '{for(i=1;i<=NF;i++){n=n*256+$i;c++}} END {printf "%.0f\n",(c==8 && n<=549755813888)?n:0}'
}
vf_space=$(df -Pk "$vf_vm_root/leases" "$1") || exit 3
printf '%s\n' "$vf_space" | awk 'NR>1 {if(!n || $2<c)c=$2; if(!n || $4<a)a=$4; printf "%s_disk=%s %.0f\n",(n==0?"overlay":"golden"),$1,$4*1024; n++} END {if(n!=2)exit 3; printf "disk_capacity=%.0f\ndisk_available=%.0f\n",c*1024,a*1024}' || exit 3
vf_cache=$(du -sk "$1") || exit 3
echo "cache_bytes=$(( $(printf '%s\n' "$vf_cache" | awk '{print $1}') * 1024 ))"
for f in "$1"/*.qcow2; do
  [ -f "$f" ] && echo "golden=$(basename "$f" .qcow2) $(vf_disk_size "$f")"
done
for s in leases images; do
  for d in "$vf_vm_root/$s"/*/; do
    [ -f "$d/kind" ] || continue
    n=$(basename "$d")
    st=stopped
    exists=0
    podman container exists "virfield-$n" || exists=$?
    case "$exists" in
      0) running=$(podman inspect -f '{{.State.Running}}' "virfield-$n")
         case "$running" in true) st=running;; false) ;; *) echo 'Invalid container state' >&2; exit 3;; esac ;;
      1) ;;
      *) echo 'Container observation failed' >&2; exit "$exists" ;;
    esac
    p=$(cat "$d/port")
    cpu=${2:-0}
    memory=${3:-0}
    if [ "$st" = running ]; then
      env=$(podman inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "virfield-$n")
      cpu=$(printf '%s\n' "$env" | awk -F= '$1=="CPU_CORES" {print $2}')
      gb=$(printf '%s\n' "$env" | awk -F= '$1=="RAM_SIZE" {sub(/G$/,"",$2); print $2}')
      case "$gb" in ''|*[!0-9]*) memory=0;; *) memory=$((gb * 1024 * 1024 * 1024));; esac
    fi
    ssh=0
    if [ "$st" = running ] && timeout 3 bash -c "exec 3<>/dev/tcp/127.0.0.1/$p && head -c 4 <&3" 2>/dev/null | grep -q '^SSH-'; then
      ssh=1
    fi
    vf_identity=unknown
    if [ -f "$d/storage/data.qcow2" ] && [ ! -L "$d/storage/data.qcow2" ]; then
      vf_inode=$(stat -c '%d:%i' "$d/storage/data.qcow2" 2>/dev/null || stat -f '%d:%i' "$d/storage/data.qcow2" 2>/dev/null || true)
      case "$vf_inode" in ''|*[!0-9:]*) ;; *) vf_identity="node-file-v1:$vf_inode";; esac
    fi
    echo "vm=$s $n $st $p $(cat "$d/golden") $ssh ${cpu:-0} $memory $(vf_disk_size "$d/storage/data.qcow2") $vf_identity"
  done
done
true
`
