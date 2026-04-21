/**
 * Socat tunnel manager — maintains SSH port-forwards from host to VM's
 * Peekaboo MCP port (7888).
 *
 * Each VM gets a unique host-side port (starting at 7900).
 * Tunnel state is persisted in SQLite so both the web console and MCP server
 * can reuse the same tunnel without opening duplicates.
 */

import { spawn, ChildProcess } from 'child_process';
import { getTunnel, setTunnel, clearTunnel, listTunnelPorts, clearAllTunnels } from './db.js';
import { homedir } from 'os';
import { existsSync } from 'fs';
import { join } from 'path';

const PEEKABOO_VM_PORT = 4040;
const TUNNEL_PORT_BASE = 7900;
const MAX_TUNNELS = 20;
const VM_USER = 'lume';

// In-memory map of live processes (not persisted — PIDs are stored in SQLite as a hint)
const liveTunnels = new Map<string, ChildProcess>();

// ── Port allocation ───────────────────────────────────────────────────────────

function findFreePort(): number {
  const used = new Set(listTunnelPorts());
  for (let p = TUNNEL_PORT_BASE; p < TUNNEL_PORT_BASE + MAX_TUNNELS; p++) {
    if (!used.has(p)) return p;
  }
  throw new Error('No free tunnel ports available');
}

// ── SSH private key path ──────────────────────────────────────────────────────

function sshKeyPath(): string | undefined {
  const candidates = [
    join(homedir(), '.ssh', 'id_ed25519'),
    join(homedir(), '.ssh', 'id_rsa'),
  ];
  return candidates.find(p => existsSync(p));
}

// ── Open tunnel ───────────────────────────────────────────────────────────────

export async function ensureTunnel(vmId: string, vmIp: string): Promise<{ localPort: number }> {
  // Check if we have a live tunnel
  const existing = getTunnel(vmId);
  if (existing && liveTunnels.has(vmId)) {
    return { localPort: existing.local_port };
  }

  // If DB says tunnel exists but process isn't live, clean up
  if (existing) clearTunnel(vmId);

  const localPort = findFreePort();

  // SSH -L port forward: localhost:localPort -> vmIp:PEEKABOO_VM_PORT
  const keyPath = sshKeyPath();
  const sshArgs = [
    '-N',                          // no remote command
    '-L', `${localPort}:127.0.0.1:${PEEKABOO_VM_PORT}`,
    '-o', 'StrictHostKeyChecking=no',
    '-o', 'UserKnownHostsFile=/dev/null',
    '-o', 'ExitOnForwardFailure=yes',
    '-o', 'ServerAliveInterval=10',
    '-o', 'ServerAliveCountMax=3',
    ...(keyPath ? ['-i', keyPath] : []),
    `${VM_USER}@${vmIp}`,
  ];

  const proc = spawn('ssh', sshArgs, { stdio: ['ignore', 'pipe', 'pipe'] });

  proc.stderr?.on('data', (d: Buffer) => {
    process.stderr.write(`[tunnel:${vmId}] ${d}`);
  });

  proc.on('exit', (code) => {
    console.log(`[tunnel:${vmId}] SSH exited: ${code}`);
    liveTunnels.delete(vmId);
    clearTunnel(vmId);
  });

  liveTunnels.set(vmId, proc);
  setTunnel(vmId, localPort, proc.pid);

  // Wait briefly for the tunnel to establish
  await new Promise(r => setTimeout(r, 500));

  return { localPort };
}

export function closeTunnel(vmId: string) {
  const proc = liveTunnels.get(vmId);
  if (proc) {
    proc.kill('SIGTERM');
    liveTunnels.delete(vmId);
  }
  clearTunnel(vmId);
}

export function closeAllTunnels() {
  for (const [vmId] of liveTunnels) {
    closeTunnel(vmId);
  }
}

/**
 * On server startup, clear any tunnel rows left over from a previous run.
 * Those PIDs are gone — the DB rows are stale and would block port allocation.
 */
export function cleanStaleTunnels() {
  clearAllTunnels();
  liveTunnels.clear();
}

export function getTunnelPort(vmId: string): number | null {
  const t = getTunnel(vmId);
  return t?.local_port ?? null;
}
