/**
 * Storage, IPSW, Xcode, and golden routes.
 */

import { Router, Request, Response } from 'express';
import { spawn } from 'child_process';
import { execFile } from 'child_process';
import { promisify } from 'util';
import { readdirSync, statSync, existsSync } from 'fs';
import * as tasks from '../tasks.js';
import https from 'https';
import { basename, join } from 'path';
import { homedir } from 'os';
import * as lume from '../lume.js';
import * as db from '../db.js';
import { VMSHARE } from '../config.js';

const { ensureLumeServe } = lume;

const execFileAsync = promisify(execFile);

export const storageRouter = Router();

// ── GET /api/storage ──────────────────────────────────────────────────────────

storageRouter.get('/storage', async (_req: Request, res: Response) => {
  try {
    const locations = await lume.listStorageLocations();

    const enriched = await Promise.all(locations.map(async (loc) => {
      const resolvedPath = loc.path.replace(/^~/, homedir());
      try {
        const { stdout } = await execFileAsync('df', ['-k', '-P', resolvedPath]);
        const lines = stdout.trim().split('\n');
        const parts = lines[lines.length - 1].split(/\s+/);
        const totalKB = parseInt(parts[1], 10);
        const usedKB = parseInt(parts[2], 10);
        const freeKB = parseInt(parts[3], 10);
        return { ...loc, resolvedPath, totalBytes: totalKB * 1024, usedBytes: usedKB * 1024, freeBytes: freeKB * 1024 };
      } catch {
        return { ...loc, resolvedPath, totalBytes: null, usedBytes: null, freeBytes: null };
      }
    }));

    res.json(enriched);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/storage ─────────────────────────────────────────────────────────

storageRouter.post('/storage', async (req: Request, res: Response) => {
  try {
    const { name, path } = req.body as { name: string; path: string };
    if (!name || !path) return void res.status(400).json({ error: 'name and path required' });
    await lume.addStorageLocation(name, path);
    res.json({ message: 'Storage location added' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── DELETE /api/storage/:name ─────────────────────────────────────────────────

storageRouter.delete('/storage/:name', async (req: Request, res: Response) => {
  try {
    await lume.removeStorageLocation(req.params.name);
    res.json({ message: 'Storage location removed' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/ipsw ─────────────────────────────────────────────────────────────

storageRouter.get('/ipsw', async (_req: Request, res: Response) => {
  try {
    const latestUrl = await lume.getIPSWUrl().catch(() => null);

    // Standard scan paths
    const scanPaths = [
      join(homedir(), 'Library', 'Application Support', 'VirtualBuddy', '_Downloads'),
      join(homedir(), 'Downloads'),
      VMSHARE,
      join(homedir(), 'Library', 'Caches', 'lume'),
    ];

    const seen = new Set<string>();
    const localFiles: Array<{ path: string; size: number; name: string; source: string }> = [];

    for (const dir of scanPaths) {
      if (!existsSync(dir)) continue;
      try {
        const files = readdirSync(dir).filter(f => f.toLowerCase().endsWith('.ipsw'));
        for (const f of files) {
          const fullPath = join(dir, f);
          if (seen.has(fullPath)) continue;
          seen.add(fullPath);
          const size = statSync(fullPath).size;
          localFiles.push({ path: fullPath, size, name: f, source: dir });
        }
      } catch { /* skip unreadable */ }
    }

    // Add registered IPSWs (user-pointed paths)
    for (const row of db.listRegisteredIPSWs()) {
      if (!seen.has(row.path) && existsSync(row.path)) {
        seen.add(row.path);
        localFiles.push({ path: row.path, size: row.size ?? 0, name: row.name, source: 'registered' });
      }
    }

    res.json({ latestUrl, localFiles });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/ipsw/catalog ─────────────────────────────────────────────────────
// Fetch available macOS IPSWs from api.ipsw.me for VirtualMac2,1 (Apple Silicon VMs).
// Returns one entry per major macOS version, newest first.

storageRouter.get('/ipsw/catalog', (_req: Request, res: Response) => {
  const url = 'https://api.ipsw.me/v4/device/VirtualMac2,1';

  const VERSION_NAMES: Record<string, string> = {
    '26': 'Tahoe', '15': 'Sequoia', '14': 'Sonoma',
    '13': 'Ventura', '12': 'Monterey', '11': 'Big Sur',
  };

  const req2 = https.get(url, { headers: { 'User-Agent': 'virfield/1.0' } }, (resp) => {
    let raw = '';
    resp.on('data', (chunk: Buffer) => { raw += chunk.toString(); });
    resp.on('end', () => {
      try {
        const data = JSON.parse(raw) as { firmwares?: Array<{ version: string; buildid: string; url: string; filesize: number; signed: boolean }> };
        const fws = (data.firmwares ?? []).sort((a, b) => {
          const av = a.version.split('.').map(Number);
          const bv = b.version.split('.').map(Number);
          for (let i = 0; i < 3; i++) {
            if ((bv[i] ?? 0) !== (av[i] ?? 0)) return (bv[i] ?? 0) - (av[i] ?? 0);
          }
          return 0;
        });

        // Keep only the latest entry per major version
        const seen = new Set<string>();
        const catalog: Array<{ version: string; buildId: string; url: string; sizeBytes: number; sizeGb: string; major: string; name: string; spec: string }> = [];
        for (const fw of fws) {
          const major = fw.version.split('.')[0];
          if (seen.has(major)) continue;
          seen.add(major);
          catalog.push({
            version: fw.version,
            buildId: fw.buildid,
            url: fw.url,
            sizeBytes: fw.filesize,
            sizeGb: (fw.filesize / 1e9).toFixed(1),
            major,
            name: VERSION_NAMES[major] ?? '',
            spec: VERSION_NAMES[major]?.toLowerCase() ?? fw.version, // e.g. "sequoia" or "15.4"
          });
        }
        res.json(catalog);
      } catch (err) {
        res.status(502).json({ error: `Failed to parse ipsw.me response: ${String(err)}` });
      }
    });
  });
  req2.on('error', (err) => res.status(502).json({ error: `ipsw.me fetch failed: ${String(err)}` }));
  req2.setTimeout(10000, () => { req2.destroy(); res.status(504).json({ error: 'ipsw.me timeout' }); });
});

// ── POST /api/ipsw/register ───────────────────────────────────────────────────

storageRouter.post('/ipsw/register', (req: Request, res: Response) => {
  try {
    const { path } = req.body as { path: string };
    if (!path) return void res.status(400).json({ error: 'path required' });
    if (!existsSync(path)) return void res.status(400).json({ error: `File not found: ${path}` });
    if (!path.toLowerCase().endsWith('.ipsw')) {
      return void res.status(400).json({ error: 'File must be an .ipsw' });
    }
    const size = statSync(path).size;
    const name = basename(path);
    db.registerIPSW(path, name, size);
    res.json({ message: 'IPSW registered', name, size });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── DELETE /api/ipsw/register ─────────────────────────────────────────────────

storageRouter.delete('/ipsw/register', (req: Request, res: Response) => {
  try {
    const { path } = req.body as { path: string };
    if (!path) return void res.status(400).json({ error: 'path required' });
    db.unregisterIPSW(path);
    res.json({ message: 'IPSW unregistered' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/ipsw/download ───────────────────────────────────────────────────
// Triggers a background download of the latest IPSW via lume.
// Progress is not tracked yet — fires and forgets, logs to server stdout.

storageRouter.post('/ipsw/download', async (req: Request, res: Response) => {
  try {
    const { url, destDir } = req.body as { url?: string; destDir?: string };
    const destination = destDir ?? VMSHARE;

    if (!url) {
      // Get latest URL from lume
      const latestUrl = await lume.getIPSWUrl();
      if (!latestUrl || !latestUrl.startsWith('http')) {
        return void res.status(400).json({ error: 'No URL provided and could not get latest from lume' });
      }
      return void triggerDownload(latestUrl, destination, res);
    }

    triggerDownload(url, destination, res);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

function triggerDownload(url: string, destDir: string, res: Response) {
  const name = basename(new URL(url).pathname);
  const destPath = join(destDir, name);

  if (existsSync(destPath)) {
    return res.status(409).json({ error: `Already exists: ${destPath}` });
  }

  // Use curl — shows progress in server logs, auto-resumes
  const proc = spawn('curl', ['-L', '-o', destPath, '--progress-bar', url], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[ipsw-dl] ${d}`));
  proc.on('exit', (code) => {
    if (code === 0) {
      const size = statSync(destPath).size;
      db.registerIPSW(destPath, name, size);
      console.log(`[ipsw-dl] Download complete: ${destPath}`);
    } else {
      console.error(`[ipsw-dl] Download failed (exit ${code})`);
    }
  });

  res.json({ message: 'Download started', name, destPath, pid: proc.pid });
}

// ── GET /api/xcode ────────────────────────────────────────────────────────────
// Returns deduplicated app list: same version in /Applications and ~/VMShare
// is merged into one entry with inVMShare: true.

storageRouter.get('/xcode', async (_req: Request, res: Response) => {
  try {
    type AppEntry = { path: string; version: string | null; name: string; inVMShare: boolean; inApplications: boolean };
    const byVersion = new Map<string, AppEntry>();
    const noVersionApps: AppEntry[] = [];

    async function scanDir(dir: string) {
      if (!existsSync(dir)) return;
      try {
        const entries = readdirSync(dir).filter(f => f.match(/^Xcode.*\.app$/));
        for (const f of entries) {
          const fullPath = join(dir, f);
          let version: string | null = null;
          try {
            const { stdout } = await execFileAsync('mdls', ['-name', 'kMDItemVersion', '-raw', fullPath]);
            version = stdout.trim() || null;
          } catch { /* skip */ }

          const isVMShare = dir === VMSHARE || fullPath.startsWith(VMSHARE);
          const isApplications = dir === '/Applications';

          if (version) {
            const existing = byVersion.get(version);
            if (existing) {
              // Merge: mark whichever location applies
              if (isVMShare) existing.inVMShare = true;
              if (isApplications) existing.inApplications = true;
            } else {
              byVersion.set(version, {
                path: fullPath,
                version,
                name: f,
                inVMShare: isVMShare,
                inApplications: isApplications,
              });
            }
          } else {
            noVersionApps.push({ path: fullPath, version: null, name: f, inVMShare: isVMShare, inApplications: isApplications });
          }
        }
      } catch { /* skip */ }
    }

    await scanDir('/Applications');
    await scanDir(VMSHARE);

    // Prefer the /Applications path as canonical for the entry if both exist,
    // but set inVMShare so UI can show the badge without a "Copy" button.
    const apps = [...byVersion.values(), ...noVersionApps];

    // XIP archives — scan VMShare + registered paths
    const archives: Array<{ path: string; size: number; name: string; source: string }> = [];
    const seenXIPs = new Set<string>();

    function addXIP(fullPath: string, source: string) {
      if (seenXIPs.has(fullPath) || !existsSync(fullPath)) return;
      seenXIPs.add(fullPath);
      archives.push({ path: fullPath, size: statSync(fullPath).size, name: basename(fullPath), source });
    }

    if (existsSync(VMSHARE)) {
      try {
        readdirSync(VMSHARE)
          .filter(f => f.endsWith('.xip'))
          .forEach(f => addXIP(join(VMSHARE, f), VMSHARE));
      } catch { /* skip */ }
    }

    for (const row of db.listRegisteredXIPs()) {
      addXIP(row.path, 'registered');
    }

    res.json({ apps, archives });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/xcode/copy-to-share ─────────────────────────────────────────────

storageRouter.post('/xcode/copy-to-share', async (req: Request, res: Response) => {
  try {
    const { sourcePath } = req.body as { sourcePath: string };
    if (!sourcePath) return void res.status(400).json({ error: 'sourcePath required' });
    if (!existsSync(sourcePath)) return void res.status(400).json({ error: `Path not found: ${sourcePath}` });

    const destPath = join(VMSHARE, basename(sourcePath));
    if (existsSync(destPath)) {
      return void res.status(409).json({ error: `Already exists in VMShare: ${destPath}` });
    }

    const taskId = tasks.createTask('copy-to-share', `Copy ${basename(sourcePath)} → VMShare`, { src: sourcePath, dest: destPath });
    res.json({ message: 'Copy started (may take several minutes for large Xcode.app)', destPath, taskId });
    execFileAsync('cp', ['-r', sourcePath, destPath])
      .then(() => tasks.resolveTask(taskId))
      .catch(err => {
        console.error('[xcode copy]', err);
        tasks.failTask(taskId, err);
      });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/xcode/extract-xip ──────────────────────────────────────────────
// Runs `xip --expand <path>` in ~/VMShare. The resulting Xcode.app appears
// in VMShare and is picked up by the next /api/xcode scan.

storageRouter.post('/xcode/extract-xip', async (req: Request, res: Response) => {
  try {
    const { path: xipPath } = req.body as { path: string };
    if (!xipPath) return void res.status(400).json({ error: 'path required' });
    if (!existsSync(xipPath)) return void res.status(400).json({ error: `File not found: ${xipPath}` });

    const taskId = tasks.createTask('xip-extract', `Extract ${basename(xipPath)}`, { src: xipPath, dest: VMSHARE });
    res.json({ message: 'XIP extraction started (may take several minutes)', xipPath, destDir: VMSHARE, taskId });

    const proc = spawn('xip', ['--expand', xipPath], {
      cwd: VMSHARE,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    tasks.attachProc(taskId, proc);
    proc.stdout?.on('data', (d: Buffer) => process.stdout.write(`[xip] ${d}`));
    proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[xip] ${d}`));
    proc.on('exit', (code) => {
      if (code === 0) {
        tasks.resolveTask(taskId);
        console.log(`[xip] extraction complete: ${xipPath}`);
      } else {
        tasks.failTask(taskId, `xip exited ${code}`);
        console.error(`[xip] extraction failed (exit ${code}): ${xipPath}`);
      }
    });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/xcode/register-xip ─────────────────────────────────────────────

storageRouter.post('/xcode/register-xip', (req: Request, res: Response) => {
  try {
    const { path: xipPath } = req.body as { path: string };
    if (!xipPath) return void res.status(400).json({ error: 'path required' });
    if (!existsSync(xipPath)) return void res.status(400).json({ error: `File not found: ${xipPath}` });
    if (!xipPath.endsWith('.xip')) return void res.status(400).json({ error: 'File must be a .xip' });
    const size = statSync(xipPath).size;
    db.registerXIP(xipPath, basename(xipPath), size);
    res.json({ message: 'XIP registered' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/host/status ──────────────────────────────────────────────────────

storageRouter.get('/host/status', async (_req: Request, res: Response) => {
  try {
    const status = await lume.getHostStatus();
    res.json(status);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/host/lume-status ─────────────────────────────────────────────────
// Lightweight check — does not throw even when lume serve is down.

storageRouter.get('/host/lume-status', async (_req: Request, res: Response) => {
  try {
    await lume.getHostStatus();
    res.json({ running: true });
  } catch {
    // HTTP API timed out — check if lume serve process is alive (it may just be overloaded).
    // If the process exists, report running=true so the UI doesn't show "lume serve down"
    // while a VM is booting and lume is temporarily unresponsive.
    const alive = lume.isLumeServeProcessAlive();
    res.json({ running: alive });
  }
});

// ── POST /api/host/lume-serve ─────────────────────────────────────────────────
// Start lume serve if it is not already running.

storageRouter.post('/host/lume-serve', async (_req: Request, res: Response) => {
  try {
    await ensureLumeServe();
    res.json({ message: 'lume serve started' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── POST /api/host/restart-lume ───────────────────────────────────────────────
// Nuclear option: force-kill lume serve (killing all running VMs) and restart.
// Use when lume serve is frozen and VMs are already in an unusable state.

storageRouter.post('/host/restart-lume', async (_req: Request, res: Response) => {
  try {
    console.log('[api] POST /host/restart-lume — force-killing and restarting lume serve');
    await lume.forceRestartLumeServe();
    res.json({ message: 'lume serve restarted' });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

// ── GET /api/golden ───────────────────────────────────────────────────────────

storageRouter.get('/golden', (_req: Request, res: Response) => {
  try {
    const versions = db.listGoldenVersions();
    res.json({ versions });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});
