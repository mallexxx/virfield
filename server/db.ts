/**
 * SQLite state database — single source of truth for VM metadata,
 * setup stage status, tunnel state, AX snapshots, and golden versions.
 *
 * Database file: ~/.virfield/state.db
 */

import Database from 'better-sqlite3';
import { homedir } from 'os';
import { mkdirSync } from 'fs';
import { join } from 'path';

const DB_DIR = process.env.VMCONSOLE_DB_DIR ?? join(homedir(), '.virfield');
const DB_PATH = join(DB_DIR, 'state.db');

mkdirSync(DB_DIR, { recursive: true });

export const db = new Database(DB_PATH);

// Enable WAL mode for concurrent reads from web server + MCP server
db.pragma('journal_mode = WAL');
db.pragma('foreign_keys = ON');

// ── Schema migrations ─────────────────────────────────────────────────────────

db.exec(`
  CREATE TABLE IF NOT EXISTS vms (
    id TEXT PRIMARY KEY,           -- lume VM name
    tag TEXT DEFAULT 'dev',        -- 'golden', 'run', 'dev'
    macos_version TEXT,
    xcode_version TEXT,
    created_at INTEGER DEFAULT (strftime('%s','now')),
    last_run_at INTEGER,
    notes TEXT
  );

  CREATE TABLE IF NOT EXISTS vm_stages (
    vm_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',  -- pending|running|done|failed
    last_run_at INTEGER,
    output TEXT,                              -- last run output (truncated to 64KB)
    PRIMARY KEY (vm_id, stage),
    FOREIGN KEY (vm_id) REFERENCES vms(id) ON DELETE CASCADE
  );

  CREATE TABLE IF NOT EXISTS vm_tunnels (
    vm_id TEXT PRIMARY KEY,
    local_port INTEGER NOT NULL,
    pid INTEGER,
    opened_at INTEGER DEFAULT (strftime('%s','now')),
    FOREIGN KEY (vm_id) REFERENCES vms(id) ON DELETE CASCADE
  );

  CREATE TABLE IF NOT EXISTS ax_snapshots (
    id TEXT PRIMARY KEY,
    vm_id TEXT NOT NULL,
    name TEXT NOT NULL,
    app TEXT,
    snapshot_json TEXT NOT NULL,
    created_at INTEGER DEFAULT (strftime('%s','now'))
  );

  CREATE TABLE IF NOT EXISTS golden_versions (
    id TEXT PRIMARY KEY,
    vm_name TEXT NOT NULL,
    macos_version TEXT,
    xcode_version TEXT,
    promoted_at INTEGER DEFAULT (strftime('%s','now')),
    notes TEXT
  );

  CREATE TABLE IF NOT EXISTS storage_locations (
    name TEXT PRIMARY KEY,
    path TEXT NOT NULL,
    is_default INTEGER DEFAULT 0
  );

  -- User-registered IPSW files outside the standard scan paths
  CREATE TABLE IF NOT EXISTS registered_ipsws (
    path TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    size INTEGER,
    registered_at INTEGER DEFAULT (strftime('%s','now'))
  );

  -- User-registered XIP archives outside ~/VMShare
  CREATE TABLE IF NOT EXISTS registered_xips (
    path TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    size INTEGER,
    registered_at INTEGER DEFAULT (strftime('%s','now'))
  );

  -- Key-value settings store (GH credentials, folder path overrides)
  CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER DEFAULT (strftime('%s','now'))
  );

  -- GHCR registry sources (multiple orgs/registries supported)
  CREATE TABLE IF NOT EXISTS ghcr_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    registry TEXT NOT NULL DEFAULT 'ghcr.io',
    organization TEXT NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER DEFAULT (strftime('%s','now'))
  );

  -- In-flight or recently completed build jobs
  -- Persists across server restarts so UI can show Stop Build / progress.
  CREATE TABLE IF NOT EXISTS build_jobs (
    golden_vm TEXT PRIMARY KEY,
    base_vm TEXT NOT NULL,
    nosip_vm TEXT NOT NULL,
    pid INTEGER,            -- OS PID of the build process, NULL if unknown/finished
    status TEXT NOT NULL DEFAULT 'running',  -- running|done|failed|stopped
    started_at TEXT NOT NULL,
    finished_at TEXT
  );
`);

