#!/usr/bin/env python3
"""
vnc-send-keys.py  <vm_name>  [options]

Connects to a lume VM via RFB VNC (auto-discovered from 'lume ls') and sends
the macOS recovery mode key sequence to disable SIP:

  1. Wait for boot manager → recovery OS transition (~20-30s, auto-dismisses)
  2. Language selection: Right Right Enter
  3. Wait for UI to settle
  4. Open Terminal: Opt+Shift+T  (Opt is perceived as Cmd inside the VM)
  5. Wait for Terminal to open
  6. Type: csrutil disable  Enter
  7. Confirm: y  Enter
  8. Username: lume  Enter
  9. Password: lume  Enter
  10. Halt: halt  Enter

After each connection loss the script re-queries 'lume ls' to get the current
VNC URL — lume restarts the proxy at a new port when the VM transitions from
boot manager to recovery OS.

Logs to stdout and to vm.log in the worktree root.

Options:
  --record / --no-record   Record VNC framebuffer to video (default: record).
                           Recording starts after the 60s recovery boot wait,
                           when the framebuffer is live, and stops on exit.
  --output PATH            Output path for the recording.
                           Default: ~/Developer/virfield/recordings/
                                    YYYYMMDD-HHMMSS-<vm_name>-sip-disable.mp4
  --viewer                 Open the VNC session in the system VNC viewer
                           (open vnc://... — macOS Screen Sharing by default).
                           NOTE: Screen Sharing sends SetPixelFormat on connect
                           and kills lume's VNC proxy. Use a viewer that sends
                           only ClientInit (e.g. RealVNC Viewer) or omit this.

Optional env vars:
  VNC_USERNAME   (default: lume)
  VNC_PASSWORD   (default: lume)  -- the *macOS* user password, not VNC password
"""
import sys, os, re, socket, struct, time, signal, subprocess, argparse

# ── CLI args ──────────────────────────────────────────────────────────────────

parser = argparse.ArgumentParser(
    description="Send SIP-disable key sequence to a lume VM via VNC.",
    add_help=True,
)
parser.add_argument("vm_name", help="lume VM name (from 'lume ls')")
parser.add_argument(
    "--record", default=False, action=argparse.BooleanOptionalAction,
    help="Record VNC framebuffer to video (default: off)",
)
parser.add_argument(
    "--output", default=None, metavar="PATH",
    help="Output path for the recording (default: auto-named in ~/Developer/virfield/recordings/)",
)
parser.add_argument(
    "--viewer", default=False, action="store_true",
    help="Open VNC session in system viewer (open vnc://...). "
         "WARNING: macOS Screen Sharing kills lume's VNC proxy.",
)
parser.add_argument(
    "--log-dir", default=None, metavar="DIR",
    help="Directory for log file (default: ~/Developer/virfield/)",
)
parser.add_argument(
    "--macos-version", default=0, type=int, metavar="MAJOR",
    help="macOS major version (e.g. 15 for Sequoia, 26 for Tahoe). "
         "Default: 0 (auto-detect from VM name).",
)
args = parser.parse_args()

VM_NAME     = args.vm_name
VM_USER     = os.environ.get("VNC_USERNAME", "lume")
VM_PASS_STR = os.environ.get("VNC_PASSWORD", "lume")

_macos_version = args.macos_version
if _macos_version == 0:
    m = re.search(r'(?:macos-|tahoe-)(\d+)', VM_NAME)
    _macos_version = int(m.group(1)) if m else 15
TAHOE = _macos_version >= 26

# ── Recording output path ─────────────────────────────────────────────────────

_rec_proc = None
_rec_out  = None

if args.record:
    if args.output:
        _rec_out = os.path.expanduser(args.output)
    else:
        _rec_dir = os.path.expanduser("~/Developer/virfield/recordings")
        os.makedirs(_rec_dir, exist_ok=True)
        _ts      = time.strftime("%Y%m%d-%H%M%S")
        _rec_out = os.path.join(_rec_dir, f"{_ts}-{VM_NAME}-sip-disable.mp4")

# ── Logging ───────────────────────────────────────────────────────────────────

