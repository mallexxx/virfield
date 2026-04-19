#!/usr/bin/env python3
"""
vnc-record.py  <vm_name> <output.mp4> [fps]

Connects to a lume VM via RFB VNC (auto-discovered from 'lume ls') and
records the framebuffer directly to a video file via ffmpeg rawvideo pipe.
No window capture — pure VNC stream.

Run alongside vnc-send-keys.py to capture the full SIP disable sequence.
Stop with Ctrl-C; ffmpeg finalises the file on exit.

Optional env vars:
  VNC_FPS   (default: 5)
"""
import sys, os, re, socket, struct, time, signal, subprocess

if len(sys.argv) < 3:
    print("usage: vnc-record.py <vm_name> <output.mp4> [fps]", file=sys.stderr)
    sys.exit(1)

VM_NAME = sys.argv[1]
OUT     = sys.argv[2]
FPS     = float(sys.argv[3]) if len(sys.argv) > 3 else float(os.environ.get("VNC_FPS", "5"))
INTERVAL = 1.0 / FPS

running = True
def _stop(sig, frame):
    global running
    running = False
signal.signal(signal.SIGTERM, _stop)
signal.signal(signal.SIGINT,  _stop)


# ── lume VNC discovery ────────────────────────────────────────────────────────

def get_vnc_info():
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


# ── VNC DES auth ──────────────────────────────────────────────────────────────

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


# ── RFB connect ───────────────────────────────────────────────────────────────

info = get_vnc_info()
if not info:
    print(f"No VNC URL found for '{VM_NAME}' in 'lume ls'", file=sys.stderr)
    sys.exit(1)
host, port, vnc_pass = info

print(f"Connecting to VNC {host}:{port} ...", flush=True)
sock = socket.create_connection((host, port), timeout=30)

read_exactly(sock, 12)
sock.sendall(b"RFB 003.008\n")

num_types = struct.unpack("B", read_exactly(sock, 1))[0]
if num_types == 0:
    n = struct.unpack(">I", read_exactly(sock, 4))[0]
    print(f"Server refused: {read_exactly(sock, n)}", file=sys.stderr)
    sys.exit(1)
types = list(read_exactly(sock, num_types))
if 2 not in types:
    print(f"VNC auth not offered: {types}", file=sys.stderr)
    sys.exit(1)
sock.sendall(b"\x02")

challenge = read_exactly(sock, 16)
sock.sendall(rfb_des_encrypt(challenge, vnc_pass))
if struct.unpack(">I", read_exactly(sock, 4))[0] != 0:
    print("VNC authentication failed", file=sys.stderr)
    sys.exit(1)

sock.sendall(b"\x01")  # ClientInit shared=1

width  = struct.unpack(">H", read_exactly(sock, 2))[0]
height = struct.unpack(">H", read_exactly(sock, 2))[0]

# Parse server's native pixel format — lume kills the proxy if we send
# SetPixelFormat or SetEncodings after the handshake, so we use the server's
# format as-is and convert to bgra ourselves.
pf = read_exactly(sock, 16)
bpp        = pf[0]          # bits-per-pixel (usually 32)
big_endian = pf[2]
true_color = pf[3]
r_max, g_max, b_max = struct.unpack_from(">HHH", pf, 4)
r_shift, g_shift, b_shift = pf[10], pf[11], pf[12]
bytes_per_pixel = bpp // 8

name_len = struct.unpack(">I", read_exactly(sock, 4))[0]
name = read_exactly(sock, name_len).decode("utf-8", errors="replace")
print(f"Connected: '{name}'  {width}x{height}  bpp={bpp} r_shift={r_shift} g_shift={g_shift} b_shift={b_shift}", flush=True)
print(f"Recording @ {FPS:.1f} fps  →  {OUT}", flush=True)

# Do NOT send SetPixelFormat or SetEncodings — lume drops the connection.


# ── pixel conversion helper ───────────────────────────────────────────────────

def pixels_to_bgra(raw: bytes, n_pixels: int) -> bytes:
    """Convert server-native pixel format to BGRA for ffmpeg."""
    out = bytearray(n_pixels * 4)
    for i in range(n_pixels):
        px_bytes = raw[i * bytes_per_pixel : i * bytes_per_pixel + bytes_per_pixel]
        if big_endian:
            px = int.from_bytes(px_bytes, "big")
        else:
            px = int.from_bytes(px_bytes, "little")
        r = (px >> r_shift) & r_max
        g = (px >> g_shift) & g_max
        b = (px >> b_shift) & b_max
        out[i*4]   = b
        out[i*4+1] = g
        out[i*4+2] = r
        out[i*4+3] = 255
    return bytes(out)


# ── ffmpeg pipe ───────────────────────────────────────────────────────────────

ffmpeg = subprocess.Popen(
    ["ffmpeg", "-y",
     "-f", "rawvideo", "-pix_fmt", "bgra",
     "-s", f"{width}x{height}",
     "-r", str(FPS),
     "-i", "pipe:0",
     "-c:v", "libx264", "-pix_fmt", "yuv420p",
     "-preset", "fast",
     OUT],
    stdin=subprocess.PIPE,
    stderr=subprocess.DEVNULL,
)


# ── Capture loop ──────────────────────────────────────────────────────────────

framebuf  = bytearray(width * height * 4)  # always BGRA
sock.settimeout(15)
frame_idx = 0

try:
    while running:
        t0 = time.monotonic()

        # Request full framebuffer update (incremental=0)
        sock.sendall(struct.pack(">BBHHHH", 3, 0, 0, 0, width, height))

        try:
            while True:
                msg_type = struct.unpack("B", read_exactly(sock, 1))[0]
                if msg_type == 0:    # FramebufferUpdate
                    break
                elif msg_type == 2:  # Bell — ignore
                    pass
                elif msg_type == 3:  # ServerCutText — discard
                    read_exactly(sock, 3)
                    n = struct.unpack(">I", read_exactly(sock, 4))[0]
                    read_exactly(sock, n)
                else:
                    print(f"Unexpected msg type {msg_type}, stopping", file=sys.stderr)
                    running = False
                    break

            if not running:
                break

            read_exactly(sock, 1)   # padding
            num_rects = struct.unpack(">H", read_exactly(sock, 2))[0]

            for _ in range(num_rects):
                rx, ry, rw, rh, enc = struct.unpack(">HHHHi", read_exactly(sock, 12))
                if enc != 0:
                    print(f"Non-raw encoding {enc} — skipping rect", file=sys.stderr)
                    continue
                raw = read_exactly(sock, rw * rh * bytes_per_pixel)
                bgra = pixels_to_bgra(raw, rw * rh)
                for row in range(rh):
                    src = row * rw * 4
                    dst = ((ry + row) * width + rx) * 4
                    framebuf[dst : dst + rw * 4] = bgra[src : src + rw * 4]

        except (socket.timeout, ConnectionError) as e:
            print(f"Socket error: {e} — retrying", file=sys.stderr)
            time.sleep(1)
            continue

        ffmpeg.stdin.write(framebuf)
        frame_idx += 1

        elapsed = time.monotonic() - t0
        sleep_t = INTERVAL - elapsed
        if sleep_t > 0:
            time.sleep(sleep_t)

finally:
    sock.close()
    ffmpeg.stdin.close()
    ffmpeg.wait()
    print(f"vnc-record: {frame_idx} frames → {OUT}", flush=True)
