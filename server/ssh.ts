/**
 * SSH client and stage runner.
 * Uses the ssh2 library for programmatic SSH — no shell escaping needed.
 */

import { Client, ConnectConfig } from 'ssh2';
import { homedir } from 'os';
import { readFileSync, existsSync } from 'fs';
import { join } from 'path';

const VM_USER = 'lume';
const SSH_PORT = 22;
const DEFAULT_TIMEOUT = 30_000;

function getHostKey(): Buffer | undefined {
  const candidates = [
    join(homedir(), '.ssh', 'id_ed25519'),
    join(homedir(), '.ssh', 'id_rsa'),
  ];
  for (const p of candidates) {
    if (existsSync(p)) return readFileSync(p);
  }
  return undefined;
}

// ── Single command execution ──────────────────────────────────────────────────

export interface SSHResult {
  stdout: string;
  stderr: string;
  code: number;
}

export function sshExec(ip: string, command: string, timeoutMs = DEFAULT_TIMEOUT): Promise<SSHResult> {
  return new Promise((resolve, reject) => {
    const conn = new Client();
    let stdout = '';
    let stderr = '';

    const timer = setTimeout(() => {
      conn.destroy();
      reject(new Error(`SSH command timed out after ${timeoutMs}ms: ${command}`));
    }, timeoutMs);

    conn.on('ready', () => {
      conn.exec(command, (err, stream) => {
        if (err) {
          clearTimeout(timer);
          conn.end();
          return reject(err);
        }

        stream.on('data', (d: Buffer) => { stdout += d.toString(); });
        stream.stderr.on('data', (d: Buffer) => { stderr += d.toString(); });
        stream.on('close', (code: number) => {
          clearTimeout(timer);
          conn.end();
          resolve({ stdout, stderr, code });
        });
      });
    });

    conn.on('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });

    const config: ConnectConfig = {
      host: ip,
      port: SSH_PORT,
      username: VM_USER,
      readyTimeout: Math.min(timeoutMs, 15_000),
    };

    const privateKey = getHostKey();
    if (privateKey) {
      config.privateKey = privateKey;
    } else {
      config.password = 'lume';
    }

    conn.connect(config);
  });
}

// ── Streaming command execution ────────────────────────────────────────────────

export interface StreamCallbacks {
  onStdout?: (line: string) => void;
  onStderr?: (line: string) => void;
  onJson?: (obj: object) => void;  // Called when a line is valid JSON
  onClose?: (code: number) => void;
}

export function sshStream(
  ip: string,
  command: string,
  callbacks: StreamCallbacks,
  timeoutMs = 600_000,
): Promise<number> {
  return new Promise((resolve, reject) => {
    const conn = new Client();

    const timer = setTimeout(() => {
      conn.destroy();
      reject(new Error(`SSH stream timed out after ${timeoutMs}ms`));
    }, timeoutMs);

    conn.on('ready', () => {
      conn.exec(command, (err, stream) => {
        if (err) {
          clearTimeout(timer);
          conn.end();
          return reject(err);
        }

        let stdoutBuf = '';
        let stderrBuf = '';

        function processLines(buf: string, cb?: (l: string) => void, jsonCb?: (o: object) => void): string {
          const lines = buf.split('\n');
          const remaining = lines.pop()!;
          for (const line of lines) {
            cb?.(line);
            if (jsonCb) {
              try { jsonCb(JSON.parse(line)); } catch { /* not JSON */ }
            }
          }
          return remaining;
        }

        stream.on('data', (d: Buffer) => {
          stdoutBuf += d.toString();
          stdoutBuf = processLines(stdoutBuf, callbacks.onStdout, callbacks.onJson);
        });

        stream.stderr.on('data', (d: Buffer) => {
          stderrBuf += d.toString();
          stderrBuf = processLines(stderrBuf, callbacks.onStderr);
        });

        stream.on('close', (code: number) => {
          // Flush remaining
          if (stdoutBuf) {
            callbacks.onStdout?.(stdoutBuf);
            if (callbacks.onJson) {
              try { callbacks.onJson(JSON.parse(stdoutBuf)); } catch { /* not JSON */ }
            }
          }
          if (stderrBuf) callbacks.onStderr?.(stderrBuf);

          clearTimeout(timer);
          conn.end();
          callbacks.onClose?.(code);
          resolve(code);
        });
      });
    });

    conn.on('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });

    const config: ConnectConfig = {
      host: ip,
      port: SSH_PORT,
      username: VM_USER,
      readyTimeout: 15_000,
    };

    const privateKey = getHostKey();
    if (privateKey) {
      config.privateKey = privateKey;
    } else {
      config.password = 'lume';
    }

    conn.connect(config);
  });
}

// ── SSH health check ──────────────────────────────────────────────────────────

export async function checkSSH(ip: string): Promise<boolean> {
  try {
    const result = await sshExec(ip, 'echo ok', 5000);
    return result.code === 0 && result.stdout.trim() === 'ok';
  } catch {
    return false;
  }
}

// ── Log tail via SSH ──────────────────────────────────────────────────────────

export type LogSource = 'app-console' | 'ui-tests' | 'peekaboo-mcp' | 'socat';

const LOG_PATHS: Record<LogSource, string> = {
  'app-console': '/tmp/ddg-app-console.log',
  'ui-tests': '/tmp/ddg-ui-tests.log',
  'peekaboo-mcp': '/tmp/peekaboo-mcp.log',
  'socat': '/tmp/socat.log',
};

export function tailLog(ip: string, source: LogSource, callbacks: StreamCallbacks): Promise<number> {
  const path = LOG_PATHS[source];
  // -F follows file rotation; -n 100 sends last 100 lines first
  return sshStream(ip, `tail -F -n 100 "${path}" 2>/dev/null || echo "Log not found: ${path}"`, callbacks, 86_400_000);
}
