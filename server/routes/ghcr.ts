/**
 * GHCR routes — push/pull VM images to/from GitHub Container Registry.
 *
 * GET    /api/ghcr/sources                      list all configured GHCR sources
 * POST   /api/ghcr/sources                      add a new source
 * DELETE /api/ghcr/sources/:id                  remove a source
 * POST   /api/ghcr/sources/:id/default           set as default source
 *
 * GET    /api/ghcr/packages?sourceId=            list container packages for a source
 * GET    /api/ghcr/packages/:name/tags?sourceId= list tags for a package
 *
 * POST   /api/ghcr/push                          push VM → GHCR (spawns lume push)
 * POST   /api/ghcr/pull                          pull image → VM  (spawns lume pull)
 * POST   /api/ghcr/check-collision               pre-flight: does VM name already exist?
 *
 * GET    /api/ghcr/task/:id                      task status + live log tail
 * POST   /api/ghcr/task/:id/cancel               kill push/pull in progress
 */

import { Router, Request, Response } from 'express';
import { spawn } from 'child_process';
import { randomUUID } from 'crypto';
import * as db from '../db.js';
import * as tasks from '../tasks.js';
import * as lume from '../lume.js';
import { getSetting } from '../db.js';

/** GitHub API base URL */
const GH_API = 'https://api.github.com';

export const ghcrRouter = Router();

const LUME_BIN = '/opt/homebrew/bin/lume';

// ── GHCR source CRUD ──────────────────────────────────────────────────────────

ghcrRouter.get('/ghcr/sources', (_req, res) => {
  res.json(db.listGhcrSources());
});

ghcrRouter.post('/ghcr/sources', (req: Request, res: Response) => {
  const { name, registry = 'ghcr.io', organization, isDefault = false } = req.body as {
    name: string; registry?: string; organization: string; isDefault?: boolean;
  };
  if (!name || !organization) {
    return void res.status(400).json({ error: 'name and organization are required' });
  }
  const id = randomUUID();
  db.addGhcrSource(id, name, registry, organization, Boolean(isDefault));
  res.json({ id });
});

ghcrRouter.delete('/ghcr/sources/:id', (req: Request, res: Response) => {
  db.removeGhcrSource(req.params.id);
  res.json({ ok: true });
});

ghcrRouter.post('/ghcr/sources/:id/default', (req: Request, res: Response) => {
  const src = db.getGhcrSource(req.params.id);
  if (!src) return void res.status(404).json({ error: 'Source not found' });
  db.setDefaultGhcrSource(req.params.id);
  res.json({ ok: true });
});

// ── Package / tag listing ─────────────────────────────────────────────────────

/**
 * Build GitHub API auth headers from stored credentials.
 * Returns empty headers for public access if no token is configured.
 */