// ── Stage names ───────────────────────────────────────────────────────────────

export const STAGES = [
  'download_ipsw',
  'create_vm',
  'setup_assistant',
  'disable_sip',
  'provision_vm',
] as const;

export type Stage = (typeof STAGES)[number];

// ── Query helpers ─────────────────────────────────────────────────────────────

export function getVM(id: string) {
  return db.prepare('SELECT * FROM vms WHERE id = ?').get(id) as VMRow | undefined;
}

export function listVMs() {
  return db.prepare('SELECT * FROM vms ORDER BY created_at DESC').all() as VMRow[];
}

export function upsertVM(vm: Partial<VMRow> & { id: string }) {
  db.prepare(`
    INSERT INTO vms (id, tag, macos_version, xcode_version, notes)
    VALUES (@id, @tag, @macos_version, @xcode_version, @notes)
    ON CONFLICT(id) DO UPDATE SET
      tag = COALESCE(@tag, tag),
      macos_version = COALESCE(@macos_version, macos_version),
      xcode_version = COALESCE(@xcode_version, xcode_version),
      notes = COALESCE(@notes, notes)
  `).run({ tag: null, macos_version: null, xcode_version: null, notes: null, ...vm });
}

export function deleteVM(id: string) {
  db.prepare('DELETE FROM vms WHERE id = ?').run(id);
}

export function renameVM(oldId: string, newId: string) {
  db.transaction(() => {
    db.prepare('INSERT OR IGNORE INTO vms (id) SELECT ? FROM vms WHERE id = ?').run(newId, oldId);
    db.prepare('UPDATE vms SET id = ? WHERE id = ?').run(newId, oldId);
    db.prepare('UPDATE vm_stages SET vm_id = ? WHERE vm_id = ?').run(newId, oldId);
    db.prepare('UPDATE vm_tunnels SET vm_id = ? WHERE vm_id = ?').run(newId, oldId);
    db.prepare('UPDATE build_jobs SET golden_vm = ? WHERE golden_vm = ?').run(newId, oldId);
    db.prepare('UPDATE build_jobs SET base_vm = ? WHERE base_vm = ?').run(newId, oldId);
    db.prepare('UPDATE build_jobs SET nosip_vm = ? WHERE nosip_vm = ?').run(newId, oldId);
  })();
}

export function touchVMRun(id: string) {
  db.prepare(`UPDATE vms SET last_run_at = strftime('%s','now') WHERE id = ?`).run(id);
}

export function getStages(vmId: string) {
  const rows = db.prepare('SELECT * FROM vm_stages WHERE vm_id = ?').all(vmId) as StageRow[];
  // Fill in missing stages as 'pending'
  const map = new Map(rows.map(r => [r.stage, r]));
  return STAGES.map(stage => map.get(stage) ?? { vm_id: vmId, stage, status: 'pending', last_run_at: null, output: null });
}

/** On server startup, reset transient stage states — processes are gone after restart.
 *  "running" → always orphaned; "failed" → errors are session-scoped, not persistent. */
export function resetOrphanedStages() {
  db.prepare(`UPDATE vm_stages SET status = 'pending', output = NULL WHERE status IN ('running', 'failed')`).run();
}

export function setStageStatus(vmId: string, stage: string, status: string, output?: string) {
  db.prepare(`
    INSERT INTO vm_stages (vm_id, stage, status, last_run_at, output)
    VALUES (?, ?, ?, strftime('%s','now'), ?)
    ON CONFLICT(vm_id, stage) DO UPDATE SET
      status = excluded.status,
      last_run_at = excluded.last_run_at,
      output = COALESCE(excluded.output, output)
  `).run(vmId, stage, status, output?.slice(0, 65536) ?? null);
}

export function getTunnel(vmId: string) {
  return db.prepare('SELECT * FROM vm_tunnels WHERE vm_id = ?').get(vmId) as TunnelRow | undefined;
}

export function setTunnel(vmId: string, localPort: number, pid?: number) {
  db.prepare(`
    INSERT INTO vm_tunnels (vm_id, local_port, pid, opened_at)
    VALUES (?, ?, ?, strftime('%s','now'))
    ON CONFLICT(vm_id) DO UPDATE SET local_port = excluded.local_port, pid = excluded.pid, opened_at = excluded.opened_at
  `).run(vmId, localPort, pid ?? null);
}

