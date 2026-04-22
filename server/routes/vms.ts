/**
 * VM routes: list, get, start, stop, clone, delete, config, stages.
 */

import { Router, Request, Response } from 'express';
import { spawn } from 'child_process';
import * as lume from '../lume.js';
import * as db from '../db.js';
import { sshExec, checkSSH } from '../ssh.js';
import { closeTunnel } from '../tunnel-manager.js';
import { VMSHARE, SCRIPTS_DIR, LOG_BASE, STATE_DIR } from '../config.js';
import { STAGE_SCRIPT_MAP, STAGE_ORDER, StageKey, STAGE_STATE_KEY, buildStageArgs } from '../stages.js';
import { activeBuildVmIds, readBuildState } from './build.js';
import { join } from 'path';
import { existsSync, readdirSync, mkdirSync, writeFileSync, readFileSync, symlinkSync, unlinkSync, rmdirSync } from 'fs';
import { homedir } from 'os';
import * as tasks from '../tasks.js';
import type { ChildProcess } from 'child_process';

export const vmsRouter = Router();

// Track stage subprocesses spawned by runHostScript so Stop can kill them.
const activeStageProcs = new Map<string, { proc: ChildProcess; stage: string }>(); // key: vmId

// Background version-detection guard — prevents concurrent duplicate SSH queries
const detectingVersion = new Set<string>();

/** SSH into a running VM, fetch sw_vers output, cache in DB. Fire-and-forget. */
function detectAndCacheVersion(vmId: string, ip: string) {
  if (detectingVersion.has(vmId)) return;
  detectingVersion.add(vmId);
  sshExec(ip, 'sw_vers -productVersion', 8000)
    .then(({ stdout }) => {
      const version = stdout.trim();
      if (version) db.upsertVM({ id: vmId, macos_version: version });
    })
    .catch(() => { /* ignore — VM may not have SSH ready yet */ })
    .finally(() => detectingVersion.delete(vmId));
}

// ── GET /api/vms ──────────────────────────────────────────────────────────────

