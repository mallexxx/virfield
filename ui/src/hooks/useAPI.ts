/**
 * API hooks — fetch from the backend REST API.
 * In dev the Vite proxy forwards /api → localhost:3000.
 */

import { useState, useEffect, useCallback, useRef } from 'react';

const BASE = '/api';

// ── Generic fetch ─────────────────────────────────────────────────────────────

export function useGet<T>(path: string, deps: unknown[] = [], pollMs?: number) {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const initialLoad = useRef(true);

  const refresh = useCallback(async () => {
    // Only show loading spinner on first fetch, not on background polls
    if (initialLoad.current) setLoading(true);
    setError(null);
    try {
      const resp = await fetch(`${BASE}${path}`);
      if (!resp.ok) throw new Error(`${resp.status} ${resp.statusText}`);
      setData(await resp.json());
      initialLoad.current = false;
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    refresh();
    if (!pollMs) return;
    const t = setInterval(refresh, pollMs);
    return () => clearInterval(t);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refresh, pollMs, ...deps]);

  return { data, loading, error, refresh };
}

export async function apiPost(path: string, body?: object): Promise<unknown> {
  const resp = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(text || `${resp.status} ${resp.statusText}`);
  }
  return resp.json();
}

export async function apiDelete(path: string): Promise<unknown> {
  const resp = await fetch(`${BASE}${path}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`${resp.status} ${resp.statusText}`);
  return resp.json();
}

export async function apiPatch(path: string, body: object): Promise<unknown> {
  const resp = await fetch(`${BASE}${path}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!resp.ok) throw new Error(`${resp.status} ${resp.statusText}`);
  return resp.json();
}

// ── VM types ──────────────────────────────────────────────────────────────────

export interface VMStage {
  vm_id: string;
  stage: string;
  status: 'pending' | 'running' | 'done' | 'failed';
  last_run_at: number | null;
  output: string | null;
}

export interface VMData {
  name: string;
  status: string;
  ipAddress: string | null;
  vncUrl: string | null;
  cpuCount: number;
  memorySize: number;
  diskSize: { allocated: number; total: number };
  display: string;
  locationName: string;
  sshAvailable: boolean | null;
  meta: {
    tag: string;
    macos_version: string | null;
    xcode_version: string | null;
    notes: string | null;
    last_run_at: number | null;
  } | null;
  stages: VMStage[];
  building?: boolean;
}

export function useVMs() {
  return useGet<VMData[]>('/vms', [], 4000);
}

export interface ActiveBuild {
  pid: number;
  goldenVm: string;
  baseVm: string;
  nosipVm: string;
  startedAt: string;
  vmIds: string[];
}

export function useActiveBuilds() {
  return useGet<ActiveBuild[]>('/vms/active-builds', [], 3000);
}

export function useVM(id: string) {
  return useGet<VMData>(`/vms/${id}`, [id], 4000);
}

export function useBuildState(vmId: string, pollMs?: number) {
  return useGet<BuildState>(`/vms/${vmId}/build-state`, [vmId], pollMs);
}

export function useRecordings(vmId: string) {
  return useGet<MediaFile[]>(`/vms/${vmId}/recordings`, [vmId]);
}

export interface ProvisionTool { id: string; label: string; }
export function useProvisionTools() {
  return useGet<ProvisionTool[]>('/vms/provision-tools');
}

export function useScreenshots(vmId: string) {
  return useGet<MediaFile[]>(`/vms/${vmId}/screenshots`, [vmId]);
}

export function useStorage() {
  return useGet<StorageLocation[]>('/storage');
}

export function useIPSW() {
  return useGet<IPSWInfo>('/ipsw');
}

export interface IPSWCatalogEntry {
  version: string;
  buildId: string;
  url: string;
  sizeBytes: number;
  sizeGb: string;
  major: string;
  name: string;   // e.g. "Sequoia"
  spec: string;   // e.g. "sequoia" or "15.4" — pass as --ipsw to script
}

export function useIPSWCatalog() {
  return useGet<IPSWCatalogEntry[]>('/ipsw/catalog');
}

export function useXcode() {
  return useGet<XcodeInfo>('/xcode');
}

// ── Background tasks ──────────────────────────────────────────────────────────

export type TaskType = 'copy-to-share' | 'xip-extract' | 'record';
export type TaskStatus = 'running' | 'done' | 'failed';

export interface BackgroundTask {
  id: string;
  type: TaskType;
  label: string;
  status: TaskStatus;
  startedAt: number;
  finishedAt?: number;
  error?: string;
  meta?: Record<string, string>;
}

export function useTasks(pollMs = 2000) {
  return useGet<BackgroundTask[]>('/vms/tasks/list', [], pollMs);
}

export function useRecordStatus(vmId: string) {
  return useGet<{ recording: boolean; taskId: string | null; file: string | null }>(
    `/vms/${encodeURIComponent(vmId)}/record/status`, [vmId], 3000,
  );
}

// ── Build pipeline types ──────────────────────────────────────────────────────

export interface BuildStageInfo {
  status: 'pending' | 'running' | 'done' | 'failed';
  label?: string;      // human-readable display name (from state.json)
  started?: string;
  finished?: string;
  substage?: string;   // sub-progress detail text
  error?: string;
}

export interface BuildState {
  vm: string;
  /** Human-readable label of the current stage (new schema) or stage key (old) */
  stage: string | null;
  substage: string | null;   // sub-progress detail for the current stage
  percent: number | null;    // 0–100 for stages that report numeric progress
  pid: number | null;
  status: 'running' | 'done' | 'failed' | 'no_state' | 'stale';
  updated: string;
  log: string | null;        // log directory path (new schema)
  log_dir: string | null;    // log directory path (old schema — keep for compat)
  recordings: string[] | null;
  hostname: string | null;
  ip: string | null;
  stages: Record<string, BuildStageInfo>;
}

export interface MediaFile {
  path: string;
  name: string;
  size: number;
  mtime: number;
  logDir: string;
}

// ── Storage types ─────────────────────────────────────────────────────────────

export interface StorageLocation {
  name: string;
  path: string;
  resolvedPath: string;
  totalBytes: number | null;
  usedBytes: number | null;
  freeBytes: number | null;
}

export interface IPSWInfo {
  latestUrl: string;
  localFiles: Array<{ path: string; size: number; name: string }>;
}

export interface XcodeInfo {
  apps: Array<{ path: string; version: string | null; name: string; inVMShare: boolean; inApplications: boolean }>;
  archives: Array<{ path: string; size: number; name: string; source: string }>;
}