export function clearTunnel(vmId: string) {
  db.prepare('DELETE FROM vm_tunnels WHERE vm_id = ?').run(vmId);
}

export function listTunnelPorts(): number[] {
  const rows = db.prepare('SELECT local_port FROM vm_tunnels').all() as { local_port: number }[];
  return rows.map(r => r.local_port);
}

export function clearAllTunnels() {
  db.prepare('DELETE FROM vm_tunnels').run();
}

export function saveSnapshot(id: string, vmId: string, name: string, app: string, snapshotJson: string) {
  db.prepare(`
    INSERT INTO ax_snapshots (id, vm_id, name, app, snapshot_json)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET snapshot_json = excluded.snapshot_json, created_at = strftime('%s','now')
  `).run(id, vmId, name, app, snapshotJson);
}

export function getSnapshot(id: string) {
  return db.prepare('SELECT * FROM ax_snapshots WHERE id = ?').get(id) as SnapshotRow | undefined;
}

export function listSnapshots(vmId: string) {
  return db.prepare('SELECT id, vm_id, name, app, created_at FROM ax_snapshots WHERE vm_id = ? ORDER BY created_at DESC').all(vmId) as Omit<SnapshotRow, 'snapshot_json'>[];
}

export function getLastSnapshot(vmId: string, app?: string) {
  if (app) {
    return db.prepare('SELECT * FROM ax_snapshots WHERE vm_id = ? AND app = ? ORDER BY created_at DESC LIMIT 1').get(vmId, app) as SnapshotRow | undefined;
  }
  return db.prepare('SELECT * FROM ax_snapshots WHERE vm_id = ? ORDER BY created_at DESC LIMIT 1').get(vmId) as SnapshotRow | undefined;
}

export function promoteGolden(vmId: string, macosVersion: string | null, xcodeVersion: string | null, notes?: string) {
  const id = `${vmId}-${Date.now()}`;
  db.prepare(`
    INSERT INTO golden_versions (id, vm_name, macos_version, xcode_version, notes)
    VALUES (?, ?, ?, ?, ?)
  `).run(id, vmId, macosVersion, xcodeVersion, notes ?? null);
  return id;
}

export function listGoldenVersions() {
  return db.prepare('SELECT * FROM golden_versions ORDER BY promoted_at DESC').all() as GoldenRow[];
}

// ── Registered IPSW / XIP helpers ────────────────────────────────────────────

export function registerIPSW(path: string, name: string, size: number) {
  db.prepare(`
    INSERT INTO registered_ipsws (path, name, size)
    VALUES (?, ?, ?)
    ON CONFLICT(path) DO UPDATE SET name = excluded.name, size = excluded.size
  `).run(path, name, size);
}

export function listRegisteredIPSWs() {
  return db.prepare('SELECT * FROM registered_ipsws ORDER BY registered_at DESC').all() as RegisteredFileRow[];
}

export function unregisterIPSW(path: string) {
  db.prepare('DELETE FROM registered_ipsws WHERE path = ?').run(path);
}

export function registerXIP(path: string, name: string, size: number) {
  db.prepare(`
    INSERT INTO registered_xips (path, name, size)
    VALUES (?, ?, ?)
    ON CONFLICT(path) DO UPDATE SET name = excluded.name, size = excluded.size
  `).run(path, name, size);
}

export function listRegisteredXIPs() {
  return db.prepare('SELECT * FROM registered_xips ORDER BY registered_at DESC').all() as RegisteredFileRow[];
}

export function unregisterXIP(path: string) {
  db.prepare('DELETE FROM registered_xips WHERE path = ?').run(path);
}

// ── Build job helpers ─────────────────────────────────────────────────────────

export interface BuildJobRow {
  golden_vm: string;
  base_vm: string;
  nosip_vm: string;
  pid: number | null;
  status: string;
  started_at: string;
  finished_at: string | null;
}

export function upsertBuildJob(goldenVm: string, baseVm: string, nosipVm: string, pid: number | null) {
  db.prepare(`
    INSERT INTO build_jobs (golden_vm, base_vm, nosip_vm, pid, status, started_at)
    VALUES (?, ?, ?, ?, 'running', datetime('now'))
    ON CONFLICT(golden_vm) DO UPDATE SET
      base_vm = excluded.base_vm,
      nosip_vm = excluded.nosip_vm,
      pid = excluded.pid,
      status = 'running',
      started_at = excluded.started_at,
      finished_at = NULL
  `).run(goldenVm, baseVm, nosipVm, pid);
}

