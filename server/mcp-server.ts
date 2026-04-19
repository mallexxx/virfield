/**
 * VM Console MCP Server — stdio transport.
 * Configured in Cursor / Claude Code mcp.json:
 *
 *   {
 *     "virfield": {
 *       "command": "npx",
 *       "args": ["tsx", "/path/to/virfield/server/mcp-server.ts"]
 *     }
 *   }
 *
 * See mcp.json.example for a ready-to-use template.
 * Shares the same SQLite DB as the web console.
 */

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  Tool,
} from '@modelcontextprotocol/sdk/types.js';
import { z } from 'zod';

import * as lume from './lume.js';
import * as db from './db.js';
import { checkSSH, sshExec } from './ssh.js';
import { ensureTunnel } from './tunnel-manager.js';
import {
  peekabooSee,
  peekabooScreenshot,
  peekabooClick,
  peekabooType,
  peekabooHotkey,
  peekabooScroll,
  peekabooListApps,
  peekabooPermissions,
} from './peekaboo-proxy.js';
import {
  takeSnapshot,
  diffLastSnapshot,
  diffNamedSnapshots,
  snapshotToYaml,
  querySnapshot,
  diffToYaml,
} from './ax-tools.js';
import { listSnapshots, getSnapshot } from './db.js';
import { join } from 'path';
import { createConnection } from 'net';
import { spawn } from 'child_process';
import { existsSync } from 'fs';
import { SCRIPTS_DIR, LOG_BASE, STATE_DIR, RECORDINGS_DIR, VMSHARE as VMSHARE_CFG } from './config.js';
import { STAGE_SCRIPT_MAP, STAGE_ORDER, StageKey, buildStageArgs } from './stages.js';
import { readFileSync, readdirSync, statSync } from 'fs';

const VMSHARE = VMSHARE_CFG;

// ── Tool definitions ──────────────────────────────────────────────────────────

