#!/usr/bin/env bash
# _vnc_tool.sh — locate or install the VNC client required by macOS 27 flows.

ensure_vncdotool() {
  if command -v vncdotool >/dev/null 2>&1; then
    VNCDTOOL="$(command -v vncdotool)"
    return 0
  fi

  local cache_dir="${VIRFIELD_VNC_VENV:-$HOME/.cache/virfield/vncdotool}"
  VNCDTOOL="$cache_dir/bin/vncdotool"
  [[ -x "$VNCDTOOL" ]] && return 0

  command -v uv >/dev/null 2>&1 || {
    echo "macOS 27 VNC automation requires either vncdotool or uv." >&2
    return 1
  }

  log "  Installing vncdotool into $cache_dir..."
  uv venv "$cache_dir"
  uv pip install --python "$cache_dir/bin/python" vncdotool
  [[ -x "$VNCDTOOL" ]] || {
    echo "Failed to install vncdotool." >&2
    return 1
  }
}
