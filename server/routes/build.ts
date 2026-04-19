/**
 * Build pipeline routes.
 *
 * POST /api/vms/build-golden            — run build-golden-vm.sh (orchestrator)
 * GET  /api/vms/active-builds           — list in-flight build processes
 * POST /api/vms/:id/stop-build          — kill the build process for a VM
 * GET  /api/vms/:id/build-state         — read state.json from LOG_BASE/<id>-latest/
 * GET  /api/vms/:id/recordings          — list .mp4 files from all log dirs for this VM
 * GET  /api/vms/:id/screenshots         — list .png/.jpg files from all log dirs
 * GET  /api/vms/:id/build-log           — read build.log from latest log dir
 * GET  /api/media                       — serve a recording/screenshot file (path-validated)
 */

import { Router, Request, Response } from 'express';
import { spawn, ChildProcess } from 'child_process';
import { readdirSync, statSync, existsSync, readFileSync, writeFileSync, mkdirSync } from 'fs';
import { join, resolve, basename } from 'path';
import { homedir } from 'os';
import { SCRIPTS_DIR, LOG_BASE, STATE_DIR, RECORDINGS_DIR, VMSHARE } from '../config.js';
import * as db from '../db.js';
import { buildStageArgs, STAGE_ORDER, StageKey, PROVISION_TOOLS, STATE_KEY_TO_DB_STAGE } from '../stages.js';

export const buildRouter = Router();

// ── In-memory active build tracking ──────────────────────────────────────────
// Keyed by goldenVm name. Cleared on process exit.

interface ActiveBuildEntry {
  pid: number;
  proc: ChildProcess;
  goldenVm: string;
  baseVm: string;
  nosipVm: string;
  startedAt: string;
}

export const activeBuildMap = new Map<string, ActiveBuildEntry>();

/**
 * All VM names currently involved in a build.
 * Combines in-memory tracked processes with DB-persisted jobs (e.g. from before
 * a server restart, or builds whose process can't be tracked by PID).
 */
export function activeBuildVmIds(): Set<string> {
  const ids = new Set<string>();
  // In-memory (processes we spawned this session)
  for (const b of activeBuildMap.values()) {
    ids.add(b.goldenVm);
    ids.add(b.baseVm);
    ids.add(b.nosipVm);
  }
  // DB-persisted running jobs (survive server restarts)
  for (const row of db.listRunningBuildJobs()) {
    ids.add(row.golden_vm);
    ids.add(row.base_vm);
    ids.add(row.nosip_vm);
  }
  return ids;
}

/**
 * Restore build tracking from DB on server startup.
 * Checks if persisted PIDs are still alive; marks dead ones as failed in DB.
 * Also checks state.json — if state says "running" the build may be alive even
 * if the tracked PID is dead (e.g. build script spawned aria2c and exited).
 */
export function restoreBuildTracking(): void {
  for (const row of db.listRunningBuildJobs()) {
    const pid = row.pid;
    let pidAlive = false;
    if (pid) {
      try { process.kill(pid, 0); pidAlive = true; } catch { /* process gone */ }
    }

    // Also check state.json — if it says "running" (top-level OR any stage)
    // there may be active sub-processes (e.g. aria2c) even if the orchestrator
    // PID exited between server restarts.
    let stateRunning = false;
    try {
      const stateFile = join(STATE_DIR, `${row.golden_vm}.json`);
      const st = JSON.parse(readFileSync(stateFile, 'utf8'));
      const anyStageRunning =
        st?.stages && typeof st.stages === 'object' &&
        Object.values(st.stages as Record<string, { status: string }>).some(s => s.status === 'running');
      stateRunning = st?.status === 'running' || Boolean(anyStageRunning);
    } catch { /* no state file or parse error — ignore */ }

    if (!pidAlive && !stateRunning) {
      console.log(`[restore] Build for ${row.golden_vm} PID ${pid ?? '?'} is dead — marking failed`);
      db.finishBuildJob(row.golden_vm, 'failed');
    } else {
      if (!pidAlive && stateRunning) {
        // Orchestrator exited but sub-process (e.g. aria2c) is still active.
        // Re-upsert the row as running (it may have been set to failed by a prior restart).
        db.upsertBuildJob(row.golden_vm, row.base_vm, row.nosip_vm, null);
      }
      console.log(`[restore] Build for ${row.golden_vm} still active (pid alive: ${pidAlive}, state running: ${stateRunning})`);
    }
    // We can't get a ChildProcess handle for a pre-existing PID,
    // so we don't add to activeBuildMap — but activeBuildVmIds() reads DB.
  }
}

