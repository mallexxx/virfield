/**
 * VM Console — HTTP server entry point.
 * Serves the REST API on localhost:3000 and the built React UI.
 * Starts lume serve as a managed subprocess.
 */

import express, { Request, Response, NextFunction } from 'express';
import expressWs from 'express-ws';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { existsSync, utimesSync } from 'fs';
import { execFile, execFileSync } from 'child_process';
import { promisify } from 'util';

const execFileAsync = promisify(execFile);
import { ensureLumeServe, stopLumeServe, isLumeServeProcessAlive } from './lume.js';
import { resetOrphanedStages } from './db.js';
import { closeAllTunnels, cleanStaleTunnels } from './tunnel-manager.js';
import { vmsRouter } from './routes/vms.js';
import { storageRouter } from './routes/storage.js';
import { buildRouter, restoreBuildTracking } from './routes/build.js';
import { settingsRouter } from './routes/settings.js';
import { ghcrRouter } from './routes/ghcr.js';
import { registerWSRoutes } from './routes/ws.js';

const __dirname = dirname(fileURLToPath(import.meta.url));
const PORT = Number(process.env.PORT ?? 3000);
const UI_DIST = join(__dirname, '..', 'ui', 'dist');
const UI_DEV_PROXY = 'http://localhost:5173';

// ── Express setup ─────────────────────────────────────────────────────────────

const app = express();
const { app: wsApp } = expressWs(app);

wsApp.use(express.json());

// CORS for dev (vite runs on 5173)
wsApp.use((_req, res, next) => {
  res.header('Access-Control-Allow-Origin', '*');
  res.header('Access-Control-Allow-Methods', 'GET,POST,PUT,PATCH,DELETE,OPTIONS');
  res.header('Access-Control-Allow-Headers', 'Content-Type');
  if (_req.method === 'OPTIONS') return res.sendStatus(204);
  next();
});

// Request logger
wsApp.use((req: Request, res: Response, next: NextFunction) => {
  const start = Date.now();
  res.on('finish', () => {
    const ms = Date.now() - start;
    const level = res.statusCode >= 500 ? 'ERROR' : res.statusCode >= 400 ? 'WARN' : 'INFO';
    console.log(`[api] ${level} ${req.method} ${req.path} ${res.statusCode} (${ms}ms)`);
  });
  next();
});

// ── API routes ────────────────────────────────────────────────────────────────

wsApp.use('/api', buildRouter);      // specific routes first (provision-tools, build-state, etc.)
wsApp.use('/api/vms', vmsRouter);    // wildcard /:id last
wsApp.use('/api', storageRouter);
wsApp.use('/api', settingsRouter);
wsApp.use('/api', ghcrRouter);

// ── lume serve management ─────────────────────────────────────────────────────
// Allows the Settings UI to start/stop/restart lume serve explicitly.

wsApp.get('/api/host/lume-status', async (_req, res) => {
  try {
    const resp = await fetch('http://localhost:7777/lume/host/status', { signal: AbortSignal.timeout(2000) });
    res.json({ running: resp.ok, processAlive: isLumeServeProcessAlive() });
  } catch {
    res.json({ running: false, processAlive: isLumeServeProcessAlive() });
  }
});