vmsRouter.get('/', async (_req: Request, res: Response) => {
  try {
    const dbVMs = db.listVMs();
    const dbMap = new Map(dbVMs.map(v => [v.id, v]));

    // Fetch lume VMs — try HTTP API first, fall back to CLI if HTTP is stuck.
    let lumeVMs: Awaited<ReturnType<typeof lume.listVMs>> = [];
    try {
      lumeVMs = await lume.listVMs();
    } catch (err) {
      console.warn('[vms] lume HTTP failed, trying CLI fallback:', String(err));
      try {
        lumeVMs = await lume.listVMsCLI();
        if (lumeVMs.length > 0) console.log(`[vms] CLI fallback returned ${lumeVMs.length} VM(s)`);
      } catch (cliErr) {
        console.warn('[vms] lume CLI fallback also failed:', String(cliErr));
      }
    }

    // Ensure any new lume VMs are seeded in SQLite
    for (const vm of lumeVMs) {
      if (!dbMap.has(vm.name)) {
        db.upsertVM({ id: vm.name });
        dbMap.set(vm.name, db.getVM(vm.name)!);
      }
    }

    // Detect VMs running via direct `lume run` CLI (not tracked by lume serve).
    // These show as "stopped" in lumeVMs even though they're actually running.
    const directRunVMs = lume.listDirectRunVMs();

    // Patch lumeVMs: override status for CLI-running VMs that lume serve missed.
    const patchedLumeVMs = lumeVMs.map(vm => {
      if (vm.status === 'stopped' && directRunVMs.has(vm.name)) {
        return { ...vm, status: 'running' };
      }
      return vm;
    });

    const lumeNames = new Set(patchedLumeVMs.map(v => v.name));

    // Compute building IDs early so we can annotate lume VMs too.
    const buildingIds = activeBuildVmIds();

    const result: Record<string, unknown>[] = patchedLumeVMs.map(vm => ({
      ...vm,
      meta: dbMap.get(vm.name) ?? null,
      stages: db.getStages(vm.name),
      building: buildingIds.has(vm.name),
    }));

    // Include DB-only VMs that are part of an active build (seeded but not yet
    // created in lume). Shows build progress in the VM list during Phase 1.
    for (const dbVm of dbVMs) {
      if (!lumeNames.has(dbVm.id) && buildingIds.has(dbVm.id)) {
        result.push({
          name: dbVm.id,
          status: 'not_created',
          ipAddress: null,
          vncUrl: null,
          cpuCount: 0,
          memorySize: 0,
          diskSize: { allocated: 0, total: 0 },
          display: '',
          locationName: '',
          sshAvailable: null,
          meta: dbMap.get(dbVm.id) ?? null,
          stages: db.getStages(dbVm.id),
          building: true,
        });
      }
    }

    // Include VMs from state.json files with status="running" that aren't already
    // visible. This catches builds started outside the console (direct script runs).
    const resultNames = new Set(result.map(v => v.name as string));
    if (existsSync(STATE_DIR)) {
      try {
        const stateFiles = readdirSync(STATE_DIR).filter(f => f.endsWith('.json'));
        for (const file of stateFiles) {
          const vmId = file.replace(/\.json$/, '');
          if (resultNames.has(vmId)) continue;
          const state = readBuildState(vmId);
          // Only show if the build itself is "running" AND the PID is still alive.
          // A stage stuck as "running" inside a failed/dead build is stale — don't surface it.
          if (state?.status !== 'running') continue;
          const pid = typeof state.pid === 'number' ? state.pid as number : null;
          if (pid) {
            try { process.kill(pid, 0); } catch { continue; } // PID dead → stale, skip
          }
          if (!dbMap.has(vmId)) db.upsertVM({ id: vmId });
          result.push({
            name: vmId,
            status: 'not_created',
            ipAddress: state.ip as string ?? null,
            vncUrl: null,
            cpuCount: 0,
            memorySize: 0,
            diskSize: { allocated: 0, total: 0 },
            display: '',
            locationName: '',
            sshAvailable: null,
            meta: db.getVM(vmId) ?? null,
            stages: db.getStages(vmId),
            building: true,
          });
          resultNames.add(vmId);
        }
      } catch (err) {
        console.warn('[vms] state.json scan failed:', String(err));
      }
    }

    // G29: Mark provisioning VMs with no active build as stale.
    // lume only sets "(stale)" explicitly for some stuck VMs; others stay in
    // "provisioning" indefinitely after a script crash. Any VM still in
    // "provisioning" that we're not actively building is treated as stuck.
    // G37: Exception — if state.json says status=running, a stage script is
    // actively using the VM (e.g. cloning during provision_vm/disable_sip).
    for (const entry of result) {
      if (entry.status === 'provisioning' && !buildingIds.has(entry.name as string)) {
        const state = readBuildState(entry.name as string);
        if (state?.status !== 'running') {
          entry.status = 'provisioning (stale)';
        }
      }
    }

    // Background: detect macOS version for running VMs that don't have it yet
    for (const vm of result) {
      if (vm.status === 'running' && (vm as { ipAddress?: string }).ipAddress && !(vm as { meta?: { macos_version?: string } }).meta?.macos_version) {
        detectAndCacheVersion(vm.name as string, (vm as { ipAddress: string }).ipAddress);
      }
    }

    res.json(result);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/vms/:id ──────────────────────────────────────────────────────────

// ── GET /api/vms/:id/vnc-url ──────────────────────────────────────────────────
// lume HTTP API always returns vncUrl=null; parse lume ls text output instead.

vmsRouter.get('/:id/vnc-url', async (req: Request, res: Response) => {
  try {
    const url = await lume.getVNCUrl(req.params.id);
    if (!url) return res.status(404).json({ error: 'VNC URL not available — is the VM running?' });
    res.json({ url });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

vmsRouter.get('/:id', async (req: Request, res: Response) => {
  try {
    const [vm, meta, stages] = await Promise.all([
      lume.getVM(req.params.id),
      db.getVM(req.params.id),
      db.getStages(req.params.id),
    ]);
    // Patch status for VMs running via direct CLI not tracked by lume serve.
    const directRunVMs = lume.listDirectRunVMs();
    const patchedVm = (vm.status === 'stopped' && directRunVMs.has(vm.name))
      ? { ...vm, status: 'running' }
      : vm;
    res.json({ ...patchedVm, meta: meta ?? null, stages });
  } catch (err) {
    res.status(404).json({ error: String(err) });
  }
});

// ── POST /api/vms/create ──────────────────────────────────────────────────────
// Must come before /:id routes so Express doesn't treat 'create' as an id.

vmsRouter.post('/create', async (req: Request, res: Response) => {
  try {
    const {
      name, ipsw, cpu = 4, memory = '8GB', diskSize = '80GB',
      display = '1920x1080', storage, tag = 'dev', unattended,
    } = req.body as {
      name: string; ipsw?: string; cpu?: number; memory?: string;
      diskSize?: string; display?: string; storage?: string;
      tag?: string; unattended?: string;
    };

    if (!name) return void res.status(400).json({ error: 'name required' });

    await lume.createVM({ name, ipsw, cpu, memory, diskSize, display, storage, unattended });
    // Extract macOS version from IPSW filename if known (e.g. UniversalMac_15.4_24E248_Restore.ipsw)
    const ipswVersion = ipsw ? (ipsw.split('/').pop() ?? '').match(/[_\-](\d+\.\d+(?:\.\d+)?)[_\-]/)?.[1] : null;
    db.upsertVM({ id: name, tag, macos_version: ipswVersion ?? undefined });
    res.json({ name, message: 'VM created' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/vms/:id/start ───────────────────────────────────────────────────

vmsRouter.post('/:id/start', (req: Request, res: Response) => {
  const vmId = req.params.id;
  const { noDisplay = true, sharedDir = VMSHARE } = req.body as {
    noDisplay?: boolean;
    sharedDir?: string;
  };

  // Respond immediately — don't block on lume.startVM which can take 10–30s
  // when lume serve is under load. The UI polls VM status to detect when it
  // transitions to running.
  db.touchVMRun(vmId);
  res.json({ message: 'VM start requested' });

  // Fire-and-forget: start the VM and write a progress log
  (async () => {
    try {
      await lume.startVM(vmId, { noDisplay, sharedDir });
      console.log(`[start] ${vmId} started via lume`);
    } catch (err) {
      console.error(`[start] lume.startVM failed for ${vmId}:`, err);
      // Don't give up — lume may have started the VM anyway (race with its own
      // internal timeout). writeStartLog will detect the running state via CLI.
    }
    writeStartLog(vmId).catch(e => console.warn('[start-log]', e));
  })();
});

/**
 * G40: After a manual VM start, write a timestamped log file to LOG_BASE/<vmId>/
 * so BuildLogPanel has something to show. Polls for IP and SSH status,
 * appending events as they happen. Updates latest.log symlink when done.
 */
async function writeStartLog(vmId: string): Promise<void> {
  const ts = new Date().toISOString().replace(/[:.]/g, '-').replace('T', '_').slice(0, 19);
  const vmDir = join(LOG_BASE, vmId);
  const logFile = join(vmDir, `${ts}-start.log`);
  const latestLink = join(vmDir, 'latest.log');

  try { mkdirSync(vmDir, { recursive: true }); } catch { /* exists */ }

  function append(line: string) {
    const ts2 = new Date().toISOString();
    const entry = `[${ts2}] ${line}\n`;
    try { writeFileSync(logFile, entry, { flag: 'a' }); } catch { /* ignore */ }
    process.stdout.write(`[start-log:${vmId}] ${line}\n`);
  }

  // Update latest.log symlink to point to this log
  function updateLatest() {
    try { unlinkSync(latestLink); } catch { /* may not exist */ }
    try { symlinkSync(logFile, latestLink); } catch { /* ignore */ }
  }

  append(`VM start requested`);
  updateLatest();

  // Wait for IP — try HTTP API first, fall back to CLI if HTTP is stuck.
  append('Waiting for IP address...');
  let ip: string | null = null;
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    await new Promise(r => setTimeout(r, 3000));
    try {
      let vm: Awaited<ReturnType<typeof lume.getVM>> | undefined;
      try {
        vm = await lume.getVM(vmId);
      } catch {
        // HTTP API stuck — try CLI fallback
        const all = await lume.listVMsCLI();
        vm = all.find(v => v.name === vmId);
      }
      if (!vm) continue;
      if (vm.status === 'stopped' || vm.status === 'not_created') {
        append(`VM stopped unexpectedly (status: ${vm.status})`);
        return;
      }
      if (vm.ipAddress) { ip = vm.ipAddress; break; }
    } catch { /* VM may not be visible yet */ }
  }

  if (!ip) {
    append('Timed out waiting for IP address');
    return;
  }
  append(`IP address: ${ip}`);

  // Log VNC URL immediately — don't make the user wait for SSH to see it.
  try {
    const vncUrl = await lume.getVNCUrl(vmId);
    if (vncUrl) {
      append(`VNC: ${vncUrl}`);
      append('VNC connected — macOS desktop appears after ~30–60s boot. SSH check running in background.');
    }
  } catch { /* best-effort */ }

  // Check SSH in background — don't block showing VNC
  append('Checking SSH availability...');
  const { checkSSH } = await import('../ssh.js');
  let sshOk = false;
  const sshDeadline = Date.now() + 90_000;
  while (Date.now() < sshDeadline) {
    await new Promise(r => setTimeout(r, 3000));
    try {
      sshOk = await checkSSH(ip);
      if (sshOk) break;
    } catch { /* ignore */ }
  }

  if (sshOk) {
    append(`SSH ready at ${ip}`);

    // Auto-heal: macOS disables auto-login after an unsafe shutdown.
    // If SSH is up but lume reports sshAvailable=false it means the VM is at the
    // login screen (auto-login broken). Re-enable it silently and reboot.
    try {
      const vms = await lume.listVMsCLI();
      const vm = vms.find(v => v.name === vmId);
      if (vm && vm.sshAvailable === false) {
        append('Auto-login disabled — re-enabling and rebooting...');
        const { sshExec: sshExecFn } = await import('../ssh.js');
        await sshExecFn(ip, 'sudo sysadminctl -autologin set -userName lume -password lume', 15_000);
        sshExecFn(ip, 'sudo reboot', 5_000).catch(() => {});
        append('Rebooting to apply auto-login...');
        return;
      }
    } catch { /* best-effort — don't fail the whole log */ }
  } else {
    // SSH unavailable via normal check — try directly anyway (lume check is strict).
    // If we can SSH in, re-enable auto-login (likely disabled after unsafe shutdown).
    try {
      const { sshExec: sshExecFn } = await import('../ssh.js');
      await sshExecFn(ip, 'echo ssh_ok', 10_000);
      append('SSH reachable (re-enabling auto-login and rebooting)...');
      await sshExecFn(ip, 'sudo sysadminctl -autologin set -userName lume -password lume', 15_000);
      sshExecFn(ip, 'sudo reboot', 5_000).catch(() => {});
      return;
    } catch {
      append('SSH not available — use VNC to interact with the VM');
    }
  }

}

// ── POST /api/vms/:id/stop ────────────────────────────────────────────────────

vmsRouter.post('/:id/stop', async (req: Request, res: Response) => {
  const vmId = req.params.id;
  closeTunnel(vmId);

  // Kill the stage subprocess tracked by runHostScript (if any).
  const activeStage = activeStageProcs.get(vmId);
  if (activeStage) {
    try { activeStage.proc.kill('SIGTERM'); } catch { /* already gone */ }
    activeStageProcs.delete(vmId);
    db.setStageStatus(vmId, activeStage.stage, 'failed', 'interrupted by stop');
  }

  // Kill the PID from the script's lock file and remove the lock dir so the
  // next run isn't blocked by a stale lock. The lock file PID is the actual
  // running process (bash wrapper child), not the Node-spawned bash wrapper.
  try {
    const lockDir = `/tmp/virfield-provision-${vmId}.lock`;
    const lockPidFile = `${lockDir}/pid`;
    if (existsSync(lockPidFile)) {
      const lockPid = parseInt(readFileSync(lockPidFile, 'utf8').trim(), 10);
      if (lockPid) {
        try { process.kill(lockPid, 'SIGTERM'); } catch { /* already gone */ }
      }
      try { unlinkSync(lockPidFile); } catch { /* ignore */ }
      try { rmdirSync(lockDir); } catch { /* ignore */ }
    }
  } catch { /* non-fatal */ }

  // Also kill the build/stage PID from state.json (covers orchestrator builds).
  const state = readBuildState(vmId);
  const buildPid = typeof state?.pid === 'number' ? state.pid as number : null;
  if (buildPid) {
    let pidAlive = false;
    try { process.kill(buildPid, 0); pidAlive = true; } catch { /* already gone */ }
    if (pidAlive) {
      try {
        process.kill(buildPid, 'SIGTERM');
        await new Promise(r => setTimeout(r, 1500));
      } catch { /* may have already exited during grace period */ }
    }
  }

  try {
    await lume.stopVM(vmId);
    res.json({ message: 'VM stopped' });
  } catch (err) {
    const msg = String(err);
    // If lume still can't stop it (VM deleted by script cleanup, or other error),
    // offer Force Delete as a last resort.
    if (msg.includes('provision') || msg.includes('not found') || msg.includes('404')) {
      return void res.status(409).json({ error: msg, canForce: true });
    }
    res.status(500).json({ error: msg });
  }
});

// ── POST /api/vms/:id/repair ──────────────────────────────────────────────────
// Re-enables auto-login and reboots when macOS disables it after an unsafe shutdown.
// SSH is available at the login screen even when lume reports ssh:false.

vmsRouter.post('/:id/repair', async (req: Request, res: Response) => {
  const vmId = req.params.id;
  try {
    const vms = await lume.listVMsCLI();
    const vm = vms.find(v => v.name === vmId);
    if (!vm || vm.status !== 'running') {
      return void res.status(409).json({ error: 'VM is not running' });
    }
    const ip = vm.ipAddress;
    if (!ip) return void res.status(409).json({ error: 'VM has no IP address yet' });

    // Re-enable auto-login. macOS disables it after unsafe shutdown.
    // The lume user is provisioned with passwordless sudo and password "lume".
    await sshExec(ip,
      'sudo sysadminctl -autologin set -userName lume -password lume',
      15_000,
    );
    // Reboot so the new auto-login setting takes effect cleanly.
    sshExec(ip, 'sudo reboot', 5_000).catch(() => { /* expected — connection drops */ });
    res.json({ message: 'Auto-login re-enabled, VM rebooting...' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/vms/:id/force-stop ─────────────────────────────────────────────
// Force-deletes a stuck provisioning VM. Lume has no force-stop without delete,
// so this removes the VM entirely. Used when normal stop returns 409.

vmsRouter.post('/:id/force-stop', async (req: Request, res: Response) => {
  try {
    closeTunnel(req.params.id);
    await lume.deleteVM(req.params.id);
    db.deleteVM(req.params.id);
    res.json({ message: 'VM force-deleted' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/vms/:id/clone ───────────────────────────────────────────────────

vmsRouter.post('/:id/clone', async (req: Request, res: Response) => {
  try {
    const { destName } = req.body as { destName?: string };
    if (!destName) return void res.status(400).json({ error: 'destName required' });
    await lume.cloneVM(req.params.id, destName);
    db.upsertVM({ id: destName, tag: 'run' });
    res.json({ name: destName });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── DELETE /api/vms/:id ───────────────────────────────────────────────────────

vmsRouter.delete('/:id', async (req: Request, res: Response) => {
  try {
    closeTunnel(req.params.id);
    await lume.deleteVM(req.params.id);
    db.deleteVM(req.params.id);
    res.json({ message: 'VM deleted' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── PATCH /api/vms/:id/config ─────────────────────────────────────────────────

vmsRouter.patch('/:id/config', async (req: Request, res: Response) => {
  try {
    const { cpu, memory, diskSize, display } = req.body as {
      cpu?: number; memory?: string; diskSize?: string; display?: string;
    };
    await lume.setVMConfig(req.params.id, { cpu, memory, diskSize, display });
    res.json({ message: 'Config updated' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── PATCH /api/vms/:id/meta ───────────────────────────────────────────────────

vmsRouter.patch('/:id/meta', (req: Request, res: Response) => {
  try {
    const { tag, macos_version, xcode_version, notes } = req.body as Partial<db.VMRow>;
    db.upsertVM({ id: req.params.id, tag, macos_version, xcode_version, notes });
    res.json({ message: 'Meta updated' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/vms/:id/stages ───────────────────────────────────────────────────

vmsRouter.get('/:id/stages', (req: Request, res: Response) => {
  res.json(db.getStages(req.params.id));
});

// ── DELETE /api/vms/:id/stages/:stage ────────────────────────────────────────
// Reset a single stage back to pending so it can be re-run.

vmsRouter.delete('/:id/stages/:stage', (req: Request, res: Response) => {
  db.setStageStatus(req.params.id, req.params.stage, 'pending', undefined);
  res.json({ message: 'Stage reset to pending' });
});

// ── POST /api/vms/:id/stages/:stage/run ──────────────────────────────────────
//
// Runs one of the 4 virfield pipeline phases on the HOST.
// Scripts live in VMCONSOLE_SCRIPTS_DIR and write state.json to VMCONSOLE_LOG_BASE/<vm>-latest/.
// Single-VM pipeline: all stages operate on the same VM (:id).
//
// Body (optional):
//   ipsw     — IPSW path (required for create_vm; defaults to 'latest')
//   xcode    — Xcode.app path (provision_vm only)
//   tools    — comma-separated tool IDs (provision_vm only; default 'all')
//   record   — boolean, enable VNC recording (default false)

vmsRouter.post('/:id/stages/:stage/run', async (req: Request, res: Response) => {
  const { id, stage } = req.params;

  if (!STAGE_ORDER.includes(stage as StageKey)) {
    return void res.status(400).json({ error: `Unknown stage: ${stage}. Valid: ${STAGE_ORDER.join(', ')}` });
  }

  const scriptFile = STAGE_SCRIPT_MAP[stage];
  if (!scriptFile) {
    return void res.status(400).json({ error: `No script mapped for stage '${stage}'` });
  }

  const fullPath = join(SCRIPTS_DIR, scriptFile);
  if (!existsSync(fullPath)) {
    return void res.status(400).json({
      error: `Script not found: ${fullPath}\n(set VMCONSOLE_SCRIPTS_DIR env var to ~/Developer/virfield/scripts)`,
    });
  }

  const {
    ipsw, xcode, tools, record = false,
  } = req.body as {
    ipsw?: string; xcode?: string; tools?: string; record?: boolean;
  };

  const vmNames = { goldenVm: id };
  const scriptArgs = buildStageArgs(stage as StageKey, vmNames, {
    ipsw, xcode, tools, record,
    vmshare: VMSHARE,
    // Each VM gets its own log dir symlinked as <vm>-latest
    logDir: undefined,  // let the script create its own timestamped dir
  });

  db.setStageStatus(id, stage, 'running');

  // Stamp state.json so syncStateToDB won't clobber the DB result with a stale
  // terminal status from a prior run when the script exits without writing state.
  try {
    const stateFile = join(STATE_DIR, `${id}.json`);
    if (existsSync(stateFile)) {
      const state = JSON.parse(readFileSync(stateFile, 'utf8')) as Record<string, unknown>;
      const stateKey = STAGE_STATE_KEY[stage];
      if (stateKey && state.stages && typeof state.stages === 'object') {
        const stages = state.stages as Record<string, unknown>;
        const prev = (stages[stateKey] as Record<string, unknown>) ?? {};
        stages[stateKey] = { label: prev.label, status: 'running' };
        // Clear top-level stale substage so the UI doesn't show the old substage text
        delete state.substage;
        writeFileSync(stateFile, JSON.stringify(state, null, 2), 'utf8');
      }
    }
  } catch { /* non-fatal */ }

  res.json({ message: `Stage ${stage} started`, status: 'running', script: scriptFile, args: scriptArgs });

  runHostScript(fullPath, scriptArgs, process.env, id, stage);
});

/**
 * Run a shell script on the host, capture combined stdout+stderr,
 * update stage status when done.
 */
function runHostScript(
  scriptPath: string,
  scriptArgs: string[],
  env: NodeJS.ProcessEnv | undefined,
  vmId: string,
  stage: string,
) {
  const stateFile = join(STATE_DIR, `${vmId}.json`);
  const scriptEnv = { ...env, STATE_FILE: stateFile };

  // Create a timestamped log file so BuildLogPanel can show this run's output.
  const ts = new Date().toISOString().replace(/[:.]/g, '-').replace('T', '_').slice(0, 19);
  const vmDir = join(LOG_BASE, vmId);
  const logFile = join(vmDir, `${ts}-${stage}.log`);
  const latestLink = join(vmDir, 'latest.log');
  try { mkdirSync(vmDir, { recursive: true }); } catch { /* exists */ }
  try { unlinkSync(latestLink); } catch { /* may not exist */ }
  try { symlinkSync(logFile, latestLink); } catch { /* ignore */ }

  const proc = spawn('bash', [scriptPath, ...scriptArgs], { env: scriptEnv, stdio: ['ignore', 'pipe', 'pipe'] });
  activeStageProcs.set(vmId, { proc, stage });
  let output = '';

  const onData = (d: Buffer) => {
    const text = d.toString();
    output += text;
    if (output.length > 65536) output = output.slice(-65536);
    try { writeFileSync(logFile, text, { flag: 'a' }); } catch { /* ignore */ }
  };

  proc.stdout?.on('data', onData);
  proc.stderr?.on('data', onData);

  proc.on('exit', (code) => {
    if (activeStageProcs.get(vmId)?.proc === proc) activeStageProcs.delete(vmId);
    const status = code === 0 ? 'done' : 'failed';
    db.setStageStatus(vmId, stage, status, output);
  });

  proc.on('error', (err) => {
    if (activeStageProcs.get(vmId)?.proc === proc) activeStageProcs.delete(vmId);
    const msg = String(err);
    db.setStageStatus(vmId, stage, 'failed', msg);
    try { writeFileSync(logFile, msg, { flag: 'a' }); } catch { /* ignore */ }
  });
}

// ── GET /api/vms/:id/logs/:source (fetch last N lines) ───────────────────────

vmsRouter.get('/:id/logs/:source', async (req: Request, res: Response) => {
  try {
    const vm = await lume.getVM(req.params.id);
    if (!vm.ipAddress) return void res.status(400).json({ error: 'VM not running' });

    const logPaths: Record<string, string> = {
      'app-console': '/tmp/app-console.log',
      'ui-tests': '/tmp/ui-tests.log',
      'peekaboo-mcp': '/tmp/peekaboo-mcp.log',
      'socat': '/tmp/socat.log',
    };
    const path = logPaths[req.params.source];
    if (!path) return void res.status(400).json({ error: 'Unknown log source' });

    const lines = req.query.lines ? Number(req.query.lines) : 500;
    const result = await sshExec(vm.ipAddress, `tail -n ${lines} "${path}" 2>/dev/null || echo ''`);
    res.type('text/plain').send(result.stdout);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/vms/:id/ssh-check ────────────────────────────────────────────────

vmsRouter.get('/:id/ssh-check', async (req: Request, res: Response) => {
  try {
    const vm = await lume.getVM(req.params.id);
    if (!vm.ipAddress) return void res.json({ available: false, reason: 'No IP' });
    const ok = await checkSSH(vm.ipAddress);
    res.json({ available: ok });
  } catch (err) {
    res.json({ available: false, reason: String(err) });
  }
});

// ── POST /api/vms/:id/ssh-open ────────────────────────────────────────────────
// Opens Terminal.app with an SSH session to the VM (macOS host only).

vmsRouter.post('/:id/ssh-open', async (req: Request, res: Response) => {
  try {
    const vm = await lume.getVM(req.params.id);
    if (!vm.ipAddress) return void res.status(400).json({ error: 'VM has no IP address' });
    const cmd = `ssh lume@${vm.ipAddress}`;
    const { effectiveSetting } = await import('./settings.js');
    const terminal = effectiveSetting('ssh_terminal') || 'Terminal';
    let script: string[];
    if (terminal === 'iTerm2' || terminal === 'iTerm') {
      script = [
        '-e', `tell application "iTerm2" to create window with default profile command "${cmd}"`,
        '-e', 'tell application "iTerm2" to activate',
      ];
    } else {
      script = [
        '-e', `tell application "Terminal" to do script "${cmd}"`,
        '-e', 'tell application "Terminal" to activate',
      ];
    }
    const proc = spawn('osascript', script, { stdio: 'ignore' });
    proc.unref();
    res.json({ message: `${terminal} opened`, cmd });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/golden/promote/:id ─────────────────────────────────────────────

vmsRouter.post('/:id/promote-golden', async (req: Request, res: Response) => {
  try {
    const meta = db.getVM(req.params.id);
    // Derive golden name from source: strip any trailing -clone-<ts> or -run-<ts> suffix,
    // then append -golden. Falls back to the source name itself if already named *-golden.
    const baseName = req.params.id.replace(/-(clone|run)-\d+$/, '');
    const goldenName = baseName.endsWith('-golden') ? baseName : `${baseName}-golden`;

    // Clone to golden name
    await lume.cloneVM(req.params.id, goldenName);
    db.upsertVM({ id: goldenName, tag: 'golden', macos_version: meta?.macos_version ?? null, xcode_version: meta?.xcode_version ?? null });

    const gid = db.promoteGolden(
      goldenName,
      meta?.macos_version ?? null,
      meta?.xcode_version ?? null,
      req.body?.notes,
    );

    res.json({ message: 'Promoted to golden', goldenId: gid });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/vms/:id/record/start ───────────────────────────────────────────
// Starts a VNC screen recording for a running VM using ffmpeg's vnc input.
// Output: ~/VMShare/recordings/<vmId>-<timestamp>.mp4

vmsRouter.post('/:id/record/start', async (req: Request, res: Response) => {
  const vmId = req.params.id;

  // Check if already recording
  const existing = tasks.getRecordingTask(vmId);
  if (existing) {
    return void res.status(409).json({ error: 'Already recording', taskId: existing.id, file: existing.meta?.file });
  }

  try {
    const vncUrl = await lume.getVNCUrl(vmId);
    if (!vncUrl) return void res.status(400).json({ error: 'VM not running or VNC not available' });

    // Parse vnc://IP:PORT?password=... → "IP:PORT"
    const u = new URL(vncUrl);
    const vncHost = u.hostname;
    const vncPort = u.port || '5900';

    const ts = new Date().toISOString().replace(/[:.]/g, '-').replace('T', '_').slice(0, 19);
    const recDir = join(homedir(), 'VMShare', 'recordings');
    mkdirSync(recDir, { recursive: true });
    const outFile = join(recDir, `${vmId}-${ts}.mp4`);

    const taskId = tasks.createTask('record', `Recording ${vmId}`, { vmId, file: outFile });

    const proc = spawn('ffmpeg', [
      '-f', 'vnc',
      '-i', `${vncHost}:${vncPort}`,
      '-vf', 'scale=1280:720',
      '-c:v', 'libx264',
      '-preset', 'ultrafast',
      '-r', '10',
      '-crf', '28',
      '-y', outFile,
    ], { stdio: ['ignore', 'pipe', 'pipe'] });

    tasks.attachProc(taskId, proc);

    proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[record:${vmId}] ${d}`));
    proc.on('exit', (code) => {
      if (code === 0 || code === null) {
        tasks.resolveTask(taskId);
        console.log(`[record] done: ${outFile}`);
      } else {
        tasks.failTask(taskId, `ffmpeg exited ${code}`);
        console.error(`[record] failed (exit ${code}): ${outFile}`);
      }
    });
    proc.on('error', (err) => {
      tasks.failTask(taskId, err);
      console.error('[record] spawn error:', err);
    });

    res.json({ message: 'Recording started', taskId, file: outFile });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/vms/:id/record/stop ────────────────────────────────────────────

vmsRouter.post('/:id/record/stop', (req: Request, res: Response) => {
  const vmId = req.params.id;
  const task = tasks.getRecordingTask(vmId);
  if (!task) return void res.status(404).json({ error: 'No active recording for this VM' });

  // Send SIGTERM — ffmpeg finalises the container on graceful stop
  try {
    (task as unknown as { proc?: { kill: (sig: string) => void } }).proc?.kill('SIGTERM');
    tasks.resolveTask(task.id);
    res.json({ message: 'Recording stopped', file: task.meta?.file });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/vms/:id/record/status ───────────────────────────────────────────

vmsRouter.get('/:id/record/status', (req: Request, res: Response) => {
  const vmId = req.params.id;
  const task = tasks.getRecordingTask(vmId);
  res.json({ recording: !!task, taskId: task?.id ?? null, file: task?.meta?.file ?? null });
});

// ── GET /api/tasks ────────────────────────────────────────────────────────────
// Returns all background tasks (copy-to-share, xip-extract, recordings).

vmsRouter.get('/tasks/list', (_req: Request, res: Response) => {
  res.json(tasks.listTasks());
});