_log_dir = os.path.expanduser(args.log_dir) if args.log_dir else os.path.expanduser("~/Developer/virfield")
os.makedirs(_log_dir, exist_ok=True)
LOG_PATH = os.path.join(_log_dir, "build.log")
_log_fh  = open(LOG_PATH, "a", buffering=1)

def log(msg: str):
    ts   = time.strftime("%Y-%m-%dT%H:%M:%S")
    line = f"[{ts}] {msg}"
    print(line, flush=True)
    _log_fh.write(line + "\n")

# ── Recorder lifecycle ────────────────────────────────────────────────────────

def stop_recorder():
    global _rec_proc
    if _rec_proc is None or _rec_proc.poll() is not None:
        return
    log("  Stopping recorder...")
    _rec_proc.terminate()
    try:
        _rec_proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        _rec_proc.kill()
        _rec_proc.wait()
    log(f"  Recording saved → {_rec_out}")
    _rec_proc = None

def _shutdown(signum, frame):
    log(f"  Signal {signum} received — cleaning up...")
    stop_recorder()
    try:
        sock.close()
    except Exception:
        pass
    _log_fh.close()
    sys.exit(1)

signal.signal(signal.SIGINT,  _shutdown)
signal.signal(signal.SIGTERM, _shutdown)

# ── lume VNC discovery ────────────────────────────────────────────────────────

def get_vnc_info():
    """Return (host, port, password) for VM_NAME from 'lume ls --format json', or None."""
    try:
        import json
        out = subprocess.check_output(["lume", "ls", "--format", "json"], stderr=subprocess.DEVNULL, text=True)
        json_lines = [l for l in out.splitlines() if not l.startswith("[20")]
        vms = json.loads("\n".join(json_lines))
        for vm in vms:
            if vm.get("name") != VM_NAME:
                continue
            vnc = vm.get("vncUrl") or ""
            m = re.search(r'vnc://:([^@]+)@([^:]+):(\d+)', vnc)
            if m:
                return m.group(2), int(m.group(3)), m.group(1)
    except Exception:
        pass
    return None

# ── DES auth ──────────────────────────────────────────────────────────────────

def rfb_des_encrypt(challenge16: bytes, password: str) -> bytes:
    pw  = (password.encode("latin-1") + b"\x00" * 8)[:8]
    key = bytearray(8)
    for i, b in enumerate(pw):
        b = ((b >> 1) & 0x55) | ((b << 1) & 0xAA)
        b = ((b >> 2) & 0x33) | ((b << 2) & 0xCC)
        b = ((b >> 4) & 0x0F) | ((b << 4) & 0xF0)
        key[i] = b & 0xFF
    return subprocess.check_output(
        ["openssl", "enc", "-des-ecb", "-provider", "legacy", "-provider", "default",
         "-K", key.hex(), "-nosalt", "-nopad"],
        input=challenge16, stderr=subprocess.DEVNULL)

def read_exactly(s, n):
    buf = b""
    while len(buf) < n:
        chunk = s.recv(n - len(buf))
        if not chunk:
            raise ConnectionError("VNC socket closed")
        buf += chunk
    return buf

# ── X11 keysyms ───────────────────────────────────────────────────────────────

KEY_RETURN      = 0xFF0D
KEY_RIGHT       = 0xFF53
KEY_LEFT_ALT    = 0xFFE9  # Option on macOS; perceived as Cmd inside the VM
KEY_LEFT_CMD    = 0xFFEB  # Command on macOS
KEY_LEFT_SHIFT  = 0xFFE1

def keysym(ch: str) -> int:
    return ord(ch)

# ── RFB connect ───────────────────────────────────────────────────────────────

HOST = PORT = VNC_PASS = None
sock = None

