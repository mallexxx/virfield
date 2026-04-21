/**
 * Peekaboo MCP proxy — routes MCP tool calls to the Peekaboo MCP server
 * running inside a specific VM, via an SSH port-forward tunnel.
 *
 * Each VM runs: socat TCP-LISTEN:4040 EXEC:peekaboo-mcp-stdio
 * We open: SSH -L localPort:127.0.0.1:7888 lume@vmIp
 * Then connect to localhost:localPort and speak MCP JSON-RPC over it.
 */

import { createConnection, Socket } from 'net';
import { ensureTunnel } from './tunnel-manager.js';

interface MCPRequest {
  jsonrpc: '2.0';
  id: number | string;
  method: string;
  params?: object;
}

interface MCPResponse {
  jsonrpc: '2.0';
  id: number | string;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

let requestId = 1;

// ── Tool name cache — fetched from tools/list on first use per VM ──────────────

const toolsCache = new Map<string, Set<string>>();

/** Fetch the list of tool names from Peekaboo via tools/list. Cached per VM. */
async function getToolNames(vmId: string, vmIp: string): Promise<Set<string>> {
  if (toolsCache.has(vmId)) return toolsCache.get(vmId)!;

  const { localPort } = await ensureTunnel(vmId, vmIp);

  const tools = await new Promise<Set<string>>((resolve, reject) => {
    const socket = createConnection({ host: '127.0.0.1', port: localPort });
    const id = requestId++;
    let buffer = '';
    let initialized = false;

    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error('Peekaboo tools/list timed out'));
    }, 15_000);

    socket.on('connect', () => {
      socket.write(JSON.stringify({
        jsonrpc: '2.0',
        id: `init-${id}`,
        method: 'initialize',
        params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'virfield', version: '0.1.0' } },
      }) + '\n');
    });

    socket.on('data', (data: Buffer) => {
      buffer += data.toString();
      const lines = buffer.split('\n');
      buffer = lines.pop()!;
      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          const msg = JSON.parse(line) as MCPResponse;
          if (!initialized && String(msg.id).startsWith('init-')) {
            initialized = true;
            socket.write(JSON.stringify({ jsonrpc: '2.0', id, method: 'tools/list', params: {} }) + '\n');
            return;
          }
          if (msg.id === id) {
            clearTimeout(timer);
            socket.destroy();
            if (msg.error) {
              reject(new Error(`tools/list error: ${msg.error.message}`));
            } else {
              const list = (msg.result as { tools?: Array<{ name: string }> })?.tools ?? [];
              resolve(new Set(list.map(t => t.name)));
            }
          }
        } catch { /* partial */ }
      }
    });

    socket.on('error', (err) => { clearTimeout(timer); reject(err); });
  });

  toolsCache.set(vmId, tools);
  return tools;
}

/** Clear cached tool names for a VM (e.g. after tunnel reconnect). */
export function clearPeekabooToolCache(vmId: string) {
  toolsCache.delete(vmId);
}

/**
 * Send a single MCP tool call to the Peekaboo server in the specified VM.
 * Opens a fresh TCP connection for each call (stateless).
 */
export async function callPeekaboo(
  vmId: string,
  vmIp: string,
  method: string,
  params: object = {},
): Promise<unknown> {
  // Validate tool name against tools/list on first use per VM
  if (method === 'tools/call') {
    const toolName = (params as { name?: string }).name;
    if (toolName) {
      try {
        const available = await getToolNames(vmId, vmIp);
        if (!available.has(toolName)) {
          throw new Error(
            `Peekaboo tool "${toolName}" not found in VM ${vmId}. ` +
            `Available tools: ${[...available].join(', ')}`
          );
        }
      } catch (err) {
        // If validation itself fails (tunnel not ready etc.), let the call proceed
        // and surface a real error from the Peekaboo server.
        if (String(err).includes('not found in VM')) throw err;
        console.warn(`[peekaboo] tool validation skipped for ${vmId}:`, String(err));
      }
    }
  }

  const { localPort } = await ensureTunnel(vmId, vmIp);

  return new Promise((resolve, reject) => {
    const socket: Socket = createConnection({ host: '127.0.0.1', port: localPort });
    const id = requestId++;
    let buffer = '';

    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error(`Peekaboo MCP call timed out: ${method}`));
    }, 30_000);

    socket.on('connect', () => {
      // MCP initialize handshake
      const init: MCPRequest = {
        jsonrpc: '2.0',
        id: `init-${id}`,
        method: 'initialize',
        params: {
          protocolVersion: '2024-11-05',
          capabilities: {},
          clientInfo: { name: 'virfield', version: '0.1.0' },
        },
      };
      socket.write(JSON.stringify(init) + '\n');
    });

    let initialized = false;

    socket.on('data', (data: Buffer) => {
      buffer += data.toString();
      const lines = buffer.split('\n');
      buffer = lines.pop()!;

      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          const msg = JSON.parse(line) as MCPResponse;

          if (!initialized && String(msg.id).startsWith('init-')) {
            // Handshake complete — send the actual tool call
            initialized = true;
            const req: MCPRequest = {
              jsonrpc: '2.0',
              id,
              method,
              params,
            };
            socket.write(JSON.stringify(req) + '\n');
            return;
          }

          if (msg.id === id) {
            clearTimeout(timer);
            socket.destroy();
            if (msg.error) {
              reject(new Error(`Peekaboo error: ${msg.error.message}`));
            } else {
              resolve(msg.result);
            }
          }
        } catch {
          // Partial JSON — wait for more data
        }
      }
    });

    socket.on('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });
}

