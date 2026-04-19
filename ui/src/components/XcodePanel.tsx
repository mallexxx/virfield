import { useState, useRef } from 'react';
import { useXcode, useTasks, apiPost } from '../hooks/useAPI.ts';

export function XcodePanel() {
  const { data, loading, error, refresh } = useXcode();
  const { data: taskList } = useTasks(2000);
  const activeTasks = (taskList ?? []).filter(t => t.status === 'running' && (t.type === 'copy-to-share' || t.type === 'xip-extract'));

  const [registerPath, setRegisterPath] = useState('');
  const [registerStatus, setRegisterStatus] = useState<string | null>(null);
  const [extracting, setExtracting] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  async function copyToShare(path: string) {
    try {
      await apiPost('/xcode/copy-to-share', { sourcePath: path });
      setRegisterStatus('Copy started (may take several minutes for large Xcode.app)');
    } catch (err) {
      setRegisterStatus(`Copy failed: ${String(err)}`);
    }
  }

  async function extractXIP(path: string) {
    setExtracting(path);
    setRegisterStatus(null);
    try {
      await apiPost('/xcode/extract-xip', { path });
      setRegisterStatus(`XIP extraction started — check VMShare for Xcode.app when complete.`);
      setTimeout(refresh, 5000);
    } catch (err) {
      setRegisterStatus(`Extract failed: ${String(err)}`);
    } finally {
      setExtracting(null);
    }
  }

  async function handleRegisterXIP(path: string) {
    setRegisterStatus(null);
    try {
      await apiPost('/xcode/register-xip', { path });
      setRegisterStatus('XIP registered.');
      setRegisterPath('');
      refresh();
    } catch (err) {
      setRegisterStatus(`Error: ${String(err)}`);
    }
  }

  function handleDragOver(e: React.DragEvent) {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(true);
  }

  function handleDragLeave(e: React.DragEvent) {
    e.preventDefault();
    setDragOver(false);
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);

    const file = e.dataTransfer.files[0];
    if (!file) return;

    // Try to get the real filesystem path (works in some Chromium-based local contexts)
    const path = (file as unknown as { path?: string }).path ?? '';
    const name = file.name;

    if (name.toLowerCase().endsWith('.xip')) {
      if (path && path.startsWith('/')) {
        handleRegisterXIP(path);
      } else {
        const guessed = `~/VMShare/${name}`;
        setRegisterPath(guessed);
        setRegisterStatus('Path could not be detected automatically — please confirm below.');
        setTimeout(() => inputRef.current?.select(), 50);
      }
    } else if (name.match(/^Xcode.*\.app$/) || name.endsWith('.app')) {
      if (path && path.startsWith('/')) {
        copyToShare(path);
      } else {
        const guessed = `/Applications/${name}`;
        setRegisterPath(guessed);
        setRegisterStatus('Drag detected a .app — you can copy it to VMShare using the path below.');
        setTimeout(() => inputRef.current?.select(), 50);
      }
    } else {
      setRegisterStatus('Only Xcode.app or .xip files can be dropped here.');
    }
  }

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-gray-200 font-semibold text-sm">Xcode Installations</h2>
        <button onClick={refresh} className="text-xs text-gray-500 hover:text-gray-300 px-2 py-1.5 hover:bg-gray-800 rounded">↻</button>
      </div>

      {/* Active background task indicators */}
      {activeTasks.length > 0 && (
        <div className="mb-3 space-y-1">
          {activeTasks.map(t => (
            <div key={t.id} className="flex items-center gap-2 px-3 py-2 bg-orange-500/10 border border-orange-500/30 rounded text-xs text-orange-300">
              <span className="animate-spin text-[10px]">⟳</span>
              <span className="flex-1">{t.label}</span>
              <span className="text-orange-500/60">running…</span>
            </div>
          ))}
        </div>
      )}
      {/* Recently finished tasks (done/failed in last 60s) */}
      {(taskList ?? []).filter(t => t.status !== 'running' && (t.type === 'copy-to-share' || t.type === 'xip-extract') && Date.now() - (t.finishedAt ?? 0) < 60_000).map(t => (
        <div key={t.id} className={`mb-2 flex items-center gap-2 px-3 py-1.5 rounded text-xs border ${t.status === 'done' ? 'bg-green-500/10 border-green-500/30 text-green-400' : 'bg-red-500/10 border-red-500/30 text-red-400'}`}>
          <span>{t.status === 'done' ? '✓' : '✗'}</span>
          <span>{t.label}</span>
          {t.error && <span className="text-red-400/70 ml-1 truncate">{t.error}</span>}
        </div>
      ))}
      {loading && <div className="text-gray-600 text-sm py-8 text-center">Scanning...</div>}
      {error && <div className="text-red-300 text-sm">{error}</div>}

      {data && (
        <div className="space-y-4">
          {/* Drag-and-drop + register zone */}
          <div
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            className={`border-2 border-dashed rounded-lg p-4 transition-colors ${
              dragOver
                ? 'border-orange-400 bg-orange-400/5'
                : 'border-gray-700 bg-gray-900/30'
            }`}
          >
            <div className="text-xs text-gray-500 mb-2 text-center">
              Drop a <span className="text-gray-400">Xcode.app</span> or <span className="text-gray-400">.xip</span> here — or enter a .xip path to register
            </div>
            <div className="flex gap-2">
              <input
                ref={inputRef}
                type="text"
                value={registerPath}
                onChange={e => setRegisterPath(e.target.value)}
                placeholder="/path/to/Xcode_16.xip"
                className="flex-1 text-xs bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-gray-300 placeholder-gray-600 focus:outline-none focus:border-orange-600"
                onKeyDown={e => { if (e.key === 'Enter' && registerPath) handleRegisterXIP(registerPath); }}
              />
              <button
                onClick={() => registerPath && handleRegisterXIP(registerPath)}
                disabled={!registerPath}
                className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
              >
                Register XIP
              </button>
            </div>
            {registerStatus && (
              <div className={`text-xs mt-2 ${registerStatus.startsWith('Error') || registerStatus.startsWith('Copy failed') ? 'text-red-400' : 'text-green-400'}`}>
                {registerStatus}
              </div>
            )}
          </div>

          {/* Xcode.app list */}
          <div className="border border-gray-800 rounded-lg overflow-hidden bg-gray-900/60">
            <div className="px-4 py-3 border-b border-gray-800 text-xs text-gray-500 uppercase tracking-wider">
              Xcode.app bundles ({data.apps.length})
            </div>
            {data.apps.length === 0 ? (
              <div className="p-4 text-xs text-gray-600">No Xcode.app found in /Applications or ~/VMShare.</div>
            ) : (
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-gray-600 border-b border-gray-800">
                    <th className="text-left px-4 py-2 font-normal">Name</th>
                    <th className="text-left px-4 py-2 font-normal">Version</th>
                    <th className="text-left px-4 py-2 font-normal">Locations</th>
                    <th className="px-4 py-2" />
                  </tr>
                </thead>
                <tbody>
                  {data.apps.map(app => (
                    <tr key={app.path} className="border-b border-gray-800/50 hover:bg-gray-800/30">
                      <td className="px-4 py-2 text-gray-300">{app.name}</td>
                      <td className="px-4 py-2 text-orange-300 font-mono">{app.version ?? '—'}</td>
                      <td className="px-4 py-2">
                        <div className="flex gap-1.5 flex-wrap">
                          {app.inApplications && (
                            <span className="px-1.5 py-0.5 rounded text-[10px] bg-blue-500/20 text-blue-300 border border-blue-500/30">/Applications</span>
                          )}
                          {app.inVMShare && (
                            <span className="px-1.5 py-0.5 rounded text-[10px] bg-green-500/20 text-green-300 border border-green-500/30">VMShare</span>
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-2 text-right">
                        {app.inApplications && !app.inVMShare && (
                          <button
                            onClick={() => copyToShare(app.path)}
                            className="text-[10px] px-2 py-0.5 bg-blue-500/20 text-blue-300 border border-blue-500/40 rounded hover:bg-blue-500/30"
                          >
                            Copy to VMShare
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {/* XIP archives */}
          {data.archives.length > 0 && (
            <div className="border border-gray-800 rounded-lg overflow-hidden bg-gray-900/60">
              <div className="px-4 py-3 border-b border-gray-800 text-xs text-gray-500 uppercase tracking-wider">
                XIP Archives ({data.archives.length})
              </div>
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-gray-600 border-b border-gray-800">
                    <th className="text-left px-4 py-2 font-normal">Name</th>
                    <th className="text-right px-4 py-2 font-normal">Size</th>
                    <th className="text-left px-4 py-2 font-normal">Source</th>
                    <th className="px-4 py-2" />
                  </tr>
                </thead>
                <tbody>
                  {data.archives.map(a => (
                    <tr key={a.path} className="border-b border-gray-800/50 hover:bg-gray-800/30">
                      <td className="px-4 py-2 text-gray-300">{a.name}</td>
                      <td className="px-4 py-2 text-right text-gray-500">{(a.size / 1e9).toFixed(1)} GB</td>
                      <td className="px-4 py-2 text-gray-600 text-[10px]">{a.source}</td>
                      <td className="px-4 py-2 text-right">
                        <button
                          onClick={() => extractXIP(a.path)}
                          disabled={extracting === a.path}
                          className="text-[10px] px-2 py-0.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-50"
                        >
                          {extracting === a.path ? 'Starting…' : 'Extract'}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
