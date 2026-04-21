import { useState, useEffect } from 'react';

interface Client {
  id: string;
  label: string;
  configFile: string;
  docs: string;
}

const CLIENTS: Client[] = [
  {
    id: 'claude-code',
    label: 'Claude Code',
    configFile: '~/.claude/claude_desktop_config.json  or  project .claude/settings.json',
    docs: 'https://docs.anthropic.com/en/docs/claude-code/mcp',
  },
  {
    id: 'cursor',
    label: 'Cursor',
    configFile: '~/.cursor/mcp.json',
    docs: 'https://docs.cursor.com/context/model-context-protocol',
  },
  {
    id: 'codex',
    label: 'OpenAI Codex CLI',
    configFile: '~/.codex/config.json',
    docs: 'https://github.com/openai/codex',
  },
  {
    id: 'generic',
    label: 'Other MCP client',
    configFile: 'client-specific',
    docs: '',
  },
];

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => { navigator.clipboard.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 2000); }}
      className="text-[10px] px-2 py-0.5 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors"
    >
      {copied ? '✓ copied' : 'copy'}
    </button>
  );
}

function CodeBlock({ children, label }: { children: string; label?: string }) {
  return (
    <div className="relative group">
      {label && <p className="text-[10px] text-gray-500 mb-1">{label}</p>}
      <pre className="bg-gray-950 border border-gray-800 rounded p-3 text-[11px] text-gray-300 font-mono overflow-x-auto whitespace-pre">
        {children}
      </pre>
      <div className="absolute top-2 right-2">
        <CopyButton text={children} />
      </div>
    </div>
  );
}

