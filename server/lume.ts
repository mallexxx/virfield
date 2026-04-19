/**
 * Lume API client.
 *
 * Uses lume's HTTP API (localhost:7777) when available, with CLI fallback
 * for operations not exposed over HTTP (create, clone, delete, set, ipsw).
 *
 * Call ensureLumeServe() once at server startup to start the daemon.
 */

import { spawn, execFile, execSync } from 'child_process';
import { promisify } from 'util';

const execFileAsync = promisify(execFile);

const LUME_API = 'http://localhost:7777';
const LUME_BIN = '/opt/homebrew/bin/lume';

// ── Lume serve daemon ─────────────────────────────────────────────────────────

let serveProc: ReturnType<typeof spawn> | null = null;

// Serialises restarts — prevents N concurrent withLumeRestart callers from all racing
// through ensureLumeServe at once and killing lume serve (and VMs) multiple times.
let restartLock: Promise<void> | null = null;

async function isLumeServeUp(): Promise<boolean> {
  try {
    const resp = await fetch(`${LUME_API}/lume/host/status`, { signal: AbortSignal.timeout(2000) });
    return resp.ok;
  } catch {
    return false;
  }
}

/** Returns true if a lume serve OS process is alive (regardless of HTTP health). */
export function isLumeServeProcessAlive(): boolean {
  try {
    const pids = execSync(`pgrep -f 'lume serve' 2>/dev/null || true`).toString().trim();
    return pids.length > 0;
  } catch {
    return false;
  }
}