// ── Helper: read state.json for a VM ─────────────────────────────────────────
// Location: STATE_DIR/<vm-name>.json (persistent per-VM file, written by scripts)

export function readBuildState(vmId: string): Record<string, unknown> | null {
  const stateFile = join(STATE_DIR, `${vmId}.json`);
  if (!existsSync(stateFile)) return null;
  try {
    return JSON.parse(readFileSync(stateFile, 'utf8'));
  } catch {
    return null;
  }
}

// ── Helper types ──────────────────────────────────────────────────────────────

interface MediaFile {
  path: string;
  name: string;
  size: number;
  mtime: number;
  dir: string;
}

// ── Helper: scan RECORDINGS_DIR for .mp4/.mov files belonging to a VM ────────
// Files are named <ts>-<vm>-recording.mp4 by _lib.sh.

function scanRecordings(vmId: string): MediaFile[] {
  const results: MediaFile[] = [];
  if (!existsSync(RECORDINGS_DIR)) return results;
  try {
    const files = readdirSync(RECORDINGS_DIR).filter(f => {
      const ext = f.toLowerCase().split('.').pop() ?? '';
      return (ext === 'mp4' || ext === 'mov') && f.includes(vmId);
    });
    for (const f of files) {
      const fullPath = join(RECORDINGS_DIR, f);
      try {
        const s = statSync(fullPath);
        results.push({ path: fullPath, name: f, size: s.size, mtime: s.mtimeMs, dir: RECORDINGS_DIR });
      } catch { /* skip */ }
    }
  } catch { /* skip */ }
  results.sort((a, b) => b.mtime - a.mtime);
  return results;
}

// ── Helper: scan log dirs for screenshots (.png/.jpg) ────────────────────────

function scanScreenshots(vmId: string): MediaFile[] {
  const results: MediaFile[] = [];
  if (!existsSync(LOG_BASE)) return results;
  try {
    const vmDirs = readdirSync(LOG_BASE).filter(e => e.includes(vmId) && e !== `${vmId}-latest`);
    for (const dir of vmDirs) {
      const dirPath = join(LOG_BASE, dir);
      try {
        if (!statSync(dirPath).isDirectory()) continue;
        for (const f of readdirSync(dirPath)) {
          const ext = f.toLowerCase().split('.').pop() ?? '';
          if (!['png', 'jpg', 'jpeg'].includes(ext)) continue;
          const fullPath = join(dirPath, f);
          try {
            const s = statSync(fullPath);
            results.push({ path: fullPath, name: f, size: s.size, mtime: s.mtimeMs, dir: dirPath });
          } catch { /* skip */ }
        }
      } catch { /* skip */ }
    }
  } catch { /* skip */ }
  results.sort((a, b) => b.mtime - a.mtime);
  return results;
}

// ── GET /api/vms/provision-tools ─────────────────────────────────────────────

buildRouter.get('/vms/provision-tools', (_req: Request, res: Response) => {
  res.json(PROVISION_TOOLS);
});

// ── POST /api/vms/build-golden ────────────────────────────────────────────────
// Runs build-golden-vm.sh (all 4 phases) as a background process.
// Body: { ipsw, xcode?, tools?, baseVm?, nosipVm?, goldenVm?, record?, startPhase? }