const tools: Tool[] = [
  // ── VM Lifecycle ──
  {
    name: 'vm_list',
    description: 'List all VMs with their state, IP address, and setup checklist status.',
    inputSchema: { type: 'object', properties: {}, required: [] },
  },
  {
    name: 'vm_start',
    description: 'Start a VM by name. Optionally clone from golden first.',
    inputSchema: {
      type: 'object',
      properties: {
        name: { type: 'string', description: 'VM name' },
        cloneFromGolden: { type: 'boolean', description: 'Clone from golden image before starting' },
        noDisplay: { type: 'boolean', description: 'Start without display (headless)', default: true },
      },
      required: ['name'],
    },
  },
  {
    name: 'vm_stop',
    description: 'Stop a running VM.',
    inputSchema: {
      type: 'object',
      properties: { name: { type: 'string' } },
      required: ['name'],
    },
  },
  {
    name: 'vm_clone_golden',
    description: 'Create an APFS CoW clone from the golden image. Returns the new VM name.',
    inputSchema: {
      type: 'object',
      properties: {
        destName: { type: 'string', description: 'Name for the new VM (default: vm-run-<timestamp>)' },
      },
      required: [],
    },
  },
  {
    name: 'vm_delete',
    description: 'Delete a VM (use only for run/dev VMs, not golden).',
    inputSchema: {
      type: 'object',
      properties: { name: { type: 'string' } },
      required: ['name'],
    },
  },
  {
    name: 'vm_get_ip',
    description: 'Get the current IP address of a running VM.',
    inputSchema: {
      type: 'object',
      properties: { name: { type: 'string' } },
      required: ['name'],
    },
  },
  {
    name: 'vm_status',
    description: 'Get full status of a VM including checklist and peekaboo connection.',
    inputSchema: {
      type: 'object',
      properties: { name: { type: 'string' } },
      required: ['name'],
    },
  },
  {
    name: 'vm_prepare_session',
    description: 'Clone golden → start VM → wait for SSH → wait for socat/Peekaboo ready → return VM name and IP. The standard agent entry point for a test session.',
    inputSchema: {
      type: 'object',
      properties: {
        sessionName: { type: 'string', description: 'Optional name for the session VM' },
      },
      required: [],
    },
  },
  {
    name: 'vm_run_stage',
    description: 'Run one of the 4 virfield pipeline phases for a VM. Waits for completion and returns stdout+stderr. Stages: create_vm, setup_assistant, disable_sip, provision_vm.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id:    { type: 'string', description: 'Golden/target VM name' },
        stage:    { type: 'string', enum: ['create_vm', 'setup_assistant', 'disable_sip', 'provision_vm'] },
        ipsw:     { type: 'string', description: 'IPSW path or "latest" (create_vm only)' },
        xcode:    { type: 'string', description: 'Xcode.app path (provision_vm only)' },
        tools:    { type: 'string', description: 'Tool IDs comma-separated (provision_vm, default: all)' },
        base_vm:  { type: 'string', description: 'Base VM name override (default: <vm_id>-base)' },
        nosip_vm: { type: 'string', description: 'NoSIP VM name override (default: <vm_id>-nosip)' },
      },
      required: ['vm_id', 'stage'],
    },
  },
  {
    name: 'vm_build_golden',
    description: 'Run the full 4-phase build-golden-vm.sh orchestrator to create a golden VM from an IPSW. Returns immediately; poll vm_get_build_state for progress.',
    inputSchema: {
      type: 'object',
      properties: {
        golden_vm: { type: 'string', description: 'Name for the resulting golden VM' },
        ipsw:      { type: 'string', description: 'IPSW path or "latest"' },
        xcode:     { type: 'string', description: 'Xcode.app path (optional)' },
        tools:     { type: 'string', description: 'Tool IDs (default: all)' },
        record:    { type: 'boolean', description: 'Record VNC phases to mp4' },
        start_phase: { type: 'number', description: 'Start from phase N (1–4)' },
      },
      required: ['golden_vm', 'ipsw'],
    },
  },
  {
    name: 'vm_get_build_state',
    description: 'Read state.json for a VM. Returns the current phase statuses, log dir, and recording path.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string', description: 'VM name' },
      },
      required: ['vm_id'],
    },
  },
  {
    name: 'vm_list_recordings',
    description: 'List VNC recording (.mp4) files from build log dirs for a VM.',
    inputSchema: {
      type: 'object',
      properties: { vm_id: { type: 'string' } },
      required: ['vm_id'],
    },
  },
  {
    name: 'vm_list_screenshots',
    description: 'List screenshot (.png/.jpg) files from build log dirs for a VM.',
    inputSchema: {
      type: 'object',
      properties: { vm_id: { type: 'string' } },
      required: ['vm_id'],
    },
  },
  // ── Peekaboo tools ──
  {
    name: 'peekaboo_see',
    description: 'Dump the accessibility (AX) tree for an app in the given VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string', description: 'VM name' },
        app: { type: 'string', description: 'App bundle ID or name (optional — defaults to frontmost app)' },
      },
      required: ['vm_id'],
    },
  },
  {
    name: 'peekaboo_image',
    description: 'Take a screenshot from the given VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        app: { type: 'string' },
      },
      required: ['vm_id'],
    },
  },
  {
    name: 'peekaboo_click',
    description: 'Click an element by accessibility identifier in the given VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        identifier: { type: 'string', description: 'Accessibility identifier of the element to click' },
        app: { type: 'string' },
      },
      required: ['vm_id', 'identifier'],
    },
  },
  {
    name: 'peekaboo_type',
    description: 'Type text in the given VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        text: { type: 'string' },
        app: { type: 'string' },
      },
      required: ['vm_id', 'text'],
    },
  },
  {
    name: 'peekaboo_hotkey',
    description: 'Send a hotkey combination in the given VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        key: { type: 'string', description: 'Key name (e.g. "l", "return", "escape")' },
        modifiers: {
          type: 'array',
          items: { type: 'string' },
          description: 'Modifier keys (e.g. ["command", "shift"])',
        },
      },
      required: ['vm_id', 'key'],
    },
  },
  {
    name: 'peekaboo_scroll',
    description: 'Scroll in the given VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        direction: { type: 'string', enum: ['up', 'down', 'left', 'right'] },
        amount: { type: 'number', description: 'Scroll amount (default: 3)' },
      },
      required: ['vm_id', 'direction'],
    },
  },
  {
    name: 'peekaboo_list_apps',
    description: 'List running apps in the given VM.',
    inputSchema: {
      type: 'object',
      properties: { vm_id: { type: 'string' } },
      required: ['vm_id'],
    },
  },
  {
    name: 'peekaboo_permissions',
    description: 'Check Peekaboo permission status (Screen Recording, Accessibility) in the given VM.',
    inputSchema: {
      type: 'object',
      properties: { vm_id: { type: 'string' } },
      required: ['vm_id'],
    },
  },
  // ── AX snapshot/diff tools ──
  {
    name: 'ax_snapshot',
    description: 'Take a named AX snapshot of an app in the given VM and store it for later diffing.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        name: { type: 'string', description: 'Label for this snapshot, e.g. "before-new-tab"' },
        app: { type: 'string' },
      },
      required: ['vm_id', 'name'],
    },
  },
  {
    name: 'ax_diff_last',
    description: 'Diff current AX state against the last snapshot for a VM/app. Returns appeared/disappeared/value_changed.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        app: { type: 'string' },
      },
      required: ['vm_id'],
    },
  },
  {
    name: 'ax_diff',
    description: 'Diff two named snapshots by ID.',
    inputSchema: {
      type: 'object',
      properties: {
        before_id: { type: 'string' },
        after_id: { type: 'string' },
      },
      required: ['before_id', 'after_id'],
    },
  },
  {
    name: 'ax_query',
    description: 'Query specific elements from the latest snapshot by identifier, role, or label.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        identifier: { type: 'string' },
        role: { type: 'string' },
        label: { type: 'string' },
      },
      required: ['vm_id'],
    },
  },
  // ── Test runner ──
  {
    name: 'run_tests',
    description: 'Launch XCUITests in a running VM via xcodebuild test-without-building. Returns immediately with a run_id (timestamp) and results_dir path. Poll get_test_results or use vm_ssh_exec to check progress. Uses the pre-built xctestrun from VMShare/DerivedData.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id:        { type: 'string', description: 'VM name (must be running)' },
        test_suite:   { type: 'string', description: 'XCTest suite/class to run, e.g. "ErrorPageUITests". Omit for all UI Tests.' },
        only_testing: { type: 'array', items: { type: 'string' }, description: 'Specific test methods, e.g. ["UI Tests/ErrorPageUITests/testFoo"]. Overrides test_suite.' },
        xctestrun:    { type: 'string', description: 'Override xctestrun filename in VMShare/DerivedData/Build/Products/ (default: macOS UI Tests_UI Tests_macosx26.4-arm64-x86_64.xctestrun)' },
        iterations:   { type: 'number', description: 'Retry failing tests N times (default: 2)' },
      },
      required: ['vm_id'],
    },
  },
  {
    name: 'get_test_run_status',
    description: 'Check if a test run is still in progress by polling xcodebuild process and log tail. Returns { running: bool, lastLines: string }.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id:  { type: 'string' },
        run_id: { type: 'string', description: 'Timestamp returned by run_tests' },
      },
      required: ['vm_id', 'run_id'],
    },
  },
  // ── SSH exec ──
  {
    name: 'vm_ssh_exec',
    description: 'Run a shell command via SSH on a running VM. Returns stdout, stderr, and exit code.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id:   { type: 'string', description: 'VM name' },
        command: { type: 'string', description: 'Shell command to run (executed as the lume user)' },
        timeout_ms: { type: 'number', description: 'Timeout in milliseconds (default: 30000, max: 300000)' },
      },
      required: ['vm_id', 'command'],
    },
  },
  // ── Build & test tools ──
  {
    name: 'get_test_results',
    description: 'List and parse test result artifacts from ~/VMShare/results/. Parses junit.xml into structured YAML (suites → tests with pass/fail/error/duration). Also surfaces .xcresult summaries and any JSON reports. Use after running tests via vm_ssh_exec.',
    inputSchema: {
      type: 'object',
      properties: {
        parse: { type: 'boolean', description: 'Parse recognised formats (junit.xml, summary.json) into YAML. Default: true', default: true },
      },
      required: [],
    },
  },
  {
    name: 'get_crash_reports',
    description: 'List and read macOS crash reports from a running VM. Looks in ~/Library/Logs/DiagnosticReports/ and /Library/Logs/DiagnosticReports/. Returns the most recent reports in YAML-frontmatter format.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id:   { type: 'string', description: 'VM name' },
        app:     { type: 'string', description: 'Filter by application name (optional)' },
        limit:   { type: 'number', description: 'Max reports to return (default: 5)' },
        content: { type: 'boolean', description: 'Include full crash log content (default: false — just metadata)' },
      },
      required: ['vm_id'],
    },
  },
  {
    name: 'get_log_stream',
    description: 'Fetch the last N lines of app-console.log from a VM.',
    inputSchema: {
      type: 'object',
      properties: {
        vm_id: { type: 'string' },
        lines: { type: 'number', default: 200 },
      },
      required: ['vm_id'],
    },
  },
];

