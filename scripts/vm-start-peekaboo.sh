#!/bin/bash
# vm-start-peekaboo.sh
# Starts Peekaboo MCP server in VM and opens SSH tunnel on host.
# After this, Peekaboo is accessible at http://localhost:7888 on the host.
#
# Usage: VM_IP=x.x.x.x ./scripts/vm-start-peekaboo.sh

set -euo pipefail

VM_IP="${VM_IP:?Set VM_IP=x.x.x.x}"
VM_USER="${VM_USER:-lume}"
PEEKABOO_PORT=7888
TUNNEL_PID_FILE="/tmp/peekaboo-tunnel.pid"

# Kill any existing tunnel
if [[ -f "$TUNNEL_PID_FILE" ]]; then
  OLD_PID=$(cat "$TUNNEL_PID_FILE")
  kill "$OLD_PID" 2>/dev/null || true
  rm -f "$TUNNEL_PID_FILE"
fi

# Start (or restart) Peekaboo in VM
echo "Starting Peekaboo in VM at $VM_IP..."
ssh "$VM_USER@$VM_IP" \
  "pgrep -f 'peekaboo mcp serve' || nohup peekaboo mcp serve --port $PEEKABOO_PORT > ~/peekaboo-mcp.log 2>&1 &"

sleep 1

# Open SSH tunnel: localhost:7888 → VM:7888
echo "Opening SSH tunnel to Peekaboo ($VM_IP:$PEEKABOO_PORT)..."
ssh -fN -L "$PEEKABOO_PORT:localhost:$PEEKABOO_PORT" "$VM_USER@$VM_IP"
lsof -ti ":$PEEKABOO_PORT" | head -1 > "$TUNNEL_PID_FILE" 2>/dev/null || true

echo "✅ Peekaboo MCP available at http://localhost:$PEEKABOO_PORT"
echo "   Tunnel PID stored in $TUNNEL_PID_FILE"
echo ""
echo "To stop tunnel: kill \$(cat $TUNNEL_PID_FILE)"
echo "To check VM log: ssh $VM_USER@$VM_IP 'tail -f ~/peekaboo-mcp.log'"