async function doEnsureLumeServe(): Promise<void> {
  // Re-check under the lock — a previous waiter may have already brought serve back up.
  if (await isLumeServeUp()) return;

  // withLumeRestart only calls us when the serve process is gone (not just slow).
  // Double-check: if process is somehow alive, bail — killing it would kill running VMs.
  if (isLumeServeProcessAlive()) {
    console.log('[lume] doEnsureLumeServe: serve process alive — skipping restart');
    return;
  }

  // Serve process is gone — safe to spawn a new one.
  console.log('[lume] lume serve process not found — starting new instance');

  // Clean up stale managed reference if any.
  if (serveProc) {
    serveProc.kill('SIGKILL');
    serveProc = null;
    await sleep(500);
  }

  console.log('[lume] Starting lume serve...');
  serveProc = spawn(LUME_BIN, ['serve'], {
    // detached: true so lume serve (and its managed VMs) survive tsx watch
    // server restarts. The process lives in its own process group.
    detached: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  // Unref so the node event loop doesn't wait for this child to exit.
  // The lume serve process will keep running independently.
  serveProc.unref();

  let portConflict = false;
  serveProc.stdout?.on('data', (d: Buffer) => process.stdout.write(`[lume-serve] ${d}`));
  serveProc.stderr?.on('data', (d: Buffer) => {
    const text = d.toString();
    if (text.includes('already in use')) portConflict = true;
    process.stderr.write(`[lume-serve] ${text}`);
  });
  serveProc.on('exit', (code) => {
    console.log(`[lume] serve process exited: ${code}`);
    serveProc = null;
  });

  // Wait up to 8 s for it to be healthy.
  for (let i = 0; i < 16; i++) {
    await sleep(500);
    if (await isLumeServeUp()) {
      console.log('[lume] serve ready');
      return;
    }
    if (portConflict) {
      // Our spawn lost the race; an external instance is starting — keep polling.
      continue;
    }
  }
  throw new Error('lume serve did not start in time');
}

export async function ensureLumeServe(): Promise<void> {
  // Fast path: serve is already up.
  for (let i = 0; i < 3; i++) {
    if (await isLumeServeUp()) return;
    if (i < 2) await sleep(500);
  }

  // Slow path: serialise — if a restart is already underway, wait for it then return.
  // This prevents N concurrent withLumeRestart callers from each independently killing
  // and restarting lume serve (which would kill any running VMs multiple times).
  if (restartLock) {
    await restartLock;
    return;
  }
  restartLock = doEnsureLumeServe();
  try {
    await restartLock;
  } finally {
    restartLock = null;
  }
}

export function stopLumeServe() {
  serveProc?.kill('SIGTERM');
  serveProc = null;
}

/**
 * Force-kill the lume serve process (including externally spawned ones) and
 * restart it. WARNING: this kills ALL running VMs managed by lume serve.
 * Only call when lume serve is frozen AND the running VMs are already unusable.
 */
export async function forceRestartLumeServe(): Promise<void> {
  console.log('[lume] forceRestartLumeServe: killing lume serve process...');

  // Kill our managed process reference if any.
  if (serveProc) {
    serveProc.kill('SIGKILL');
    serveProc = null;
  }

  // Also kill any externally spawned lume serve process via pgrep.
  try {
    const pids = execSync(`pgrep -f 'lume serve' 2>/dev/null || true`).toString().trim();
    if (pids) {
      for (const pid of pids.split('\n').filter(Boolean)) {
        try { execSync(`kill -9 ${pid} 2>/dev/null || true`); } catch { /* ignore */ }
      }
      console.log(`[lume] forceRestartLumeServe: killed pids: ${pids}`);
    }
  } catch { /* ignore */ }

  // Invalidate caches so first request after restart fetches fresh data.
  vmListHttpCache.expiresAt = 0;
  vmListHttpCache.inflight = null;
  vmListCliCache.expiresAt = 0;
  vmListCliCache.inflight = null;

  await sleep(1000);
  await ensureLumeServe();
}

// ── Response coalescing cache ─────────────────────────────────────────────────
//
// Multiple frontend pollers hit /api/vms every 3-4s. Each call tries lume serve
// HTTP (10s timeout). When lume serve is slow (e.g. its SSH-status goroutine is
// hanging on a TCP timeout), these piled-up fetch requests fill lume's accept
// queue and freeze it entirely.
//
// Fix: coalesce concurrent calls to the same endpoint into a single in-flight
// fetch, and cache results for VM_LIST_CACHE_TTL_MS. All concurrent callers get
// the same result with zero extra lume requests.

const VM_LIST_CACHE_TTL_MS = 2000; // 2s — matches frontend poll interval

interface CacheEntry<T> {
  value: T;
  expiresAt: number;
  inflight: Promise<T> | null;
}

// Separate caches: HTTP API and CLI fetch from different code paths.
// CLI cache is the reliable fallback — must never be poisoned by HTTP failures.
const vmListHttpCache: CacheEntry<LumeVM[]> = { value: [], expiresAt: 0, inflight: null };
const vmListCliCache:  CacheEntry<LumeVM[]> = { value: [], expiresAt: 0, inflight: null };

async function coalesced<T>(cache: CacheEntry<T>, fetch: () => Promise<T>): Promise<T> {
  const now = Date.now();
  if (now < cache.expiresAt) return cache.value;   // fresh cache hit
  if (cache.inflight) return cache.inflight;        // coalesce to existing request
  cache.inflight = fetch().then(v => {
    cache.value = v;
    cache.expiresAt = Date.now() + VM_LIST_CACHE_TTL_MS;
    cache.inflight = null;
    return v;
  }).catch(err => {
    cache.inflight = null;
    throw err;
  });
  return cache.inflight;
}

// ── HTTP API wrappers ─────────────────────────────────────────────────────────

/**
 * Detect a network-level fetch failure (lume serve not running / crashed / frozen).
 * - TypeError "fetch failed": TCP connection refused (lume not running)
 * - DOMException "TimeoutError": AbortSignal.timeout fired (lume frozen/unresponsive)
 */
function isNetworkError(err: unknown): boolean {
  if (err instanceof TypeError && String(err).includes('fetch failed')) return true;
  if (err instanceof DOMException && err.name === 'TimeoutError') return true;
  return false;
}

/**
 * Run an API call; if lume serve has crashed, restart it and retry once.
 *
 * Key distinction:
 *   DEAD  (TCP refused / process gone) → restart lume serve, then retry.
 *   ALIVE but slow/overloaded          → do NOT restart (would kill running VMs);
 *                                        just re-throw so callers get a fast error.
 *
 * lume serve becomes temporarily unresponsive when a VM is booting (high load).
 * Restarting it in that window would SIGKILL it, taking the VM with it.
 * Callers already handle transient errors gracefully (e.g. falling back to DB state).
 */
async function withLumeRestart<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (err) {
    if (isNetworkError(err)) {
      if (isLumeServeProcessAlive()) {
        // Serve process exists — it's overloaded, not dead.  Don't restart.
        throw err;
      }
      console.log('[lume] fetch failed and serve process gone — restarting lume serve and retrying...');
      await ensureLumeServe();
      return await fn();
    }
    throw err;
  }
}

async function lumeGet<T>(path: string): Promise<T> {
  return withLumeRestart(async () => {
    const resp = await fetch(`${LUME_API}${path}`, { signal: AbortSignal.timeout(10_000) });
    if (!resp.ok) throw new Error(`lume API ${path}: ${resp.status} ${await resp.text()}`);
    return resp.json() as Promise<T>;
  });
}

async function lumePost<T>(path: string, body?: object): Promise<T> {
  return withLumeRestart(async () => {
    const resp = await fetch(`${LUME_API}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(30_000),
    });
    if (!resp.ok) throw new Error(`lume API POST ${path}: ${resp.status} ${await resp.text()}`);
    return resp.json() as Promise<T>;
  });
}

// ── VM types ──────────────────────────────────────────────────────────────────

export interface LumeVM {
  name: string;
  os: string;
  status: string;
  cpuCount: number;
  memorySize: number;   // bytes
  diskSize: { allocated: number; total: number };
  display: string;
  ipAddress: string | null;
  vncUrl: string | null;
  sshAvailable: boolean | null;
  locationName: string;
  sharedDirectories: string[] | null;
  networkMode: string;
  provisioningOperation: string | null;
  downloadProgress: number | null;
}

export interface HostStatus {
  status: string;
  version: string;
  vm_count: number;
  max_vms: number;
  available_slots: number;
}

export interface StorageLocation {
  name: string;
  path: string;
}

// ── lume ls --format json — used for CLI fallback and VNC URL ────────────────
//
// lume ls text output truncates long VNC URLs (long passwords push the port off
// the end of the fixed-width column). --format json is always complete.

/** Fetch all VMs via `lume ls --format json`. Safe when HTTP API is stuck. Coalesced + cached. */
export async function listVMsCLI(): Promise<LumeVM[]> {
  return coalesced(vmListCliCache, async () => {
    try {
      const { stdout } = await execFileAsync(LUME_BIN, ['ls', '--format', 'json'], { timeout: 10_000 });
      return JSON.parse(stdout) as LumeVM[];
    } catch {
      return [];
    }
  });
}

// ── VMs running via direct `lume run` CLI (not tracked by lume serve) ────────
//
// When `03-disable-sip.sh` or other scripts start a VM with `lume run` directly,
// lume serve is not notified. These VMs show as "stopped" in lume ls and the HTTP
// API even though they're running. Detect them by scanning the process list.

/** Names of VMs currently running via a direct `lume run <name>` CLI process. */
export function listDirectRunVMs(): Set<string> {
  try {
    // pgrep -a lists all lume processes with their full command lines
    const out = execSync('pgrep -a lume 2>/dev/null || true', { encoding: 'utf8', timeout: 3000 });
    const running = new Set<string>();
    for (const line of out.split('\n')) {
      // Match: <pid> /path/to/lume run <vmname> [flags...]
      const m = line.match(/lume run (\S+)/);
      if (m) running.add(m[1]);
    }
    return running;
  } catch {
    return new Set();
  }
}

// ── VNC URL — from lume ls --format json (text output truncates long passwords) ──

export async function getVNCUrl(name: string): Promise<string | null> {
  try {
    const vms = await listVMsCLI();
    return vms.find(v => v.name === name)?.vncUrl ?? null;
  } catch {
    return null;
  }
}

// ── VM operations via HTTP API ────────────────────────────────────────────────

/**
 * List VMs via HTTP API. Coalesces concurrent callers so the UI's 3-4s polling
 * loop doesn't pile up concurrent fetch requests against lume serve.
 */
export async function listVMs(): Promise<LumeVM[]> {
  return coalesced(vmListHttpCache, () => lumeGet<LumeVM[]>('/lume/vms'));
}

export async function getVM(name: string): Promise<LumeVM> {
  return lumeGet<LumeVM>(`/lume/vms/${encodeURIComponent(name)}`);
}

export async function startVM(name: string, opts: {
  noDisplay?: boolean;
  sharedDir?: string;
} = {}): Promise<void> {
  await lumePost(`/lume/vms/${encodeURIComponent(name)}/run`, {
    noDisplay: opts.noDisplay ?? true,
    sharedDirectories: opts.sharedDir ? [{ hostPath: opts.sharedDir, readOnly: false }] : undefined,
  });
}

export async function stopVM(name: string): Promise<void> {
  // Stop can take 30–90s (lume sends SIGINT and waits for VM to shut down gracefully).
  // Fallback chain when lume serve HTTP is stuck (e.g. overloaded during VM boot):
  //   1. lume HTTP API (90 s timeout, bypasses withLumeRestart to avoid duplicate stops)
  //   2. SSH into the VM → `sudo shutdown -h now`  (bypasses lume entirely)
  //   lume CLI `stop` is NOT used as fallback — it also talks to lume serve HTTP.
  try {
    const resp = await fetch(`${LUME_API}/lume/vms/${encodeURIComponent(name)}/stop`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      signal: AbortSignal.timeout(90_000),
    });
    if (!resp.ok) throw new Error(`lume API POST /lume/vms/${name}/stop: ${resp.status} ${await resp.text()}`);
  } catch (err) {
    if (isNetworkError(err)) {
      // lume serve HTTP is stuck — try SSH shutdown directly.
      // lume CLI `stop` also goes through lume serve so it's no help here.
      // Note: lume's sshAvailable field is strict (requires full login); we always
      // try SSH directly since the daemon runs even at the login screen.
      console.log(`[lume] stopVM HTTP failed — trying SSH shutdown for ${name}`);
      const vms = await listVMsCLI();
      const vm = vms.find(v => v.name === name);
      const ip = vm?.ipAddress;
      if (ip) {
        try {
          const { sshExec } = await import('./ssh.js');
          await sshExec(ip, 'sudo shutdown -h now', 15_000);
          console.log(`[lume] SSH shutdown sent to ${name} at ${ip}`);
          return;
        } catch (sshErr) {
          console.warn(`[lume] SSH shutdown failed for ${name}:`, sshErr);
          throw new Error(`Cannot stop ${name}: lume serve is unresponsive and SSH shutdown failed (${sshErr})`);
        }
      }
      throw new Error(`Cannot stop ${name}: lume serve is unresponsive and VM has no IP address`);
    }
    throw err;
  }
}

export async function getHostStatus(): Promise<HostStatus> {
  return lumeGet<HostStatus>('/lume/host/status');
}

export async function listStorageLocations(): Promise<StorageLocation[]> {
  return lumeGet<StorageLocation[]>('/lume/config/locations');
}

// ── CLI-only operations ───────────────────────────────────────────────────────

async function lumeCLI(...args: string[]): Promise<string> {
  const { stdout, stderr } = await execFileAsync(LUME_BIN, args, { timeout: 120_000 });
  if (stderr && !stdout) throw new Error(stderr.trim());
  return stdout.trim();
}

export async function createVM(opts: {
  name: string;
  ipsw?: string;
  cpu?: number;
  memory?: string;
  diskSize?: string;
  display?: string;
  storage?: string;
  unattended?: string;
}): Promise<void> {
  const args = ['create', opts.name, '--os', 'macos'];
  if (opts.ipsw) args.push('--ipsw', opts.ipsw);
  if (opts.cpu) args.push('--cpu', String(opts.cpu));
  if (opts.memory) args.push('--memory', opts.memory);
  if (opts.diskSize) args.push('--disk-size', opts.diskSize);
  if (opts.display) args.push('--display', opts.display);
  if (opts.storage) args.push('--storage', opts.storage);
  if (opts.unattended) args.push('--unattended', opts.unattended);
  await lumeCLI(...args);
}

export async function cloneVM(sourceName: string, destName: string): Promise<void> {
  await lumeCLI('clone', sourceName, destName);
}

export async function deleteVM(name: string): Promise<void> {
  // lume delete prompts for confirmation — use echo to pipe 'y'
  await new Promise<void>((resolve, reject) => {
    const proc = spawn(LUME_BIN, ['delete', name], { stdio: ['pipe', 'pipe', 'pipe'] });
    proc.stdin?.write('y\n');
    proc.stdin?.end();
    proc.on('exit', (code) => {
      if (code === 0) resolve();
      else reject(new Error(`lume delete exited ${code}`));
    });
  });
}

export async function setVMConfig(name: string, opts: {
  cpu?: number;
  memory?: string;
  diskSize?: string;
  display?: string;
}): Promise<void> {
  const args = ['set', name];
  if (opts.cpu !== undefined) args.push('--cpu', String(opts.cpu));
  if (opts.memory) args.push('--memory', opts.memory);
  if (opts.diskSize) args.push('--disk-size', opts.diskSize);
  if (opts.display) args.push('--display', opts.display);
  await lumeCLI(...args);
}

export async function getIPSWUrl(): Promise<string> {
  return lumeCLI('ipsw');
}

export async function listImages(): Promise<string[]> {
  const out = await lumeCLI('images');
  // Parse tabular output — lines after header
  return out.split('\n').slice(1).filter(Boolean);
}

export async function addStorageLocation(name: string, path: string): Promise<void> {
  await lumeCLI('config', 'storage', 'add', name, path);
}

export async function removeStorageLocation(name: string): Promise<void> {
  await lumeCLI('config', 'storage', 'remove', name);
}

// ── Utilities ─────────────────────────────────────────────────────────────────

function sleep(ms: number) { return new Promise(r => setTimeout(r, ms)); }

/**
 * Poll until VM has an IP address (or timeout).
 */
export async function waitForIP(name: string, timeoutMs = 180_000): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const vm = await getVM(name);
      if (vm.ipAddress) return vm.ipAddress;
    } catch {
      // VM may not be visible yet
    }
    await sleep(3000);
  }
  throw new Error(`Timed out waiting for IP for VM ${name}`);
}

/**
 * Poll until VM status matches desired state.
 */
export async function waitForStatus(name: string, desiredStatus: string, timeoutMs = 180_000): Promise<LumeVM> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const vm = await getVM(name);
      if (vm.status === desiredStatus) return vm;
    } catch {
      // ignore
    }
    await sleep(2000);
  }
  throw new Error(`Timed out waiting for VM ${name} to be ${desiredStatus}`);
}