function ghApiHeaders(): Record<string, string> {
  const token = getSetting('github_token');
  return token
    ? { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' }
    : { Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' };
}

/**
 * GET /api/ghcr/packages?sourceId=<id>
 *
 * Returns the list of container packages (image names) available in the
 * configured org/user via the GitHub Packages API.
 * Requires a token with read:packages if the org/packages are private.
 */
ghcrRouter.get('/ghcr/packages', async (req: Request, res: Response) => {
  const sourceId = String(req.query.sourceId ?? '');
  const source = sourceId ? db.getGhcrSource(sourceId) : db.getDefaultGhcrSource();
  if (!source) return void res.status(400).json({ error: 'No GHCR source found' });

  const username = getSetting('github_username') ?? '';
  const { organization } = source;

  // Determine whether org is a user account or an org
  // Try org endpoint first, fall back to user endpoint
  const tryUrls = [
    `${GH_API}/orgs/${encodeURIComponent(organization)}/packages?package_type=container&per_page=100`,
    `${GH_API}/users/${encodeURIComponent(organization)}/packages?package_type=container&per_page=100`,
    // fallback: if org == authed user
    username ? `${GH_API}/user/packages?package_type=container&per_page=100` : null,
  ].filter(Boolean) as string[];

  for (const url of tryUrls) {
    try {
      const resp = await fetch(url, { headers: ghApiHeaders(), signal: AbortSignal.timeout(8000) });
      if (!resp.ok) continue;
      const pkgs = await resp.json() as Array<{ name: string; visibility: string; updated_at: string }>;
      return void res.json(pkgs.map(p => ({
        name: p.name,
        visibility: p.visibility,
        updatedAt: p.updated_at,
      })));
    } catch { /* try next */ }
  }

  res.status(502).json({ error: 'Could not fetch packages. Check GitHub token and source configuration.' });
});

/**
 * GET /api/ghcr/packages/:name/tags?sourceId=<id>
 *
 * Returns the list of tags for a given container package.
 * Each tag is derived from the package version metadata.
 */
ghcrRouter.get('/ghcr/packages/:name/tags', async (req: Request, res: Response) => {
  const { name } = req.params;
  const sourceId = String(req.query.sourceId ?? '');
  const source = sourceId ? db.getGhcrSource(sourceId) : db.getDefaultGhcrSource();
  if (!source) return void res.status(400).json({ error: 'No GHCR source found' });

  const username = getSetting('github_username') ?? '';
  const { organization } = source;

  const encodedName = encodeURIComponent(name);
  const tryUrls = [
    `${GH_API}/orgs/${encodeURIComponent(organization)}/packages/container/${encodedName}/versions?per_page=100`,
    `${GH_API}/users/${encodeURIComponent(organization)}/packages/container/${encodedName}/versions?per_page=100`,
    username ? `${GH_API}/user/packages/container/${encodedName}/versions?per_page=100` : null,
  ].filter(Boolean) as string[];

  for (const url of tryUrls) {
    try {
      const resp = await fetch(url, { headers: ghApiHeaders(), signal: AbortSignal.timeout(8000) });
      if (!resp.ok) continue;
      const versions = await resp.json() as Array<{
        id: number;
        name: string;
        metadata?: { container?: { tags: string[] } };
        created_at: string;
        updated_at: string;
      }>;
      // Flatten to a list of {tag, updatedAt} — exclude untagged versions
      const tags: Array<{ tag: string; updatedAt: string; versionName: string }> = [];
      for (const v of versions) {
        const vtags = v.metadata?.container?.tags ?? [];
        for (const tag of vtags) {
          tags.push({ tag, updatedAt: v.updated_at, versionName: v.name });
        }
      }
      // Sort newest first
      tags.sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime());
      return void res.json(tags);
    } catch { /* try next */ }
  }

  res.status(502).json({ error: 'Could not fetch tags. Check GitHub token and source configuration.' });
});

// ── Collision check ───────────────────────────────────────────────────────────

/**
 * POST /api/ghcr/check-collision
 * Body: { vmName: string }
 * Returns: { exists: boolean, vm?: object }
 *
 * Checks whether a VM with the given name already exists in lume, so the UI
 * can warn before attempting a pull that would overwrite it.
 */
ghcrRouter.post('/ghcr/check-collision', async (req: Request, res: Response) => {
  const { vmName } = req.body as { vmName: string };
  if (!vmName) return void res.status(400).json({ error: 'vmName is required' });
  try {
    const vms = await lume.listVMs();
    const existing = vms.find(v => v.name === vmName);
    res.json({ exists: Boolean(existing), vm: existing ?? null });
  } catch {
    // Fall back to CLI if lume serve is down
    try {
      const vms = await lume.listVMsCLI();
      const existing = vms.find(v => v.name === vmName);
      res.json({ exists: Boolean(existing), vm: existing ?? null });
    } catch (err) {
      res.status(500).json({ error: String(err) });
    }
  }
});

// ── Push VM → GHCR ───────────────────────────────────────────────────────────

/**
 * POST /api/ghcr/push
 * Body: {
 *   vmName: string,       lume VM to push
 *   imageName: string,    e.g. "uitest-golden"
 *   tag: string,          e.g. "26.4.1-16.3-20260419"
 *   additionalTags?: string[],  e.g. ["26.4.1-latest"]
 *   sourceId?: string,    GHCR source (uses default if omitted)
 *   chunkSizeMb?: number, default 512
 * }
 * Returns: { taskId: string }
 */
ghcrRouter.post('/ghcr/push', (req: Request, res: Response) => {
  const {
    vmName, imageName, tag,
    additionalTags = [],
    sourceId,
    chunkSizeMb = 512,
  } = req.body as {
    vmName: string; imageName: string; tag: string;
    additionalTags?: string[]; sourceId?: string; chunkSizeMb?: number;
  };

  if (!vmName || !imageName || !tag) {
    return void res.status(400).json({ error: 'vmName, imageName, and tag are required' });
  }

  // Resolve source
  const source = sourceId
    ? db.getGhcrSource(sourceId)
    : db.getDefaultGhcrSource();
  if (!source) {
    return void res.status(400).json({ error: 'No GHCR source configured. Add one in Settings → GHCR Sources.' });
  }

  // Resolve credentials
  const ghUsername = getSetting('github_username') ?? '';
  const ghToken    = getSetting('github_token') ?? '';
  if (!ghUsername || !ghToken) {
    return void res.status(400).json({ error: 'GitHub credentials not set. Configure them in Settings.' });
  }

  // Build lume push args
  const args = [
    'push',
    vmName,
    `${imageName}:${tag}`,
    '--organization', source.organization,
    '--registry', source.registry,
    '--chunk-size-mb', String(chunkSizeMb),
    '--verbose',
  ];
  for (const t of additionalTags) {
    args.push('--additional-tags', t);
  }

  const label = `Push ${vmName} → ${source.registry}/${source.organization}/${imageName}:${tag}`;
  const taskId = tasks.createTask('ghcr-push', label, {
    vmName, imageName, tag, sourceId: source.id,
    registry: source.registry, organization: source.organization,
  });

  // Collect log lines for task (capped at 2000 lines)
  const logLines: string[] = [];

  const proc = spawn(LUME_BIN, args, {
    env: { ...process.env, GITHUB_USERNAME: ghUsername, GITHUB_TOKEN: ghToken },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  tasks.attachProc(taskId, proc);

  const onData = (d: Buffer) => {
    const lines = d.toString().split('\n').filter(Boolean);
    logLines.push(...lines);
    if (logLines.length > 2000) logLines.splice(0, logLines.length - 2000);
    tasks.setTaskLog(taskId, logLines.join('\n'));
  };
  proc.stdout.on('data', onData);
  proc.stderr.on('data', onData);

  proc.on('exit', (code) => {
    if (code === 0) {
      tasks.resolveTask(taskId);
    } else {
      tasks.failTask(taskId, `lume push exited with code ${code}`);
    }
  });

  res.json({ taskId });
});

// ── Pull image → VM ───────────────────────────────────────────────────────────

/**
 * POST /api/ghcr/pull
 * Body: {
 *   imageName: string,   e.g. "uitest-golden"
 *   tag: string,         e.g. "26.4.1-16.3-20260419"
 *   vmName: string,      local VM name to create
 *   sourceId?: string,   GHCR source (uses default if omitted)
 *   force?: boolean,     pass --force to lume pull (overwrites existing VM!)
 * }
 * Returns: { taskId: string }
 */
ghcrRouter.post('/ghcr/pull', async (req: Request, res: Response) => {
  const {
    imageName, tag, vmName,
    sourceId,
    force = false,
  } = req.body as {
    imageName: string; tag: string; vmName: string;
    sourceId?: string; force?: boolean;
  };

  if (!imageName || !tag || !vmName) {
    return void res.status(400).json({ error: 'imageName, tag, and vmName are required' });
  }

  // Collision check — refuse if VM already exists and force is not set
  if (!force) {
    try {
      const vms = await lume.listVMs().catch(() => lume.listVMsCLI());
      const existing = vms.find(v => v.name === vmName);
      if (existing) {
        return void res.status(409).json({
          error: `A VM named "${vmName}" already exists (status: ${existing.status}). Choose a different name or set force=true to overwrite.`,
          existing,
        });
      }
    } catch (err) {
      console.warn('[ghcr/pull] Could not check for collision:', err);
    }
  }

  // Resolve source
  const source = sourceId
    ? db.getGhcrSource(sourceId)
    : db.getDefaultGhcrSource();
  if (!source) {
    return void res.status(400).json({ error: 'No GHCR source configured. Add one in Settings → GHCR Sources.' });
  }

  // Credentials — optional for public packages
  const ghUsername = getSetting('github_username') ?? '';
  const ghToken    = getSetting('github_token') ?? '';

  const args = [
    'pull',
    `${imageName}:${tag}`,
    vmName,
    '--organization', source.organization,
    '--registry', source.registry,
  ];
  if (ghUsername) args.push('--username', ghUsername);
  if (ghToken)    args.push('--password', ghToken);
  if (force)      args.push('--force');

  const label = `Pull ${source.registry}/${source.organization}/${imageName}:${tag} → ${vmName}`;
  const taskId = tasks.createTask('ghcr-pull', label, {
    imageName, tag, vmName, sourceId: source.id,
    registry: source.registry, organization: source.organization,
  });

  const logLines: string[] = [];

  const proc = spawn(LUME_BIN, args, {
    env: { ...process.env },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  tasks.attachProc(taskId, proc);

  const onData = (d: Buffer) => {
    const lines = d.toString().split('\n').filter(Boolean);
    logLines.push(...lines);
    if (logLines.length > 2000) logLines.splice(0, logLines.length - 2000);
    tasks.setTaskLog(taskId, logLines.join('\n'));
  };
  proc.stdout.on('data', onData);
  proc.stderr.on('data', onData);

  proc.on('exit', (code) => {
    if (code === 0) {
      // Seed the new VM in our DB
      db.upsertVM({ id: vmName });
      tasks.resolveTask(taskId);
    } else {
      tasks.failTask(taskId, `lume pull exited with code ${code}`);
    }
  });

  res.json({ taskId });
});

// ── Task status ───────────────────────────────────────────────────────────────

ghcrRouter.get('/ghcr/task/:id', (req: Request, res: Response) => {
  const task = tasks.getTask(req.params.id);
  if (!task) return void res.status(404).json({ error: 'Task not found' });
  const { proc: _proc, ...safe } = task as typeof task & { proc?: unknown };
  void _proc;
  res.json({ ...(safe as object), log: tasks.getTaskLog(req.params.id) });
});

ghcrRouter.post('/ghcr/task/:id/cancel', (req: Request, res: Response) => {
  const task = tasks.getTask(req.params.id);
  if (!task) return void res.status(404).json({ error: 'Task not found' });
  tasks.cancelTask(req.params.id);
  res.json({ ok: true });
});
