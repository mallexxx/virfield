package baremetal

// All physical and VM leases share a node-level reference set. Only the last
// reference may remove a file Virfield created; an external hold is preserved.
const sleepHoldFunctions = `
vf_hold_lock() {
  umask 077
  vf_root="$HOME/virfield/.sleep-hold"
  mkdir -p "$vf_root/refs"
  vf_lock="$vf_root/lock"
  vf_tries=0
  until mkdir "$vf_lock" 2>/dev/null; do
    vf_tries=$((vf_tries + 1))
    [ "$vf_tries" -lt 30 ] || { echo "sleep hold lock busy" >&2; return 3; }
    sleep 1
  done
  trap 'rmdir "$vf_lock"' EXIT
}
vf_hold_identity() {
  if [ "$(uname -s)" = Darwin ]; then stat -f '%d:%i' "$1"; else stat -c '%d:%i' "$1"; fi
}
vf_hold() {
  [ -n "$2" ] || return 0
  vf_dir="$1"
  vf_file="$2"
  vf_hold_lock
  vf_ref="$(basename "$(dirname "$vf_dir")")-$(basename "$vf_dir")"
  if [ ! -e "$vf_file" ]; then
    if (set -C; : > "$vf_file") 2>/dev/null; then
      printf '%s\n%s\n' "$vf_file" "$(vf_hold_identity "$vf_file")" > "$vf_root/owned"
    fi
  fi
  printf '%s\n' "$vf_file" > "$vf_root/refs/$vf_ref"
  rmdir "$vf_lock"
  trap - EXIT
}
vf_unhold() {
  vf_dir="$1"
  vf_file="${2:-}"
  vf_hold_lock
  vf_ref="$(basename "$(dirname "$vf_dir")")-$(basename "$vf_dir")"
  # Transfer a pre-reference-counting lease's ownership into the shared set.
  if [ -f "$vf_dir/hold-created" ] && [ -n "$vf_file" ] && [ -e "$vf_file" ] && [ ! -f "$vf_root/owned" ]; then
    printf '%s\n%s\n' "$vf_file" "$(vf_hold_identity "$vf_file")" > "$vf_root/owned"
  fi
  rm -f "$vf_root/refs/$vf_ref" "$vf_dir/hold-created"
  vf_any=0
  for vf_entry in "$vf_root"/refs/*; do [ ! -f "$vf_entry" ] || vf_any=1; done
  if [ "$vf_any" = 0 ] && [ -f "$vf_root/owned" ]; then
    vf_file=$(sed -n '1p' "$vf_root/owned")
    vf_identity=$(sed -n '2p' "$vf_root/owned")
    if [ -e "$vf_file" ] && [ "$(vf_hold_identity "$vf_file")" = "$vf_identity" ]; then rm -f "$vf_file"; fi
    rm -f "$vf_root/owned"
  fi
  rmdir "$vf_lock"
  trap - EXIT
}
`
