/**
 * Settings routes — GET/POST /api/settings
 *
 * Stores key-value config in SQLite (overrides env-var defaults from config.ts).
 *
 * Keys:
 *   github_username   GH username for lume push
 *   github_token      PAT with write:packages / read:packages
 *   scripts_dir       path to virfield/scripts
 *   log_base          root for log dirs + state.json files
 *   state_dir         per-VM state.json dir
 *   recordings_dir    VNC recording output dir
 *   vmshare           virtiofs shared volume path
 *   repo_dir          apple-browsers.git path
 */

import { Router, Request, Response } from 'express';
import * as db from '../db.js';
import { homedir } from 'os';
import { join } from 'path';

export const settingsRouter = Router();

/** All setting keys we expose to the UI. */
const SETTING_KEYS = [
  'github_username',
  'github_token',
  'scripts_dir',
  'log_base',
  'state_dir',
  'recordings_dir',
  'vmshare',
  'repo_dir',
] as const;

/** Default values (mirrors config.ts). */
const DEFAULTS: Record<string, string> = {
  scripts_dir:    join(homedir(), 'Developer', 'virfield', 'scripts'),
  log_base:       join(homedir(), 'Developer', 'virfield', 'logs'),
  state_dir:      join(homedir(), 'Developer', 'virfield', 'state'),
  recordings_dir: join(homedir(), 'Developer', 'virfield', 'recordings'),
  vmshare:        join(homedir(), 'VMShare'),
  repo_dir:       join(homedir(), 'Developer', 'repo'),
};

/** GET /api/settings — returns all settings with defaults filled in */
settingsRouter.get('/settings', (_req: Request, res: Response) => {
  const stored = db.getAllSettings();
  const merged: Record<string, string> = {};
  for (const key of SETTING_KEYS) {
    merged[key] = stored[key] ?? DEFAULTS[key] ?? '';
  }
  // Mask token for display — send a flag instead of the value
  const hasToken = Boolean(merged['github_token']);
  const result: Record<string, string | boolean> = { ...merged };
  if (hasToken) result['github_token'] = '***';
  result['github_token_set'] = hasToken;
  res.json(result);
});

/** POST /api/settings — save one or more settings */
settingsRouter.post('/settings', (req: Request, res: Response) => {
  const body = req.body as Record<string, string>;
  for (const [key, value] of Object.entries(body)) {
    if (!SETTING_KEYS.includes(key as typeof SETTING_KEYS[number])) {
      return void res.status(400).json({ error: `Unknown setting key: ${key}` });
    }
    if (value === '' || value === null || value === undefined) {
      db.deleteSetting(key);
    } else {
      db.setSetting(key, String(value));
    }
  }
  res.json({ ok: true });
});

/** DELETE /api/settings/:key — clear a single setting (revert to default) */
settingsRouter.delete('/settings/:key', (req: Request, res: Response) => {
  const { key } = req.params;
  db.deleteSetting(key);
  res.json({ ok: true });
});

/** Helper used by other routes to read effective setting (DB > env > default). */
export function effectiveSetting(key: string): string {
  return db.getSetting(key) ?? process.env[envKey(key)] ?? DEFAULTS[key] ?? '';
}

function envKey(key: string): string {
  const map: Record<string, string> = {
    scripts_dir:    'VMCONSOLE_SCRIPTS_DIR',
    log_base:       'VMCONSOLE_LOG_BASE',
    state_dir:      'VMCONSOLE_STATE_DIR',
    recordings_dir: 'VMCONSOLE_RECORDINGS_DIR',
    vmshare:        'VMCONSOLE_VMSHARE',
    repo_dir:       'VMCONSOLE_REPO_DIR',
  };
  return map[key] ?? '';
}