// ── Helper: wait for TCP port to accept connections ───────────────────────────

function waitForTCPPort(host: string, port: number, timeoutMs: number): Promise<void> {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs;
    function attempt() {
      const sock = createConnection({ host, port });
      sock.once('connect', () => { sock.destroy(); resolve(); });
      sock.once('error', () => {
        sock.destroy();
        if (Date.now() > deadline) {
          reject(new Error(`TCP port ${host}:${port} not available after ${timeoutMs}ms`));
        } else {
          setTimeout(attempt, 1000);
        }
      });
    }
    attempt();
  });
}

// ── Helper: run a script synchronously and capture output ────────────────────

function runStageScript(scriptPath: string, scriptArgs: string[]): Promise<{ code: number; output: string }> {
  return new Promise((resolve, reject) => {
    const proc = spawn('bash', [scriptPath, ...scriptArgs], {
      env: process.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';

    const onData = (d: Buffer) => {
      output += d.toString();
      if (output.length > 65536) output = output.slice(-65536);
    };
    proc.stdout?.on('data', onData);
    proc.stderr?.on('data', onData);

    proc.on('exit', (code) => resolve({ code: code ?? 1, output }));
    proc.on('error', (err) => reject(err));
  });
}

// ── Helper: scan RECORDINGS_DIR for mp4/mov files belonging to a VM ──────────

function scanRecordingsDir(vmId: string): Array<{ path: string; name: string; size: number; mtime: number }> {
  const results: Array<{ path: string; name: string; size: number; mtime: number }> = [];
  if (!existsSync(RECORDINGS_DIR)) return results;
  try {
    readdirSync(RECORDINGS_DIR)
      .filter(f => f.includes(vmId) && /\.(mp4|mov)$/i.test(f))
      .forEach(f => {
        const fullPath = join(RECORDINGS_DIR, f);
        try {
          const s = statSync(fullPath);
          results.push({ path: fullPath, name: f, size: s.size, mtime: s.mtimeMs });
        } catch { /* skip */ }
      });
  } catch { /* skip */ }
  return results.sort((a, b) => b.mtime - a.mtime);
}

// ── Helper: scan log dirs for screenshots ────────────────────────────────────

function scanLogDir(vmId: string, exts: string[]): Array<{ path: string; name: string; size: number; mtime: number }> {
  const results: Array<{ path: string; name: string; size: number; mtime: number }> = [];
  if (!existsSync(LOG_BASE)) return results;
  try {
    const entries = readdirSync(LOG_BASE).filter(e => e.includes(vmId) && e !== `${vmId}-latest`);
    for (const dir of entries) {
      const dirPath = join(LOG_BASE, dir);
      try {
        if (!statSync(dirPath).isDirectory()) continue;
        for (const f of readdirSync(dirPath)) {
          const ext = f.toLowerCase().split('.').pop() ?? '';
          if (!exts.includes(ext)) continue;
          const fullPath = join(dirPath, f);
          const s = statSync(fullPath);
          results.push({ path: fullPath, name: f, size: s.size, mtime: s.mtimeMs });
        }
      } catch { /* skip */ }
    }
  } catch { /* skip */ }
  return results.sort((a, b) => b.mtime - a.mtime);
}

// ── Helper: resolve VM IP from name ──────────────────────────────────────────

async function resolveVMIp(vmId: string): Promise<string> {
  const vm = await lume.getVM(vmId);
  if (!vm.ipAddress) throw new Error(`VM ${vmId} has no IP — is it running?`);
  return vm.ipAddress;
}

// ── MCP Server ────────────────────────────────────────────────────────────────

const server = new Server(
  { name: 'virfield', version: '0.1.0' },
  { capabilities: { tools: {} } },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools }));

