/**
 * WebSocket routes for live log tailing.
 * Registers WS routes on the express-ws augmented app.
 */

import { Application } from 'express';
import { WebSocket } from 'ws';
import * as lume from '../lume.js';
import { tailLog, LogSource } from '../ssh.js';

// WS /api/vms/:id/logs/:source/tail
// Client connects; server SSHes into VM and streams log lines.
export function registerWSRoutes(wsApp: Application) {
  (wsApp as any).ws('/api/:id/logs/:source/tail', async (ws: WebSocket, req: any) => {
  const { id, source } = req.params as { id: string; source: string };

  const send = (type: string, data: string) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type, data, ts: Date.now() }));
    }
  };

  try {
    const vm = await lume.getVM(id);
    if (!vm.ipAddress) {
      send('error', 'VM has no IP address');
      ws.close();
      return;
    }

    send('connected', `Tailing ${source} from ${vm.ipAddress}`);

    let killed = false;

    ws.on('close', () => { killed = true; });

    await tailLog(vm.ipAddress, source as LogSource, {
      onStdout: (line) => { if (!killed) send('line', line); },
      onStderr: (line) => { if (!killed) send('error', line); },
      onClose: (code) => { if (!killed) { send('closed', `Exit: ${code}`); ws.close(); } },
    });

  } catch (err) {
    send('error', String(err));
    ws.close();
  }
  });
}
