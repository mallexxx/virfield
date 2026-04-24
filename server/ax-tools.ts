/**
 * AX snapshot/diff/YAML tools.
 * Snapshots are stored in SQLite and on disk at ~/VMShare/.snapshots/
 *
 * Peekaboo's `see` tool returns human-readable text wrapped in MCP content:
 *   { content: [{ type: 'text', text: '📸 UI State Captured\n...' }], isError: false }
 *
 * The text format per element is:
 *   {id} - "{label}" - at (x, y)[ - [not actionable]]
 * grouped under role headers:
 *   {role} (N found, M actionable):
 *
 * We parse this into ParsedElement[] for diffing.
 */

import { randomUUID } from 'crypto';
import { mkdirSync, writeFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import { saveSnapshot, getSnapshot, getLastSnapshot } from './db.js';
import { callPeekaboo } from './peekaboo-proxy.js';

const SNAPSHOT_DIR = join(homedir(), 'VMShare', '.snapshots');
mkdirSync(SNAPSHOT_DIR, { recursive: true });

// ── Parsed element ─────────────────────────────────────────────────────────────

interface ParsedElement {
  id: string;       // "elem_14", "menu_40"
  role: string;     // "button", "textField", "menu", "other"
  label: string;    // accessible label / current value
  actionable: boolean;
  x: number;
  y: number;
}

/** Extract the Peekaboo text payload from the MCP content wrapper. */
function extractText(snapshotJson: string): string | null {
  try {
    const parsed = JSON.parse(snapshotJson) as unknown;
    if (typeof parsed === 'string') return parsed;
    const p = parsed as Record<string, unknown>;
    const content = p['content'];
    if (Array.isArray(content) && content.length > 0) {
      const first = content[0] as Record<string, unknown>;
      if (typeof first['text'] === 'string') return first['text'] as string;
    }
    return null;
  } catch {
    return null;
  }
}

/** Parse Peekaboo's human-readable `see` output into a flat element list. */
function parsePeekabooText(text: string): ParsedElement[] {
  const elements: ParsedElement[] = [];
  let currentRole = '';

  for (const line of text.split('\n')) {
    // Role header: "button (13 found, 10 actionable):"
    const roleMatch = line.match(/^(\w+)\s+\(\d+\s+found/);
    if (roleMatch) {
      currentRole = roleMatch[1];
      continue;
    }

    // Element line: '  elem_14 - "Back" - at (232, 156) - [not actionable]'
    // or:           '  menu_40 - "Apple" - at (10, 0)'
    const elemMatch = line.match(/^\s+(\S+)\s+-\s+"([^"]*)"\s+-\s+at\s+\((\d+),\s*(\d+)\)(.*)/);
    if (elemMatch && currentRole) {
      const [, id, label, xStr, yStr, rest] = elemMatch;
      const actionable = !rest.includes('[not actionable]');
      elements.push({
        id,
        role: currentRole,
        label,
        actionable,
        x: Number(xStr),
        y: Number(yStr),
      });
    }
  }

  return elements;
}

// ── Snapshot management ───────────────────────────────────────────────────────

export async function takeSnapshot(vmId: string, vmIp: string, name: string, app?: string) {
  const result = await callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'see',
    arguments: app ? { app } : {},
  });

  const snapshotJson = JSON.stringify(result);
  const id = `${vmId}-${name}-${Date.now()}`;

  saveSnapshot(id, vmId, name, app ?? '', snapshotJson);

  const filePath = join(SNAPSHOT_DIR, `${id}.json`);
  writeFileSync(filePath, snapshotJson);

  return { id, name, app, size: snapshotJson.length };
}

// ── Diff ──────────────────────────────────────────────────────────────────────

interface DiffResult {
  appeared: Array<{ id: string; role: string; label: string }>;
  disappeared: Array<{ id: string; role: string; label: string }>;
  value_changed: Array<{ id: string; role: string; before: string; after: string }>;
}