def connect():
    global sock, HOST, PORT, VNC_PASS

    # Discover current VNC URL from lume (handles port changes after proxy restart)
    info = get_vnc_info()
    if not info:
        raise RuntimeError(f"No VNC URL found for VM '{VM_NAME}' in 'lume ls'")
    HOST, PORT, VNC_PASS = info
    log(f"Connecting to VNC {HOST}:{PORT} ...")
    s = socket.create_connection((HOST, PORT), timeout=30)

    read_exactly(s, 12)                 # server version
    s.sendall(b"RFB 003.008\n")

    num_types = struct.unpack("B", read_exactly(s, 1))[0]
    if num_types == 0:
        n = struct.unpack(">I", read_exactly(s, 4))[0]
        raise RuntimeError(f"Server refused: {read_exactly(s, n)}")
    types = list(read_exactly(s, num_types))
    if 2 not in types:
        raise RuntimeError(f"VNC auth not offered: {types}")
    s.sendall(b"\x02")

    challenge = read_exactly(s, 16)
    s.sendall(rfb_des_encrypt(challenge, VNC_PASS))
    result = struct.unpack(">I", read_exactly(s, 4))[0]
    if result != 0:
        raise RuntimeError("VNC authentication failed")

    s.sendall(b"\x01")                  # ClientInit shared=1

    width  = struct.unpack(">H", read_exactly(s, 2))[0]
    height = struct.unpack(">H", read_exactly(s, 2))[0]
    read_exactly(s, 16)                 # pixel format (ignored)
    name_len = struct.unpack(">I", read_exactly(s, 4))[0]
    name = read_exactly(s, name_len).decode("utf-8", errors="replace")
    log(f"  Connected: '{name}'  {width}x{height}")

    sock = s

# ── Key sending ────────────────────────────────────────────────────────────────

def send_key(ks: int, down: bool):
    sock.sendall(struct.pack(">BBxxI", 4, 1 if down else 0, ks))

def press(ks: int):
    send_key(ks, True)
    time.sleep(0.05)
    send_key(ks, False)
    time.sleep(0.05)

def type_string(s: str):
    for ch in s:
        press(keysym(ch))

def key_combo(modifier: int, key: int):
    send_key(modifier, True)
    time.sleep(0.05)
    press(key)
    send_key(modifier, False)
    time.sleep(0.05)

def key_combo3(mod1: int, mod2: int, key: int):
    send_key(mod1, True)
    time.sleep(0.05)
    send_key(mod2, True)
    time.sleep(0.05)
    press(key)
    send_key(mod2, False)
    time.sleep(0.05)
    send_key(mod1, False)
    time.sleep(0.05)

def reconnect_with_backoff(max_attempts=40, delay=3):
    """Poll lume ls until the VM has a VNC URL, then connect.
    The VM appears 'stopped' briefly during boot manager → recovery OS transition."""
    for attempt in range(1, max_attempts + 1):
        log(f"  Reconnect attempt {attempt}/{max_attempts}...")
        try:
            connect()
            log("  Reconnected successfully.")
            return
        except Exception as e:
            log(f"    Failed: {e}")
            time.sleep(delay)
    raise RuntimeError("Could not reconnect after max attempts — VM may have stopped.")

def wait_sec(n, label=""):
    """Pure sleep — no socket activity during waits.
    The lume proxy survives idle waits; it only drops on unexpected message types."""
    log(f"  Waiting {n}s{' ' + label if label else ''}...")
    time.sleep(n)

def ensure_connected():
    """After a wait, always reconnect fresh.
    The lume VNC session becomes stale when the boot manager transitions to the
    recovery OS (same port, but server-side session resets). Sending on a stale
    connection gets BrokenPipe. Reconnecting gives a valid session."""
    global sock
    log("  Refreshing VNC connection...")
    try:
        sock.close()
    except Exception:
        pass
    reconnect_with_backoff()

def reconnect_and_retry(fn, *args):
    """Call fn(*args), reconnecting once if the pipe breaks."""
    try:
        fn(*args)
    except (BrokenPipeError, ConnectionError, OSError):
        log("  Connection lost during keypress — waiting for new proxy...")
        reconnect_with_backoff()
        fn(*args)

# ── SIP disable sequence ──────────────────────────────────────────────────────

log("\n=== SIP disable sequence ===")
log(f"  VM: {VM_NAME}  user: {VM_USER}  macos: {_macos_version}  record: {args.record}  viewer: {args.viewer}")

