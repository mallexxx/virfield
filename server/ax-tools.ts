/**
 * AX snapshot/diff/YAML tools.
 * Snapshots are stored in SQLite and on disk at ~/VMShare/.snapshots/
 */

import { randomUUID } from 'crypto';
import { mkdirSync, writeFileSync, readFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import { saveSnapshot, getSnapshot, getLastSnapshot } from './db.js';
import { callPeekaboo } from './peekaboo-proxy.js';

const SNAPSHOT_DIR = join(homedir(), 'VMShare', '.snapshots');
mkdirSync(SNAPSHOT_DIR, { recursive: true });

// ── Snapshot management ───────────────────────────────────────────────────────

export async function takeSnapshot(vmId: string, vmIp: string, name: string, app?: string) {
  const result = await callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'see',
    arguments: app ? { app } : {},
  });

  const snapshotJson = JSON.stringify(result);
  const id = `${vmId}-${name}-${Date.now()}`;

  // Save to SQLite
  saveSnapshot(id, vmId, name, app ?? '', snapshotJson);

  // Save to disk
  const filePath = join(SNAPSHOT_DIR, `${id}.json`);
  writeFileSync(filePath, snapshotJson);

  return { id, name, app, size: snapshotJson.length };
}

// ── Diff ──────────────────────────────────────────────────────────────────────

interface AXElement {
  identifier?: string;
  role?: string;
  label?: string;
  value?: string;
  children?: AXElement[];
}

interface DiffResult {
  appeared: AXElement[];
  disappeared: AXElement[];
  value_changed: Array<{ identifier: string; before: string; after: string }>;
}

function flattenAX(tree: AXElement, elements: AXElement[] = []): AXElement[] {
  elements.push(tree);
  for (const child of tree.children ?? []) {
    flattenAX(child, elements);
  }
  return elements;
}

export function diffSnapshots(beforeJson: string, afterJson: string): DiffResult {
  const before = JSON.parse(beforeJson) as AXElement;
  const after = JSON.parse(afterJson) as AXElement;

  const beforeElements = flattenAX(before);
  const afterElements = flattenAX(after);

  const beforeById = new Map(beforeElements.filter(e => e.identifier).map(e => [e.identifier!, e]));
  const afterById = new Map(afterElements.filter(e => e.identifier).map(e => [e.identifier!, e]));

  const appeared: AXElement[] = [];
  const disappeared: AXElement[] = [];
  const value_changed: DiffResult['value_changed'] = [];

  for (const [id, afterEl] of afterById) {
    const beforeEl = beforeById.get(id);
    if (!beforeEl) {
      appeared.push(afterEl);
    } else if (beforeEl.value !== afterEl.value) {
      value_changed.push({
        identifier: id,
        before: beforeEl.value ?? '',
        after: afterEl.value ?? '',
      });
    }
  }

  for (const [id, beforeEl] of beforeById) {
    if (!afterById.has(id)) {
      disappeared.push(beforeEl);
    }
  }

  return { appeared, disappeared, value_changed };
}

export async function diffLastSnapshot(vmId: string, vmIp: string, app?: string): Promise<DiffResult> {
  const last = getLastSnapshot(vmId, app);
  if (!last) throw new Error(`No snapshot found for VM ${vmId}${app ? ` app ${app}` : ''}`);

  const current = await callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'see',
    arguments: app ? { app } : {},
  });
  const currentJson = JSON.stringify(current);

  return diffSnapshots(last.snapshot_json, currentJson);
}

export async function diffNamedSnapshots(beforeId: string, afterId: string): Promise<DiffResult> {
  const before = getSnapshot(beforeId);
  const after = getSnapshot(afterId);
  if (!before) throw new Error(`Snapshot not found: ${beforeId}`);
  if (!after) throw new Error(`Snapshot not found: ${afterId}`);
  return diffSnapshots(before.snapshot_json, after.snapshot_json);
}

// ── YAML conversion ───────────────────────────────────────────────────────────

function indent(n: number) { return '  '.repeat(n); }

function elementToYaml(el: AXElement, depth: number): string {
  const lines: string[] = [];
  const fields: string[] = [];
  if (el.role) fields.push(`role: ${el.role}`);
  if (el.identifier) fields.push(`identifier: "${el.identifier}"`);
  if (el.label) fields.push(`label: "${el.label}"`);
  if (el.value) fields.push(`value: "${el.value}"`);

  lines.push(`${indent(depth)}- ${fields.join(', ')}`);

  for (const child of el.children ?? []) {
    lines.push(elementToYaml(child, depth + 1));
  }
  return lines.join('\n');
}

export function snapshotToYaml(snapshotJson: string): string {
  const tree = JSON.parse(snapshotJson) as AXElement;
  return elementToYaml(tree, 0);
}

// ── Diff → YAML ───────────────────────────────────────────────────────────────

function elementLine(el: AXElement): string {
  const parts: string[] = [];
  if (el.role) parts.push(`role: ${el.role}`);
  if (el.identifier) parts.push(`id: "${el.identifier}"`);
  if (el.label) parts.push(`label: "${el.label}"`);
  if (el.value) parts.push(`value: "${el.value}"`);
  return parts.join(', ');
}

export function diffToYaml(diff: DiffResult): string {
  const lines: string[] = [];

  if (diff.appeared.length) {
    lines.push('appeared:');
    for (const el of diff.appeared) lines.push(`  - ${elementLine(el)}`);
  }
  if (diff.disappeared.length) {
    lines.push('disappeared:');
    for (const el of diff.disappeared) lines.push(`  - ${elementLine(el)}`);
  }
  if (diff.value_changed.length) {
    lines.push('value_changed:');
    for (const vc of diff.value_changed) {
      lines.push(`  - id: "${vc.identifier}"`);
      lines.push(`    before: "${vc.before}"`);
      lines.push(`    after:  "${vc.after}"`);
    }
  }
  if (!lines.length) lines.push('no_changes: true');
  return lines.join('\n');
}

// ── Query ──────────────────────────────────────────────────────────────────────

export function querySnapshot(snapshotJson: string, opts: {
  identifier?: string;
  role?: string;
  label?: string;
}): AXElement[] {
  const tree = JSON.parse(snapshotJson) as AXElement;
  const all = flattenAX(tree);
  return all.filter(el => {
    if (opts.identifier && el.identifier !== opts.identifier) return false;
    if (opts.role && el.role !== opts.role) return false;
    if (opts.label && el.label !== opts.label) return false;
    return true;
  });
}