export function diffSnapshots(beforeJson: string, afterJson: string): DiffResult {
  const beforeText = extractText(beforeJson);
  const afterText = extractText(afterJson);

  if (!beforeText || !afterText) {
    throw new Error('Snapshot data is not in Peekaboo text format');
  }

  const beforeElements = parsePeekabooText(beforeText);
  const afterElements = parsePeekabooText(afterText);

  const beforeMap = new Map(beforeElements.map(e => [e.id, e]));
  const afterMap = new Map(afterElements.map(e => [e.id, e]));

  const appeared: DiffResult['appeared'] = [];
  const disappeared: DiffResult['disappeared'] = [];
  const value_changed: DiffResult['value_changed'] = [];

  for (const [id, afterEl] of afterMap) {
    const beforeEl = beforeMap.get(id);
    if (!beforeEl) {
      appeared.push({ id, role: afterEl.role, label: afterEl.label });
    } else if (beforeEl.label !== afterEl.label) {
      value_changed.push({ id, role: afterEl.role, before: beforeEl.label, after: afterEl.label });
    }
  }

  for (const [id, beforeEl] of beforeMap) {
    if (!afterMap.has(id)) {
      disappeared.push({ id, role: beforeEl.role, label: beforeEl.label });
    }
  }

  return { appeared, disappeared, value_changed };
}

export async function diffLastSnapshot(
  vmId: string,
  vmIp: string,
  app?: string,
  updateBaseline = true,
): Promise<DiffResult> {
  const last = getLastSnapshot(vmId, app);
  if (!last) throw new Error(`No snapshot found for VM ${vmId}${app ? ` app ${app}` : ''}. Call ax_snapshot first.`);

  const current = await callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'see',
    arguments: app ? { app } : {},
  });
  const currentJson = JSON.stringify(current);

  const diff = diffSnapshots(last.snapshot_json, currentJson);

  // Advance the baseline so the next ax_diff_last shows what changed since this call.
  if (updateBaseline) {
    const id = `${vmId}-diff-baseline-${Date.now()}`;
    saveSnapshot(id, vmId, 'diff-baseline', app ?? '', currentJson);
    const filePath = join(SNAPSHOT_DIR, `${id}.json`);
    writeFileSync(filePath, currentJson);
  }

  return diff;
}

export async function diffNamedSnapshots(beforeId: string, afterId: string): Promise<DiffResult> {
  const before = getSnapshot(beforeId);
  const after = getSnapshot(afterId);
  if (!before) throw new Error(`Snapshot not found: ${beforeId}`);
  if (!after) throw new Error(`Snapshot not found: ${afterId}`);
  return diffSnapshots(before.snapshot_json, after.snapshot_json);
}

// ── YAML conversion ───────────────────────────────────────────────────────────

export function snapshotToYaml(snapshotJson: string): string {
  const text = extractText(snapshotJson);
  if (!text) return '(snapshot in unknown format)';
  // Return the raw Peekaboo text — it's already human-readable.
  // Strip the header lines (before "UI Elements:") to reduce noise.
  const idx = text.indexOf('UI Elements:');
  return idx >= 0 ? text.slice(idx) : text;
}

// ── Diff → YAML ───────────────────────────────────────────────────────────────

export function diffToYaml(diff: DiffResult): string {
  const lines: string[] = [];

  if (diff.appeared.length) {
    lines.push('appeared:');
    for (const el of diff.appeared) {
      lines.push(`  - id: "${el.id}", role: ${el.role}, label: "${el.label}"`);
    }
  }
  if (diff.disappeared.length) {
    lines.push('disappeared:');
    for (const el of diff.disappeared) {
      lines.push(`  - id: "${el.id}", role: ${el.role}, label: "${el.label}"`);
    }
  }
  if (diff.value_changed.length) {
    lines.push('value_changed:');
    for (const vc of diff.value_changed) {
      lines.push(`  - id: "${vc.id}", role: ${vc.role}`);
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
}): Array<{ id: string; role: string; label: string; actionable: boolean }> {
  const text = extractText(snapshotJson);
  if (!text) return [];
  const elements = parsePeekabooText(text);
  return elements.filter(el => {
    if (opts.identifier && el.id !== opts.identifier) return false;
    if (opts.role && el.role !== opts.role) return false;
    if (opts.label && !el.label.toLowerCase().includes(opts.label.toLowerCase())) return false;
    return true;
  });
}