server.setRequestHandler(CallToolRequestSchema, async (req) => {
  const { name, arguments: args } = req.params;
  const a = args as Record<string, unknown>;

  try {
    switch (name) {

      // ── VM Lifecycle ─────────────────────────────────────────────────────

      case 'vm_list': {
        const vms = await lume.listVMs();
        const result = vms.map(vm => ({
          name: vm.name,
          status: vm.status,
          ip: vm.ipAddress,
          vnc: vm.vncUrl,
          cpu: vm.cpuCount,
          memoryGB: Math.round(vm.memorySize / 1e9 * 10) / 10,
          stages: db.getStages(vm.name),
        }));
        return { content: [{ type: 'text', text: JSON.stringify(result, null, 2) }] };
      }

      case 'vm_start': {
        const vmName = String(a.name);
        if (a.cloneFromGolden) {
          await lume.cloneVM('uitest-golden', vmName);
          db.upsertVM({ id: vmName, tag: 'run' });
        }
        await lume.startVM(vmName, { noDisplay: (a.noDisplay as boolean) ?? true, sharedDir: VMSHARE });
        db.touchVMRun(vmName);
        return { content: [{ type: 'text', text: `VM ${vmName} start requested` }] };
      }

      case 'vm_stop': {
        await lume.stopVM(String(a.name));
        return { content: [{ type: 'text', text: `VM ${a.name} stopped` }] };
      }

      case 'vm_clone_golden': {
        const dest = String(a.destName ?? `vm-run-${Date.now()}`);
        await lume.cloneVM('uitest-golden', dest);
        db.upsertVM({ id: dest, tag: 'run' });
        return { content: [{ type: 'text', text: `Cloned golden → ${dest}` }] };
      }

      case 'vm_delete': {
        await lume.deleteVM(String(a.name));
        db.deleteVM(String(a.name));
        return { content: [{ type: 'text', text: `VM ${a.name} deleted` }] };
      }

      case 'vm_get_ip': {
        const vm = await lume.getVM(String(a.name));
        return { content: [{ type: 'text', text: vm.ipAddress ?? 'No IP (VM not running)' }] };
      }

      case 'vm_status': {
        const [vm, stages] = await Promise.all([
          lume.getVM(String(a.name)),
          db.getStages(String(a.name)),
        ]);
        const sshOk = vm.ipAddress ? await checkSSH(vm.ipAddress) : false;
        return {
          content: [{
            type: 'text',
            text: JSON.stringify({ ...vm, stages, sshAvailableNow: sshOk }, null, 2),
          }],
        };
      }

      case 'vm_prepare_session': {
        const sessionName = String(a.sessionName ?? `vm-run-${Date.now()}`);

        // 1. Clone golden
        await lume.cloneVM('uitest-golden', sessionName);
        db.upsertVM({ id: sessionName, tag: 'run' });

        // 2. Start VM
        await lume.startVM(sessionName, { noDisplay: true, sharedDir: VMSHARE });
        db.touchVMRun(sessionName);

        // 3. Wait for IP
        const ip = await lume.waitForIP(sessionName, 180_000);

        // 4. Wait for SSH
        let sshReady = false;
        for (let i = 0; i < 20; i++) {
          await new Promise(r => setTimeout(r, 5000));
          sshReady = await checkSSH(ip);
          if (sshReady) break;
        }
        if (!sshReady) throw new Error(`SSH not available on ${sessionName} (${ip})`);

        // 5. Ensure tunnel
        const { localPort } = await ensureTunnel(sessionName, ip);

        // 6. Verify tunnel port is actually accepting connections (B6)
        await waitForTCPPort('127.0.0.1', localPort, 30_000);

        return {
          content: [{
            type: 'text',
            text: JSON.stringify({ vm_id: sessionName, ip, localPort, status: 'ready' }, null, 2),
          }],
        };
      }

      case 'vm_run_stage': {
        const vmId = String(a.vm_id);
        const stage = String(a.stage);

        if (!STAGE_ORDER.includes(stage as StageKey)) {
          throw new Error(`Unknown stage: ${stage}. Valid: ${STAGE_ORDER.join(', ')}`);
        }

        const scriptFile = STAGE_SCRIPT_MAP[stage];
        const scriptPath = join(SCRIPTS_DIR, scriptFile);
        if (!existsSync(scriptPath)) {
          throw new Error(`Script not found: ${scriptPath}. Set VMCONSOLE_SCRIPTS_DIR.`);
        }

        const vmNames = {
          baseVm:   String(a.base_vm  ?? `${vmId}-base`),
          nosipVm:  String(a.nosip_vm ?? `${vmId}-nosip`),
          goldenVm: vmId,
        };
        const scriptArgs = buildStageArgs(stage as StageKey, vmNames, {
          ipsw:    a.ipsw  ? String(a.ipsw)  : undefined,
          xcode:   a.xcode ? String(a.xcode) : undefined,
          tools:   a.tools ? String(a.tools) : undefined,
          vmshare: VMSHARE,
        });

        db.setStageStatus(vmId, stage, 'running');
        const { code, output } = await runStageScript(scriptPath, scriptArgs);
        const status = code === 0 ? 'done' : 'failed';
        db.setStageStatus(vmId, stage, status, output);

        return {
          content: [{
            type: 'text',
            text: `Stage ${stage} ${status} (exit ${code})\n\n${output}`,
          }],
        };
      }

      case 'vm_build_golden': {
        const goldenVm = String(a.golden_vm);
        const ipsw     = String(a.ipsw ?? 'latest');
        const orchestrator = join(SCRIPTS_DIR, 'build-golden-vm.sh');
        if (!existsSync(orchestrator)) {
          throw new Error(`Orchestrator not found: ${orchestrator}. Set VMCONSOLE_SCRIPTS_DIR.`);
        }

        const args: string[] = ['--ipsw', ipsw, '--golden-vm', goldenVm];
        if (a.xcode)       args.push('--xcode', String(a.xcode));
        if (a.tools)       args.push('--tools', String(a.tools));
        if (a.record)      args.push('--record');
        if (a.start_phase) args.push('--start-phase', String(a.start_phase));
        args.push('--vmshare', VMSHARE);

        for (const stage of STAGE_ORDER) db.setStageStatus(goldenVm, stage, 'pending');
        db.upsertVM({ id: goldenVm, tag: 'golden' });

        const proc = spawn('bash', [orchestrator, 'run', ...args], {
          stdio: ['ignore', 'pipe', 'pipe'], detached: false,
        });
        proc.stdout?.on('data', (d: Buffer) => process.stdout.write(`[build:${goldenVm}] ${d}`));
        proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[build:${goldenVm}] ${d}`));
        proc.on('exit', code => console.log(`[build:${goldenVm}] exited: ${code}`));

        return {
          content: [{
            type: 'text',
            text: `Build started for ${goldenVm} (PID ${proc.pid}). Use vm_get_build_state to monitor.`,
          }],
        };
      }

      case 'vm_get_build_state': {
        const vmId = String(a.vm_id);
        const stateFile = join(STATE_DIR, `${vmId}.json`);
        if (!existsSync(stateFile)) {
          return { content: [{ type: 'text', text: JSON.stringify({ vm: vmId, status: 'no_state', stages: {} }) }] };
        }
        const state = JSON.parse(readFileSync(stateFile, 'utf8'));
        return { content: [{ type: 'text', text: JSON.stringify(state, null, 2) }] };
      }

      case 'vm_list_recordings': {
        const vmId = String(a.vm_id);
        const files = scanRecordingsDir(vmId);
        return { content: [{ type: 'text', text: JSON.stringify(files, null, 2) }] };
      }

      case 'vm_list_screenshots': {
        const vmId = String(a.vm_id);
        const files = scanLogDir(vmId, ['png', 'jpg', 'jpeg']);
        return { content: [{ type: 'text', text: JSON.stringify(files, null, 2) }] };
      }

      // ── Peekaboo ─────────────────────────────────────────────────────────

      case 'peekaboo_see': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooSee(String(a.vm_id), ip, a.app as string | undefined);
        return { content: [{ type: 'text', text: JSON.stringify(result, null, 2) }] };
      }

      case 'peekaboo_image': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooScreenshot(String(a.vm_id), ip, a.app as string | undefined);
        // Result may contain base64 image data
        const text = typeof result === 'object' ? JSON.stringify(result) : String(result);
        return { content: [{ type: 'text', text }] };
      }

      case 'peekaboo_click': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooClick(String(a.vm_id), ip, String(a.identifier), a.app as string | undefined);
        return { content: [{ type: 'text', text: JSON.stringify(result) }] };
      }

      case 'peekaboo_type': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooType(String(a.vm_id), ip, String(a.text), a.app as string | undefined);
        return { content: [{ type: 'text', text: JSON.stringify(result) }] };
      }

      case 'peekaboo_hotkey': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooHotkey(String(a.vm_id), ip, String(a.key), a.modifiers as string[] | undefined);
        return { content: [{ type: 'text', text: JSON.stringify(result) }] };
      }

      case 'peekaboo_scroll': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooScroll(String(a.vm_id), ip, String(a.direction), a.amount as number | undefined);
        return { content: [{ type: 'text', text: JSON.stringify(result) }] };
      }

      case 'peekaboo_list_apps': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooListApps(String(a.vm_id), ip);
        return { content: [{ type: 'text', text: JSON.stringify(result, null, 2) }] };
      }

      case 'peekaboo_permissions': {
        const ip = await resolveVMIp(String(a.vm_id));
        const result = await peekabooPermissions(String(a.vm_id), ip);
        return { content: [{ type: 'text', text: JSON.stringify(result, null, 2) }] };
      }

      // ── AX tools ─────────────────────────────────────────────────────────

      case 'ax_snapshot': {
        const ip = await resolveVMIp(String(a.vm_id));
        const meta = await takeSnapshot(String(a.vm_id), ip, String(a.name), a.app as string | undefined);
        // Return snapshot metadata + YAML tree so the agent can see the UI state immediately.
        const snap = getSnapshot(meta.id);
        const yamlTree = snap ? snapshotToYaml(snap.snapshot_json) : '(snapshot data unavailable)';
        return {
          content: [{
            type: 'text',
            text: `snapshot_id: "${meta.id}"\nname: "${meta.name}"\napp: "${meta.app ?? ''}"\n\n${yamlTree}`,
          }],
        };
      }

      case 'ax_diff_last': {
        // Take a fresh snapshot, diff vs last stored — return YAML diff by default.
        const ip = await resolveVMIp(String(a.vm_id));
        const diff = await diffLastSnapshot(String(a.vm_id), ip, a.app as string | undefined);
        return { content: [{ type: 'text', text: diffToYaml(diff) }] };
      }

      case 'ax_diff': {
        const diff = await diffNamedSnapshots(String(a.before_id), String(a.after_id));
        return { content: [{ type: 'text', text: diffToYaml(diff) }] };
      }

      case 'ax_to_yaml': {
        const snap = getSnapshot(String(a.snapshot_id));
        if (!snap) throw new Error(`Snapshot not found: ${a.snapshot_id}`);
        const yaml = snapshotToYaml(snap.snapshot_json);
        return { content: [{ type: 'text', text: yaml }] };
      }

      case 'ax_query': {
        const snaps = listSnapshots(String(a.vm_id));
        if (!snaps.length) throw new Error(`No snapshots for VM ${a.vm_id}`);
        const latest = getSnapshot(snaps[0].id);
        if (!latest) throw new Error('Snapshot data missing');
        const results = querySnapshot(latest.snapshot_json, {
          identifier: a.identifier as string | undefined,
          role: a.role as string | undefined,
          label: a.label as string | undefined,
        });
        // Return YAML — compact one-liner per matched element.
        type El = { role?: string; identifier?: string; label?: string; value?: string };
        const yaml = results.length
          ? (results as El[]).map(el => {
              const p: string[] = [];
              if (el.role) p.push(`role: ${el.role}`);
              if (el.identifier) p.push(`id: "${el.identifier}"`);
              if (el.label) p.push(`label: "${el.label}"`);
              if (el.value) p.push(`value: "${el.value}"`);
              return `- ${p.join(', ')}`;
            }).join('\n')
          : 'no_results: true';
        return { content: [{ type: 'text', text: yaml }] };
      }

      // ── Build & test ──────────────────────────────────────────────────────

      case 'get_test_results': {
        const { readdirSync, statSync, existsSync, readFileSync } = await import('fs');
        const resultsDir = join(VMSHARE, 'results');

        if (!existsSync(resultsDir)) {
          return { content: [{ type: 'text', text: 'results_dir: not found\npath: ~/VMShare/results/' }] };
        }

        const shouldParse = a.parse !== false;
        const entries = readdirSync(resultsDir, { withFileTypes: true });
        const lines: string[] = ['results:'];

        /** Minimal regex-based JUnit XML parser — no dependencies needed. */
        function parseJunit(xml: string): string[] {
          const out: string[] = ['    format: junit_xml', '    suites:'];
          const suiteRe = /<testsuite([^>]*)>([\s\S]*?)<\/testsuite>/g;
          const attrRe = /(\w+)="([^"]*)"/g;
          let sm: RegExpExecArray | null;
          while ((sm = suiteRe.exec(xml)) !== null) {
            const attrs: Record<string,string> = {};
            let am: RegExpExecArray | null;
            const attrSrc = new RegExp(attrRe.source, 'g');
            while ((am = attrSrc.exec(sm[1])) !== null) attrs[am[1]] = am[2];
            out.push(`      - name: "${attrs.name ?? '?'}"`);
            out.push(`        tests: ${attrs.tests ?? '?'}, failures: ${attrs.failures ?? 0}, errors: ${attrs.errors ?? 0}, time: ${attrs.time ?? '?'}s`);
            const body = sm[2];
            const caseRe = /<testcase([^>]*)(?:\/>|>([\s\S]*?)<\/testcase>)/g;
            let cm: RegExpExecArray | null; let shown = 0;
            while ((cm = caseRe.exec(body)) !== null && shown < 25) {
              const ca: Record<string,string> = {};
              const caAttr = new RegExp(attrRe.source, 'g');
              while ((am = caAttr.exec(cm[1])) !== null) ca[am[1]] = am[2];
              const inner = cm[2] ?? '';
              const hasFail = inner.includes('<failure');
              const hasErr  = inner.includes('<error');
              const status  = hasFail ? 'FAIL' : hasErr ? 'ERROR' : 'PASS';
              out.push(`          - [${status}] ${ca.classname ?? ''}.${ca.name ?? ''} (${ca.time ?? '?'}s)`);
              if (hasFail) {
                const msgM = /message="([^"]*)"/.exec(inner);
                if (msgM) out.push(`            failure: "${msgM[1].slice(0, 120)}"`);
              }
              shown++;
            }
          }
          return out;
        }

        for (const entry of entries) {
          const fullPath = join(resultsDir, entry.name);
          const stat = statSync(fullPath);
          const sizekb = Math.round(stat.size / 1024);
          const mtime = new Date(stat.mtimeMs).toISOString();

          lines.push(`  - name: "${entry.name}"`);
          lines.push(`    size_kb: ${sizekb}`);
          lines.push(`    modified: "${mtime}"`);

          if (shouldParse && (entry.name.endsWith('.xml') || entry.name.endsWith('.junit'))) {
            try {
              const xml = readFileSync(fullPath, 'utf8');
              if (xml.includes('<testsuite')) {
                lines.push(...parseJunit(xml));
              } else {
                lines.push('    format: xml');
              }
            } catch { lines.push('    parse_error: true'); }

          } else if (shouldParse && entry.name.endsWith('.json')) {
            try {
              const obj = JSON.parse(readFileSync(fullPath, 'utf8')) as Record<string,unknown>;
              lines.push('    format: json');
              lines.push(`    keys: [${Object.keys(obj).slice(0, 10).join(', ')}]`);
              if (typeof obj.passed === 'number') lines.push(`    passed: ${obj.passed}, failed: ${obj.failed ?? 0}`);
              if (typeof obj.total === 'number') lines.push(`    total: ${obj.total}`);
            } catch { lines.push('    format: json, parse_error: true'); }

          } else if (entry.name.endsWith('.xcresult') && stat.isDirectory()) {
            lines.push('    format: xcresult');
            lines.push('    note: "run: xcrun xcresulttool get --path <path> --format json | head -100"');
          }
        }

        if (entries.length === 0) lines.push('  (empty — no files in ~/VMShare/results/)');
        return { content: [{ type: 'text', text: lines.join('\n') }] };
      }

      case 'get_crash_reports': {
        const ip = await resolveVMIp(String(a.vm_id));
        const appFilter = a.app ? String(a.app) : null;
        const limit = Math.min(Number(a.limit ?? 5), 20);
        const includeContent = a.content === true;

        // List crash reports from both user and system dirs
        const listCmd = [
          `ls -t ~/Library/Logs/DiagnosticReports/*.{crash,ips,hang} 2>/dev/null`,
          `ls -t /Library/Logs/DiagnosticReports/*.{crash,ips,hang} 2>/dev/null`,
        ].join('; ');

        const { stdout: fileList } = await sshExec(ip, listCmd, 15_000);
        let files = fileList.trim().split('\n').filter(Boolean);

        if (appFilter) {
          const lc = appFilter.toLowerCase();
          files = files.filter(f => f.toLowerCase().includes(lc));
        }
        files = files.slice(0, limit);

        if (!files.length) {
          return { content: [{ type: 'text', text: `crash_reports: none${appFilter ? ` matching "${appFilter}"` : ''}` }] };
        }

        const lines: string[] = ['crash_reports:'];
        for (const f of files) {
          const fname = f.split('/').pop() ?? f;
          lines.push(`  - file: "${fname}"`);
          lines.push(`    path: "${f}"`);

          if (includeContent) {
            // Trim to first 100 lines to avoid flooding context
            const { stdout: content } = await sshExec(ip, `head -n 100 '${f}' 2>/dev/null`, 10_000);
            if (content.trim()) {
              lines.push(`    content: |`);
              for (const ln of content.split('\n').slice(0, 100)) {
                lines.push(`      ${ln}`);
              }
            }
          } else {
            // Just the header — first 10 lines has Process, Identifier, Exception type
            const { stdout: header } = await sshExec(ip, `head -n 20 '${f}' 2>/dev/null`, 8_000);
            const relevant = header.split('\n')
              .filter(ln => /^(Process|Identifier|Exception Type|Exception Codes|Crashed Thread|OS Version):/i.test(ln))
              .slice(0, 6);
            if (relevant.length) {
              lines.push(`    summary:`);
              for (const ln of relevant) lines.push(`      ${ln.trim()}`);
            }
          }
        }

        return { content: [{ type: 'text', text: lines.join('\n') }] };
      }

      case 'get_log_stream': {
        const ip = await resolveVMIp(String(a.vm_id));
        const lines = Number(a.lines ?? 200);
        const result = await sshExec(ip, `tail -n ${lines} /tmp/app-console.log 2>/dev/null || echo ''`);
        return { content: [{ type: 'text', text: result.stdout }] };
      }

      case 'vm_ssh_exec': {
        const ip = await resolveVMIp(String(a.vm_id));
        const command = String(a.command);
        const timeoutMs = Math.min(Number(a.timeout_ms ?? 30_000), 300_000);
        const result = await sshExec(ip, command, timeoutMs);
        return {
          content: [{
            type: 'text',
            text: JSON.stringify({ stdout: result.stdout, stderr: result.stderr, code: result.code }, null, 2),
          }],
        };
      }

      case 'run_tests': {
        const vmId = String(a.vm_id);
        const ip = await resolveVMIp(vmId);
        const ts = new Date().toISOString().replace(/[:.]/g, '-').replace('T', '_').slice(0, 19);
        const resultsDir = `/Volumes/My Shared Files/results/${ts}`;
        const xctestrun = String(a.xctestrun ?? 'macOS UI Tests_UI Tests_macosx26.4-arm64-x86_64.xctestrun');
        const xctestrunPath = `/Volumes/My Shared Files/DerivedData/Build/Products/${xctestrun}`;
        const iterations = Number(a.iterations ?? 2);

        // Build -only-testing flags
        const onlyTesting: string[] = Array.isArray(a.only_testing)
          ? (a.only_testing as string[])
          : a.test_suite
          ? [`UI Tests/${String(a.test_suite)}`]
          : [];
        const onlyTestingFlags = onlyTesting.map(t => `-only-testing "${t}"`).join(' ');

        const script = `#!/bin/bash
export PATH="/opt/homebrew/bin:$PATH"
mkdir -p "${resultsDir}"
caffeinate -dims &
CAFFEINATE_PID=$!
trap "kill $CAFFEINATE_PID 2>/dev/null || true" EXIT
screenresolution set 1920x1080x32@60 2>/dev/null || true
# Suppress any "move to Applications" dialogs for the app under test (add app bundle IDs as needed)
# defaults write <your.app.bundle.id> moveToApplicationsFolderAlertSuppress 1 2>/dev/null || true
# Kill any stale app / test runner processes before launching tests (customise for your app):
# pkill -f "YourApp" 2>/dev/null || true; pkill -f "UI Tests-Runner" 2>/dev/null || true
sleep 2
xcodebuild test-without-building \\
  -xctestrun "${xctestrunPath}" \\
  -destination 'platform=macOS,arch=arm64' \\
  -derivedDataPath "/Volumes/My Shared Files/DerivedData" \\
  -skipPackagePluginValidation -skipMacroValidation \\
  ${onlyTestingFlags} \\
  -test-iterations ${iterations} -retry-tests-on-failure \\
  -test-repetition-relaunch-enabled YES \\
  -resultBundlePath "${resultsDir}/UITests.xcresult" \\
  2>&1 | tee "${resultsDir}/ui-tests.log"
TEST_EXIT=\${PIPESTATUS[0]}
xcbeautify --report junit --report-path "${resultsDir}" --junit-report-filename "report.xml" < "${resultsDir}/ui-tests.log" 2>/dev/null || true
echo "=== DONE exit=\${TEST_EXIT} ==="
exit \$TEST_EXIT`;

        const scriptPath = `/Volumes/My Shared Files/run-tests-${ts}.sh`;
        // Write script to VMShare (accessible from host)
        const { writeFileSync, mkdirSync } = await import('fs');
        // Create results dir on host first so nohup redirect succeeds
        mkdirSync(`${VMSHARE}/results/${ts}`, { recursive: true });
        writeFileSync(`${VMSHARE}/run-tests-${ts}.sh`, script, { mode: 0o755 });

        // Launch in background via SSH; nohup so it survives SSH disconnect
        await sshExec(ip, `nohup bash "${scriptPath}" > "${resultsDir}/runner.log" 2>&1 &`, 10_000);

        return {
          content: [{
            type: 'text',
            text: JSON.stringify({ run_id: ts, results_dir: resultsDir, status: 'running', xctestrun, only_testing: onlyTesting }, null, 2),
          }],
        };
      }

      case 'get_test_run_status': {
        const vmId = String(a.vm_id);
        const runId = String(a.run_id);
        const ip = await resolveVMIp(vmId);
        const resultsDir = `/Volumes/My Shared Files/results/${runId}`;

        const [procResult, logResult] = await Promise.all([
          sshExec(ip, `pgrep -f 'xcodebuild.*${runId}' > /dev/null 2>&1 && echo running || echo done`, 5_000),
          sshExec(ip, `tail -20 "${resultsDir}/ui-tests.log" 2>/dev/null || tail -20 "${resultsDir}/runner.log" 2>/dev/null || echo ''`, 5_000),
        ]);
        const running = procResult.stdout.trim() === 'running';
        return {
          content: [{
            type: 'text',
            text: JSON.stringify({ run_id: runId, running, lastLines: logResult.stdout.trim() }, null, 2),
          }],
        };
      }

      default:
        throw new Error(`Unknown tool: ${name}`);
    }
  } catch (err) {
    return {
      isError: true,
      content: [{ type: 'text', text: `Error: ${String(err)}` }],
    };
  }
});

// ── Start ─────────────────────────────────────────────────────────────────────

const transport = new StdioServerTransport();

async function main() {
  // Ensure lume serve is running (best-effort — MCP server may start before web console)
  try {
    await lume.ensureLumeServe();
  } catch {
    // MCP server can still work with CLI fallback
  }

  await server.connect(transport);
  process.stderr.write('[virfield-mcp] Server started\n');
}

main().catch(err => {
  process.stderr.write(`[virfield-mcp] Fatal: ${err}\n`);
  process.exit(1);
});