export function MCPSetupPanel() {
  const [activeClient, setActiveClient] = useState('claude-code');
  const [mcpPath, setMcpPath] = useState<string | null>(null);

  useEffect(() => {
    fetch('/api/host/mcp-server-path')
      .then(r => r.json())
      .then((d: { path: string }) => setMcpPath(d.path))
      .catch(() => setMcpPath('/path/to/virfield/server/mcp-server.ts'));
  }, []);

  const serverPath = mcpPath ?? '/path/to/virfield/server/mcp-server.ts';

  const claudeCodeConfig = JSON.stringify({
    mcpServers: {
      virfield: {
        command: 'npx',
        args: ['tsx', serverPath],
      },
    },
  }, null, 2);

  const cursorConfig = JSON.stringify({
    virfield: {
      command: 'npx',
      args: ['tsx', serverPath],
    },
  }, null, 2);

  const codexConfig = JSON.stringify({
    mcpServers: {
      virfield: {
        command: 'npx',
        args: ['tsx', serverPath],
      },
    },
  }, null, 2);

  const genericConfig = JSON.stringify({
    virfield: {
      command: 'npx',
      args: ['tsx', serverPath],
      description: 'virfield MCP — manage macOS lume VMs, Peekaboo, AX snapshots, run UI tests',
    },
  }, null, 2);

  return (
    <div className="max-w-2xl space-y-6">
      <div>
        <h2 className="text-sm font-semibold text-gray-200 mb-1">MCP Setup</h2>
        <p className="text-xs text-gray-500">
          Connect virfield to your AI coding assistant as an MCP server.
          Pick your client below for the exact config snippet to paste.
        </p>
      </div>

      {/* Client tabs */}
      <div className="flex gap-1 flex-wrap">
        {CLIENTS.map(c => (
          <button
            key={c.id}
            onClick={() => setActiveClient(c.id)}
            className={`px-3 py-1.5 text-xs rounded border transition-colors ${
              activeClient === c.id
                ? 'bg-orange-500/20 text-orange-300 border-orange-500/40'
                : 'text-gray-400 border-gray-700 hover:text-gray-200 hover:border-gray-600'
            }`}
          >
            {c.label}
          </button>
        ))}
      </div>

      {/* Claude Code */}
      {activeClient === 'claude-code' && (
        <div className="space-y-4">
          <p className="text-xs text-gray-400">
            Add to <code className="font-mono text-gray-300">~/.claude/claude_desktop_config.json</code> (global)
            or to a project's <code className="font-mono text-gray-300">.claude/settings.json</code> (project-scoped).
          </p>

          <CodeBlock label="~/.claude/claude_desktop_config.json">
            {claudeCodeConfig}
          </CodeBlock>

          <div className="border border-gray-800 rounded p-3 bg-gray-900/40 space-y-2">
            <p className="text-[11px] font-medium text-gray-300">Quick setup via CLI</p>
            <CodeBlock>
              {`# Global install (all projects)\nclaudie mcp add virfield npx tsx ${serverPath}\n\n# Or copy mcp.json.example and edit path:\ncp mcp.json.example mcp.json`}
            </CodeBlock>
          </div>

          <div className="border border-gray-800 rounded p-3 bg-gray-900/40">
            <p className="text-[11px] font-medium text-gray-300 mb-2">After editing the config</p>
            <ol className="text-xs text-gray-500 space-y-1 list-decimal list-inside">
              <li>Restart Claude Code (or reload the MCP server list)</li>
              <li>Confirm <code className="font-mono text-gray-400">virfield</code> appears in the MCP tools list</li>
              <li>Try: <code className="font-mono text-gray-400">vm_list</code> to verify the connection</li>
            </ol>
          </div>
        </div>
      )}

      {/* Cursor */}
      {activeClient === 'cursor' && (
        <div className="space-y-4">
          <p className="text-xs text-gray-400">
            Add to <code className="font-mono text-gray-300">~/.cursor/mcp.json</code>.
            Create the file if it doesn't exist.
          </p>

          <CodeBlock label="~/.cursor/mcp.json">
            {cursorConfig}
          </CodeBlock>

          <div className="border border-gray-800 rounded p-3 bg-gray-900/40">
            <p className="text-[11px] font-medium text-gray-300 mb-2">After editing the config</p>
            <ol className="text-xs text-gray-500 space-y-1 list-decimal list-inside">
              <li>Open Cursor Settings → Features → MCP</li>
              <li>Click <strong className="text-gray-400">Refresh</strong> or restart Cursor</li>
              <li>Confirm <code className="font-mono text-gray-400">virfield</code> shows as Connected</li>
            </ol>
          </div>
        </div>
      )}

      {/* Codex */}
      {activeClient === 'codex' && (
        <div className="space-y-4">
          <p className="text-xs text-gray-400">
            Add to <code className="font-mono text-gray-300">~/.codex/config.json</code>.
          </p>

          <CodeBlock label="~/.codex/config.json">
            {codexConfig}
          </CodeBlock>

          <div className="border border-gray-800 rounded p-3 bg-gray-900/40">
            <p className="text-[11px] font-medium text-gray-300 mb-2">After editing the config</p>
            <ol className="text-xs text-gray-500 space-y-1 list-decimal list-inside">
              <li>Restart the Codex CLI session</li>
              <li>MCP servers are loaded at startup</li>
            </ol>
          </div>
        </div>
      )}

      {/* Generic */}
      {activeClient === 'generic' && (
        <div className="space-y-4">
          <p className="text-xs text-gray-400">
            Most MCP clients accept a JSON config with a <code className="font-mono text-gray-300">command</code> +{' '}
            <code className="font-mono text-gray-300">args</code> entry. Refer to your client's docs for the exact location.
          </p>

          <CodeBlock label="Generic MCP config entry">
            {genericConfig}
          </CodeBlock>
        </div>
      )}

      {/* Available tools */}
      <div>
        <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-3 pb-1 border-b border-gray-800">
          Available MCP Tools
        </h3>
        <div className="grid grid-cols-2 gap-x-6 gap-y-1">
          {[
            ['vm_list', 'List all VMs with status'],
            ['vm_start / vm_stop', 'Start or stop a VM'],
            ['vm_status', 'Full VM status + checklist'],
            ['vm_prepare_session', 'Clone golden → boot → SSH ready'],
            ['vm_build_golden', 'Run full 4-phase golden build'],
            ['vm_run_stage', 'Run a single build phase'],
            ['vm_clone_golden', 'APFS clone from golden image'],
            ['vm_ssh_exec', 'Run a command in the VM via SSH'],
            ['peekaboo_see', 'Screenshot + describe VM screen'],
            ['peekaboo_click / type / scroll', 'UI interactions in the VM'],
            ['peekaboo_hotkey', 'Send keyboard shortcuts to VM'],
            ['ax_snapshot', 'Capture accessibility tree'],
            ['ax_diff / ax_diff_last', 'Diff AX snapshots'],
            ['run_tests', 'Run xcodebuild UI tests in VM'],
            ['get_test_results', 'Parse xcresult pass/fail/errors'],
            ['get_log_stream', 'Tail unified log from VM'],
            ['get_crash_reports', 'Retrieve VM crash logs'],
          ].map(([name, desc]) => (
            <div key={name} className="flex gap-2 py-0.5">
              <code className="text-[10px] text-orange-300 font-mono whitespace-nowrap flex-shrink-0">{name}</code>
              <span className="text-[10px] text-gray-500 truncate">{desc}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Prereqs */}
      <div className="border border-gray-800 rounded p-3 bg-gray-900/40">
        <p className="text-[11px] font-medium text-gray-300 mb-2">Prerequisites</p>
        <ul className="text-xs text-gray-500 space-y-1">
          <li>• <code className="font-mono text-gray-400">npx</code> available (Node.js 20+)</li>
          <li>• virfield backend running: <code className="font-mono text-gray-400">npm run dev</code></li>
          <li>• lume serve running (start from Settings if needed)</li>
          <li>• GitHub credentials + GHCR source configured in Settings (for push/pull)</li>
        </ul>
      </div>
    </div>
  );
}