wsApp.post('/api/host/lume-serve', async (_req, res) => {
  try {
    await ensureLumeServe();
    res.json({ ok: true });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

wsApp.post('/api/host/lume-stop', (_req, res) => {
  stopLumeServe();
  res.json({ ok: true });
});

wsApp.post('/api/host/lume-restart', async (_req, res) => {
  try {
    stopLumeServe();
    await new Promise(r => setTimeout(r, 1000));
    await ensureLumeServe();
    res.json({ ok: true });
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});


// ── Update check ─────────────────────────────────────────────────────────────
// Compares local HEAD sha against the latest commit on the GitHub remote.
// Caches the GitHub response for 30 min to stay within unauthenticated rate limits.

const GITHUB_REPO = 'mallexxx/virfield';
let currentSha = 'unknown';
try { currentSha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: __dirname, encoding: 'utf8' }).trim(); } catch { /* not a git repo */ }

let updateCache: { ts: number; sha: string; message: string } | null = null;

wsApp.get('/api/host/update-check', async (_req, res) => {
  const now = Date.now();
  if (!updateCache || now - updateCache.ts > 30 * 60 * 1000) {
    try {
      const resp = await fetch(`https://api.github.com/repos/${GITHUB_REPO}/commits/main`, {
        headers: { 'Accept': 'application/vnd.github.v3+json', 'User-Agent': 'virfield-server' },
        signal: AbortSignal.timeout(8000),
      });
      if (resp.ok) {
        const data = await resp.json() as { sha: string; commit: { message: string } };
        updateCache = { ts: now, sha: data.sha, message: data.commit.message.split('\n')[0] };
      }
    } catch { /* network unavailable — return cached or current */ }
  }
  const latest = updateCache?.sha ?? currentSha;
  res.json({
    current: currentSha,
    latest,
    updateAvailable: latest !== 'unknown' && currentSha !== 'unknown' && latest !== currentSha,
    latestMessage: updateCache?.message ?? '',
  });
});

// Returns the absolute path to mcp-server.ts so the UI can show copy-paste configs.
wsApp.get('/api/host/mcp-server-path', (_req, res) => {
  const mcpPath = join(dirname(fileURLToPath(import.meta.url)), 'mcp-server.ts');
  res.json({ path: mcpPath });
});

// Restart the backend server itself by touching this file — tsx watch detects
// the mtime change and restarts the process. lume serve is detached so VMs
// are unaffected.
wsApp.post('/api/host/server-restart', (_req, res) => {
  res.json({ ok: true, message: 'Restarting…' });
  setTimeout(() => {
    console.log('[console] Restarting server on request from UI…');
    const now = new Date();
    try { utimesSync(fileURLToPath(import.meta.url), now, now); } catch (e) {
      console.warn('[console] Could not touch index.ts for restart:', e);
    }
  }, 200);
});

registerWSRoutes(wsApp);

// JSON error handler — replaces Express's default HTML 500 page
// Must be defined after all routes and have 4 params so Express recognises it as an error handler.
// eslint-disable-next-line @typescript-eslint/no-unused-vars
wsApp.use((err: Error, req: Request, res: Response, _next: NextFunction) => {
  console.error(`[api] UNHANDLED ${req.method} ${req.path}:`, err.message);
  console.error(err.stack);
  res.status(500).json({ error: err.message });
});

// ── Static UI (production build) ──────────────────────────────────────────────

if (existsSync(UI_DIST)) {
  wsApp.use(express.static(UI_DIST));
  // SPA fallback
  wsApp.get('*', (_req, res) => {
    res.sendFile(join(UI_DIST, 'index.html'));
  });
} else {
  wsApp.get('/', (_req, res) => {
    res.json({
      message: 'VM Console API running. Build the UI with: npm run build',
      apiDocs: '/api',
      devUI: 'Run: npm run dev:ui (in another terminal)',
    });
  });
}

// ── Startup ───────────────────────────────────────────────────────────────────

async function main() {
  console.log('[console] Starting VM Console...');

  // Clear stale tunnel DB rows from any previous server run
  cleanStaleTunnels();

  // Reset any stages still marked "running" from a previous server session
  resetOrphanedStages();

  // Restore active build tracking from DB (cleans up dead PIDs)
  restoreBuildTracking();

  try {
    await ensureLumeServe();
  } catch (err) {
    console.error('[console] Warning: could not start lume serve:', err);
    console.error('[console] VM operations may fail. Start lume serve manually.');
  }

  wsApp.listen(PORT, '127.0.0.1', () => {
    console.log(`[console] Server running at http://localhost:${PORT}`);
    console.log(`[console] API: http://localhost:${PORT}/api/vms`);
    if (!existsSync(UI_DIST)) {
      console.log(`[console] UI dev: run 'npm run dev:ui' then open http://localhost:5173`);
    }
  });
}

// ── Graceful shutdown ─────────────────────────────────────────────────────────

process.on('SIGINT', () => {
  console.log('\n[console] Shutting down...');
  // Close socat tunnels (local port forwards only — does not stop VMs).
  closeAllTunnels();
  // NOTE: we intentionally do NOT call stopLumeServe() here.
  // lume serve is detached and must survive server restarts so running VMs
  // are not killed when tsx watch reloads the backend during development.
  process.exit(0);
});

process.on('SIGTERM', () => {
  closeAllTunnels();
  // Same: leave lume serve running across restarts.
  process.exit(0);
});

main().catch(err => {
  console.error('[console] Fatal error:', err);
  process.exit(1);
});