# Step 1: Connect to VNC (boot options screen, 1920×1080).
log("  Connecting to VNC (boot options screen)...")
time.sleep(5)
reconnect_with_backoff()
time.sleep(2)

# Open VNC viewer if requested. NOTE: macOS Screen Sharing sends SetPixelFormat
# on connect and will kill lume's VNC proxy. Use a compatible viewer.
if args.viewer:
    info = get_vnc_info()
    if info:
        _vh, _vp, _vpw = info
        vnc_url = f"vnc://:{_vpw}@{_vh}:{_vp}"
        log(f"  Opening VNC viewer: {vnc_url}")
        subprocess.Popen(["open", vnc_url])

# Step 3: Right Right Enter — navigate boot options to "Options", enter recovery.
# Right+Right are sent with reconnect_and_retry. Enter is sent separately so a
# connection drop between the Rights and Enter doesn't silently swallow Enter.
log("  Boot options: Right Right Enter → Options...")
reconnect_and_retry(press, KEY_RIGHT)
time.sleep(0.2)
reconnect_and_retry(press, KEY_RIGHT)
time.sleep(2)
log("  Sending Enter to select Options...")
reconnect_and_retry(press, KEY_RETURN)
log("  Enter sent.")

# Step 4: Reconnect to the recovery OS VNC session.
# Wait 5s before closing so the Right+Right+Enter key events are fully
# processed by the server before we tear down the connection.
wait_sec(5, "for boot options Enter to be processed")
log("  Closing stale socket, reconnecting to recovery OS session...")
try:
    sock.close()
except Exception:
    pass
reconnect_with_backoff()
time.sleep(2)

# Step 5: Wait for recovery environment to fully load and language chooser to appear.
# VNC framebuffer is live after this wait — start recorder here if enabled.
wait_sec(60, "for recovery OS to fully load" + (" and language chooser to appear" if TAHOE else ""))

if args.record:
    log(f"  Starting recorder → {_rec_out}")
    _rec_proc = subprocess.Popen(
        [sys.executable, os.path.join(os.path.dirname(__file__), "vnc-record.py"),
         VM_NAME, _rec_out],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    time.sleep(2)  # give recorder a moment to connect before sending keys

if TAHOE:
    log("  Accepting language selector (Enter)...")
    reconnect_and_retry(press, KEY_RETURN)
    wait_sec(15, "after language selection")

# Step 6: open Terminal via Opt+Shift+T — Opt is perceived as Cmd inside the VM.
log("  Opening Terminal (Opt+Shift+T)...")
reconnect_and_retry(key_combo3, KEY_LEFT_ALT, KEY_LEFT_SHIFT, keysym('t'))
wait_sec(12, "for Terminal to open")

# Step 7: csrutil disable
log("  Running: csrutil disable")
reconnect_and_retry(type_string, "csrutil disable")
reconnect_and_retry(press, KEY_RETURN)
wait_sec(5, "for csrutil prompt")

# Step 8: confirm
log("  Confirming (y)...")
reconnect_and_retry(press, keysym('y'))
reconnect_and_retry(press, KEY_RETURN)
wait_sec(1)

# Step 9: username
log(f"  Username: {VM_USER}")
reconnect_and_retry(type_string, VM_USER)
reconnect_and_retry(press, KEY_RETURN)
wait_sec(1)

# Step 10: password
log("  Password...")
reconnect_and_retry(type_string, VM_PASS_STR)
reconnect_and_retry(press, KEY_RETURN)
wait_sec(1)

# Step 11: halt
log("  Halting VM...")
reconnect_and_retry(type_string, "halt")
reconnect_and_retry(press, KEY_RETURN)
wait_sec(15, "for VM to halt")

log("Key sequence complete.")
try:
    sock.close()
except Exception:
    pass

# Stop recorder before lume stop — VM is halted so VNC will have dropped,
# but terminate cleanly to let ffmpeg finalise the file.
stop_recorder()

# Force-stop the VM via lume in case halt left a black-screen VNC session.
log("  Stopping VM via lume...")
subprocess.run(["lume", "stop", VM_NAME], capture_output=True)

log("  Done.")
_log_fh.close()