/**
 * Send multiple MCP tool calls in a single persistent session.
 * Required for stateful tools like Peekaboo's click-with-on which depends on
 * a prior `see` snapshot captured in the same connection.
 */
export async function callPeekabooSession(
  vmId: string,
  vmIp: string,
  calls: Array<{ name: string; arguments: Record<string, unknown> }>,
): Promise<unknown[]> {
  const { localPort } = await ensureTunnel(vmId, vmIp);

  return new Promise((resolve, reject) => {
    const socket: Socket = createConnection({ host: '127.0.0.1', port: localPort });
    const baseId = requestId++;
    let buffer = '';
    let initialized = false;
    let callIndex = 0;
    const results: unknown[] = [];

    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error('Peekaboo session timed out'));
    }, 30_000);

    const sendNext = () => {
      if (callIndex >= calls.length) {
        clearTimeout(timer);
        socket.destroy();
        resolve(results);
        return;
      }
      const call = calls[callIndex];
      socket.write(JSON.stringify({
        jsonrpc: '2.0',
        id: `${baseId}-${callIndex}`,
        method: 'tools/call',
        params: { name: call.name, arguments: call.arguments },
      }) + '\n');
    };

    socket.on('connect', () => {
      socket.write(JSON.stringify({
        jsonrpc: '2.0',
        id: `init-${baseId}`,
        method: 'initialize',
        params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'virfield', version: '0.1.0' } },
      }) + '\n');
    });

    socket.on('data', (data: Buffer) => {
      buffer += data.toString();
      const lines = buffer.split('\n');
      buffer = lines.pop()!;
      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          const msg = JSON.parse(line) as MCPResponse;
          if (!initialized && String(msg.id).startsWith('init-')) {
            initialized = true;
            socket.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized', params: {} }) + '\n');
            sendNext();
            return;
          }
          if (msg.id === `${baseId}-${callIndex}`) {
            if (msg.error) {
              clearTimeout(timer);
              socket.destroy();
              reject(new Error(`Peekaboo session error at step ${callIndex}: ${msg.error.message}`));
              return;
            }
            results.push(msg.result);
            callIndex++;
            sendNext();
          }
        } catch { /* partial */ }
      }
    });

    socket.on('error', (err) => { clearTimeout(timer); reject(err); });
  });
}

// ── Convenience wrappers for individual Peekaboo tools ───────────────────────

export async function peekabooSee(vmId: string, vmIp: string, app?: string) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'see',
    arguments: app ? { app } : {},
  });
}

export async function peekabooScreenshot(vmId: string, vmIp: string, app?: string) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'screenshot',
    arguments: app ? { app } : {},
  });
}

export async function peekabooClick(vmId: string, vmIp: string, identifier: string, app?: string) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'click',
    arguments: { identifier, ...(app ? { app } : {}) },
  });
}

export async function peekabooType(vmId: string, vmIp: string, text: string, app?: string) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'type',
    arguments: { text, ...(app ? { app } : {}) },
  });
}

export async function peekabooHotkey(vmId: string, vmIp: string, key: string, modifiers?: string[]) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'hotkey',
    arguments: { key, ...(modifiers ? { modifiers } : {}) },
  });
}

export async function peekabooScroll(vmId: string, vmIp: string, direction: string, amount?: number) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'scroll',
    arguments: { direction, ...(amount !== undefined ? { amount } : {}) },
  });
}

export async function peekabooListApps(vmId: string, vmIp: string) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'list_apps',
    arguments: {},
  });
}

export async function peekabooPermissions(vmId: string, vmIp: string) {
  return callPeekaboo(vmId, vmIp, 'tools/call', {
    name: 'permissions',
    arguments: { action: 'status' },
  });
}