buildRouter.post('/vms/build-golden', (req: Request, res: Response) => { try {
  const {
    ipsw,
    xcode,
    tools,
    record = false,
    installMissing = false,
    startPhase,
    cpu,
    memory,
    disk,
    display,
    goldenVm = 'golden',
  } = req.body as {
    ipsw: string;
    xcode?: string;
    tools?: string;
    record?: boolean;
    installMissing?: boolean;
    startPhase?: number;
    cpu?: number;
    memory?: string;
    disk?: string;
    display?: string;
    goldenVm?: string;
  };

  if (!ipsw) return void res.status(400).json({ error: 'ipsw required (path or "latest")' });

  const orchestrator = join(SCRIPTS_DIR, 'build-golden-vm.sh');
  if (!existsSync(orchestrator)) {
    return void res.status(400).json({
      error: `Orchestrator not found: ${orchestrator}. Set VMCONSOLE_SCRIPTS_DIR.`,
    });
  }

  // Prevent duplicate builds for the same golden VM (or any of its intermediates).
  const alreadyBuilding = activeBuildVmIds();
  // Derive the intermediate names to check all three
  const _prefix0 = goldenVm.replace(/-golden$/, '');
  if ([goldenVm, `${_prefix0}-base`, `${_prefix0}-nosip`].some(id => alreadyBuilding.has(id))) {
    return void res.status(409).json({ error: `Build for '${goldenVm}' is already running. Stop it first.` });
  }

  // Always derive base/nosip names from goldenVm so all 3 VMs share the same prefix.
  // e.g. macos-15.4-golden → macos-15.4-base, macos-15.4-nosip
  const prefix = goldenVm.replace(/-golden$/, '');
  const baseVm  = `${prefix}-base`;
  const nosipVm = `${prefix}-nosip`;

  const args: string[] = ['--ipsw', ipsw];
  if (xcode)           args.push('--xcode', xcode);
  if (tools)           args.push('--tools', tools);
  if (record)          args.push('--record');
  if (installMissing)  args.push('--install-missing');
  if (startPhase)      args.push('--start-phase', String(startPhase));
  if (cpu)             args.push('--cpu', String(cpu));
  if (memory)          args.push('--memory', memory);
  if (disk)            args.push('--disk', disk);
  if (display)         args.push('--display', display);
  // build-golden-vm.sh uses --vm for the golden VM name and derives
  // base/nosip names automatically (e.g. macos-15-golden → macos-15-base, macos-15-nosip).
  // Do NOT pass --base-vm / --nosip-vm / --golden-vm — the script doesn't accept them.
  args.push('--vm', goldenVm);
  args.push('--vmshare', VMSHARE);

  // Seed all three VM rows + stage rows (golden gets all 4 stages for tracking;
  // base/nosip are intermediate VMs — just register them so they appear in the list)
  const finalGolden = goldenVm;
  db.upsertVM({ id: finalGolden, tag: 'golden' });
  db.upsertVM({ id: baseVm,  tag: 'dev' });
  db.upsertVM({ id: nosipVm, tag: 'dev' });
  for (const stage of STAGE_ORDER) {
    db.setStageStatus(finalGolden, stage, 'pending');
  }

  // Write a fresh state.json so any stale "failed" result from a prior run
  // doesn't confuse the UI on the first poll after starting this build.
  try {
    mkdirSync(STATE_DIR, { recursive: true });
    writeFileSync(
      join(STATE_DIR, `${finalGolden}.json`),
      JSON.stringify({ vm: finalGolden, status: 'running', stages: {}, started: new Date().toISOString() }),
      'utf8',
    );
  } catch { /* non-fatal */ }

  // Use detached: true so the build process survives server restarts (tsx watch
  // reloads). Without detached, Node sends SIGTERM to child processes on exit,
  // which kills a running build mid-download/provision whenever the server is reloaded.
  const proc = spawn('bash', [orchestrator, 'run', ...args], {
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: true,
  });
  // Unref so Node's event loop doesn't wait for this long-running child.
  proc.unref();

  const startedAt = new Date().toISOString();
  activeBuildMap.set(finalGolden, { pid: proc.pid!, proc, goldenVm: finalGolden, baseVm, nosipVm, startedAt });
  db.upsertBuildJob(finalGolden, baseVm, nosipVm, proc.pid!);

  proc.stdout?.on('data', (d: Buffer) => process.stdout.write(`[build:${finalGolden}] ${d}`));
  proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[build:${finalGolden}] ${d}`));
  proc.on('exit', (code) => {
    console.log(`[build:${finalGolden}] exited: ${code}`);
    activeBuildMap.delete(finalGolden);
    db.finishBuildJob(finalGolden, code === 0 ? 'done' : 'failed');
    // Final sync from state.json into DB
    const state = readBuildState(finalGolden);
    if (state?.stages && typeof state.stages === 'object') {
      syncStateToDB(finalGolden, state.stages as Record<string, { status: string; error?: string }>);
    }
  });

  res.json({ message: 'Build started', goldenVm: finalGolden, pid: proc.pid, args });
} catch (err) { res.status(500).json({ error: String(err) }); } });

// ── GET /api/vms/active-builds ───────────────────────────────────────────────
// Returns all in-flight build processes (PID + VM name triplet).

buildRouter.get('/vms/active-builds', (_req: Request, res: Response) => {
  // Merge in-memory builds (this session) + DB-persisted running jobs
  const seen = new Set<string>();
  const builds: Array<{ pid: number | null; goldenVm: string; baseVm: string; nosipVm: string; startedAt: string; vmIds: string[] }> = [];

  for (const b of activeBuildMap.values()) {
    seen.add(b.goldenVm);
    builds.push({ pid: b.pid, goldenVm: b.goldenVm, baseVm: b.baseVm, nosipVm: b.nosipVm, startedAt: b.startedAt, vmIds: [b.goldenVm, b.baseVm, b.nosipVm] });
  }
  for (const row of db.listRunningBuildJobs()) {
    if (!seen.has(row.golden_vm)) {
      builds.push({ pid: row.pid, goldenVm: row.golden_vm, baseVm: row.base_vm, nosipVm: row.nosip_vm, startedAt: row.started_at, vmIds: [row.golden_vm, row.base_vm, row.nosip_vm] });
    }
  }
  res.json(builds);
});

// ── POST /api/vms/:id/stop-build ──────────────────────────────────────────────
// Kill the build process that involves the given VM (golden, base, or nosip).

buildRouter.post('/vms/:id/stop-build', (req: Request, res: Response) => {
  const vmId = req.params.id;

  // Check in-memory first
  const memBuild = Array.from(activeBuildMap.values()).find(
    b => b.goldenVm === vmId || b.baseVm === vmId || b.nosipVm === vmId,
  );
  if (memBuild) {
    try {
      memBuild.proc.kill('SIGTERM');
    } catch { /* already dead */ }
    activeBuildMap.delete(memBuild.goldenVm);
    db.finishBuildJob(memBuild.goldenVm, 'stopped');
    console.log(`[build] Stopped build for ${memBuild.goldenVm} (pid ${memBuild.pid})`);
    return void res.json({ message: 'Build stopped', goldenVm: memBuild.goldenVm, pid: memBuild.pid });
  }

  // Fall back to DB-persisted job (e.g. build started before this server session)
  const dbJob = db.listRunningBuildJobs().find(
    r => r.golden_vm === vmId || r.base_vm === vmId || r.nosip_vm === vmId,
  );
  if (!dbJob) {
    return void res.status(404).json({ error: `No active build found for VM: ${vmId}` });
  }
  if (dbJob.pid) {
    try { process.kill(dbJob.pid, 'SIGTERM'); } catch { /* already dead */ }
  }
  db.finishBuildJob(dbJob.golden_vm, 'stopped');
  console.log(`[build] Stopped DB-tracked build for ${dbJob.golden_vm} (pid ${dbJob.pid ?? 'unknown'})`);
  res.json({ message: 'Build stopped', goldenVm: dbJob.golden_vm, pid: dbJob.pid });
});

// ── GET /api/vms/:id/build-state ──────────────────────────────────────────────

buildRouter.get('/vms/:id/build-state', (req: Request, res: Response) => {
  try {
    const state = readBuildState(req.params.id);
    if (!state) {
      return void res.json({ vm: req.params.id, status: 'no_state', stages: {} });
    }

    // If top-level status is failed/no_state but a sub-stage is still running
    // (e.g. aria2c download), promote the status to running so the UI shows progress.
    const anyStageRunning =
      state.stages && typeof state.stages === 'object' &&
      Object.values(state.stages as Record<string, { status: string }>).some(s => s.status === 'running');
    if ((state.status === 'failed' || state.status === 'no_state') && anyStageRunning) {
      state.status = 'running';
    }

    // G38: If state.json claims running but the PID is dead, mark as stale.
    // anyStageRunning from state.json is unreliable — a crashed script leaves
    // stages as "running" forever. PID liveness is the authoritative check.
    if (state.status === 'running' && typeof state.pid === 'number') {
      let pidAlive = false;
      try { process.kill(state.pid as number, 0); pidAlive = true; } catch { /* dead */ }
      if (!pidAlive) {
        state.status = 'stale';
      }
    }

    // Sync into DB — non-fatal if it fails (e.g. VM not yet in vms table)
    if (state.stages && typeof state.stages === 'object') {
      try {
        syncStateToDB(req.params.id, state.stages as Record<string, { status: string }>);
      } catch (syncErr) {
        console.warn(`[build-state] syncStateToDB failed for ${req.params.id}:`, syncErr);
      }
    }

    res.json(state);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/vms/:id/recordings ───────────────────────────────────────────────

buildRouter.get('/vms/:id/recordings', (req: Request, res: Response) => {
  res.json(scanRecordings(req.params.id));
});

// ── GET /api/vms/:id/screenshots ─────────────────────────────────────────────

buildRouter.get('/vms/:id/screenshots', (req: Request, res: Response) => {
  res.json(scanScreenshots(req.params.id));
});

// ── GET /api/vms/:id/build-log ────────────────────────────────────────────────

// ── GET /api/vms/:id/log-files ────────────────────────────────────────────────
// List per-run log files from LOG_BASE/<vmId>/ (new flat-file structure).

buildRouter.get('/vms/:id/log-files', (req: Request, res: Response) => {
  const dir = join(LOG_BASE, req.params.id);
  if (!existsSync(dir)) return void res.json([]);
  try {
    const files = readdirSync(dir)
      .filter(f => f.endsWith('.log') && f !== 'latest.log')
      .map(f => {
        const stat = statSync(join(dir, f));
        return { name: f, size: stat.size, mtime: stat.mtimeMs };
      })
      .sort((a, b) => b.mtime - a.mtime);
    res.json(files);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

buildRouter.get('/vms/:id/build-log', (req: Request, res: Response) => {
  const lines = req.query.lines ? Number(req.query.lines) : 2000;

  // New flat-file structure: LOG_BASE/<vmId>/<file> (file defaults to latest.log)
  const vmDir = join(LOG_BASE, req.params.id);
  if (existsSync(vmDir)) {
    const fileName = (req.query.file as string) || 'latest.log';
    // Prevent path traversal
    if (fileName.includes('/') || fileName.includes('..')) {
      return void res.status(400).json({ error: 'Invalid file name' });
    }
    const logFile = join(vmDir, fileName);
    if (!existsSync(logFile)) return void res.type('text/plain').send('');
    try {
      const content = readFileSync(logFile, 'utf8');
      res.type('text/plain').send(content.split('\n').slice(-lines).join('\n'));
      return;
    } catch (err) {
      return void res.status(500).json({ error: String(err) });
    }
  }

  // Fallback: old structure — state.json log dir or <vm>-latest symlink
  const state = readBuildState(req.params.id);
  const logDir = (state?.log ?? state?.log_dir) as string | undefined;
  const logFile = logDir
    ? join(logDir as string, 'build.log')
    : join(LOG_BASE, `${req.params.id}-latest`, 'build.log');
  if (!existsSync(logFile)) return void res.type('text/plain').send('');
  try {
    const content = readFileSync(logFile, 'utf8');
    res.type('text/plain').send(content.split('\n').slice(-lines).join('\n'));
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/media ────────────────────────────────────────────────────────────
// Serves a recording or screenshot file. Path must be within LOG_BASE.

buildRouter.get('/media', (req: Request, res: Response) => {
  const rawPath = req.query.path as string;
  if (!rawPath) return void res.status(400).json({ error: 'path query param required' });

  const resolved = resolve(rawPath);
  const allowed = [LOG_BASE, STATE_DIR, RECORDINGS_DIR].map(d => resolve(d));
  const inAllowed = allowed.some(d => resolved.startsWith(d + '/') || resolved === d);

  if (!inAllowed) {
    return void res.status(403).json({ error: 'Path outside allowed directories' });
  }

  if (!existsSync(resolved)) {
    return void res.status(404).json({ error: 'File not found' });
  }

  res.sendFile(resolved);
});

// ── Helper: sync state.json stage statuses into SQLite ───────────────────────

function syncStateToDB(vmId: string, stages: Record<string, { status: string; error?: string; substage?: string }>) {
  for (const [stateKey, info] of Object.entries(stages)) {
    const stage = STATE_KEY_TO_DB_STAGE[stateKey];
    if (!stage) continue;
    // Only sync terminal states. 'running' is set by the server when it starts a
    // stage and cleared by resetOrphanedStages on restart — never re-apply it from
    // state.json which can be stale (e.g. script crashed mid-stage).
    if (info.status === 'running' || info.status === 'pending') continue;
    const output = info.error ?? info.substage ?? undefined;
    db.setStageStatus(vmId, stage, info.status, output);
  }
}
