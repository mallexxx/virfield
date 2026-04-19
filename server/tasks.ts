/**
 * In-memory background task registry.
 * Tracks long-running host operations (copy-to-share, xip-extract, etc.)
 * so the UI can show progress without polling the script directly.
 */

import { randomUUID } from 'crypto';
import { ChildProcess } from 'child_process';

export type TaskType = 'copy-to-share' | 'xip-extract' | 'record' | 'ghcr-push' | 'ghcr-pull';
export type TaskStatus = 'running' | 'done' | 'failed' | 'cancelled';

export interface Task {
  id: string;
  type: TaskType;
  label: string;
  status: TaskStatus;
  startedAt: number;
  finishedAt?: number;
  error?: string;
  /** Extra context — e.g. vmId for recordings, destPath for copy ops */
  meta?: Record<string, string>;
  proc?: ChildProcess;  // not serialised
}

const tasks = new Map<string, Task>();
/** Live log output per task — stored separately to avoid bloating Task objects. */
const taskLogs = new Map<string, string>();

/** Register a new task as running and return its ID. */
export function createTask(type: TaskType, label: string, meta?: Record<string, string>): string {
  const id = randomUUID();
  tasks.set(id, { id, type, label, status: 'running', startedAt: Date.now(), meta });
  return id;
}

export function attachProc(id: string, proc: ChildProcess): void {
  const task = tasks.get(id);
  if (task) task.proc = proc;
}

export function resolveTask(id: string): void {
  const task = tasks.get(id);
  if (task) { task.status = 'done'; task.finishedAt = Date.now(); task.proc = undefined; }
}

export function failTask(id: string, err: unknown): void {
  const task = tasks.get(id);
  if (task) { task.status = 'failed'; task.finishedAt = Date.now(); task.error = String(err); task.proc = undefined; }
}

export function getTask(id: string): Task | undefined {
  return tasks.get(id);
}

export function setTaskLog(id: string, log: string): void {
  taskLogs.set(id, log);
}

export function getTaskLog(id: string): string {
  return taskLogs.get(id) ?? '';
}

export function cancelTask(id: string): void {
  const task = tasks.get(id);
  if (!task) return;
  if (task.proc) {
    try { task.proc.kill('SIGTERM'); } catch { /* already gone */ }
  }
  task.status = 'cancelled';
  task.finishedAt = Date.now();
  task.proc = undefined;
}

/** Returns all tasks, newest first. Prunes tasks older than 30 min that are done/failed/cancelled. */
export function listTasks(): Omit<Task, 'proc'>[] {
  const cutoff = Date.now() - 30 * 60 * 1000;
  for (const [id, t] of tasks) {
    if (['done', 'failed', 'cancelled'].includes(t.status) && (t.finishedAt ?? 0) < cutoff) {
      tasks.delete(id);
      taskLogs.delete(id);
    }
  }
  return [...tasks.values()]
    .sort((a, b) => b.startedAt - a.startedAt)
    .map(({ proc: _proc, ...rest }) => rest);
}

/** Find running record task for a VM. */
export function getRecordingTask(vmId: string): Task | undefined {
  for (const t of tasks.values()) {
    if (t.type === 'record' && t.status === 'running' && t.meta?.vmId === vmId) return t;
  }
  return undefined;
}