export function finishBuildJob(goldenVm: string, status: 'done' | 'failed' | 'stopped') {
  db.prepare(`
    UPDATE build_jobs SET status = ?, finished_at = datetime('now'), pid = NULL
    WHERE golden_vm = ?
  `).run(status, goldenVm);
}

export function listRunningBuildJobs(): BuildJobRow[] {
  return db.prepare(`SELECT * FROM build_jobs WHERE status = 'running'`).all() as BuildJobRow[];
}

// ── Row types ─────────────────────────────────────────────────────────────────

export interface VMRow {
  id: string;
  tag: string;
  macos_version: string | null;
  xcode_version: string | null;
  created_at: number;
  last_run_at: number | null;
  notes: string | null;
}

export interface StageRow {
  vm_id: string;
  stage: string;
  status: string;
  last_run_at: number | null;
  output: string | null;
}

export interface TunnelRow {
  vm_id: string;
  local_port: number;
  pid: number | null;
  opened_at: number;
}

export interface SnapshotRow {
  id: string;
  vm_id: string;
  name: string;
  app: string;
  snapshot_json: string;
  created_at: number;
}

export interface GoldenRow {
  id: string;
  vm_name: string;
  macos_version: string | null;
  xcode_version: string | null;
  promoted_at: number;
  notes: string | null;
}

export interface RegisteredFileRow {
  path: string;
  name: string;
  size: number | null;
  registered_at: number;
}

// ── Settings helpers ──────────────────────────────────────────────────────────

export function getSetting(key: string): string | null {
  const row = db.prepare('SELECT value FROM settings WHERE key = ?').get(key) as { value: string } | undefined;
  return row?.value ?? null;
}

export function setSetting(key: string, value: string): void {
  db.prepare(`
    INSERT INTO settings (key, value, updated_at)
    VALUES (?, ?, strftime('%s','now'))
    ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
  `).run(key, value);
}

export function deleteSetting(key: string): void {
  db.prepare('DELETE FROM settings WHERE key = ?').run(key);
}

export function getAllSettings(): Record<string, string> {
  const rows = db.prepare('SELECT key, value FROM settings').all() as { key: string; value: string }[];
  return Object.fromEntries(rows.map(r => [r.key, r.value]));
}

// ── GHCR source helpers ───────────────────────────────────────────────────────

export interface GhcrSourceRow {
  id: string;
  name: string;
  registry: string;
  organization: string;
  is_default: number;
  created_at: number;
}

export function listGhcrSources(): GhcrSourceRow[] {
  return db.prepare('SELECT * FROM ghcr_sources ORDER BY is_default DESC, created_at ASC').all() as GhcrSourceRow[];
}

export function getGhcrSource(id: string): GhcrSourceRow | undefined {
  return db.prepare('SELECT * FROM ghcr_sources WHERE id = ?').get(id) as GhcrSourceRow | undefined;
}

export function getDefaultGhcrSource(): GhcrSourceRow | undefined {
  return db.prepare('SELECT * FROM ghcr_sources WHERE is_default = 1 LIMIT 1').get() as GhcrSourceRow | undefined;
}

export function addGhcrSource(id: string, name: string, registry: string, organization: string, isDefault: boolean): void {
  if (isDefault) {
    // Demote all others
    db.prepare('UPDATE ghcr_sources SET is_default = 0').run();
  }
  db.prepare(`
    INSERT INTO ghcr_sources (id, name, registry, organization, is_default)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      name = excluded.name,
      registry = excluded.registry,
      organization = excluded.organization,
      is_default = excluded.is_default
  `).run(id, name, registry, organization, isDefault ? 1 : 0);
}

export function removeGhcrSource(id: string): void {
  db.prepare('DELETE FROM ghcr_sources WHERE id = ?').run(id);
}

export function setDefaultGhcrSource(id: string): void {
  db.prepare('UPDATE ghcr_sources SET is_default = 0').run();
  db.prepare('UPDATE ghcr_sources SET is_default = 1 WHERE id = ?').run(id);
}
