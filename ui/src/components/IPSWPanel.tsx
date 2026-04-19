import { useState, useRef } from 'react';
import { useIPSW, apiPost } from '../hooks/useAPI.ts';

export function IPSWPanel() {
  const { data, loading, error, refresh } = useIPSW();

  const [registerPath, setRegisterPath] = useState('');
  const [registerStatus, setRegisterStatus] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  async function handleRegister(path: string) {
    setRegisterStatus(null);
    try {
      await apiPost('/ipsw/register', { path });
      setRegisterStatus('Registered.');
      setRegisterPath('');
      refresh();
    } catch (err) {
      setRegisterStatus(`Error: ${String(err)}`);
    }
  }

  async function handleDownloadLatest() {
    setDownloading(true);
    setRegisterStatus(null);
    try {
      const result = await apiPost('/ipsw/download') as { message: string; name: string };
      setRegisterStatus(`${result.message} — ${result.name}`);
    } catch (err) {
      setRegisterStatus(`Download failed: ${String(err)}`);
    } finally {
      setDownloading(false);
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
    if (path && path.startsWith('/') && path.toLowerCase().endsWith('.ipsw')) {
      handleRegister(path);
    } else if (file.name.toLowerCase().endsWith('.ipsw')) {
      // Path not available — pre-fill input so user can adjust
      const guessed = `~/Downloads/${file.name}`;
      setRegisterPath(guessed);
      setRegisterStatus('Path could not be detected automatically — please confirm below.');
      setTimeout(() => inputRef.current?.select(), 50);
    } else {
      setRegisterStatus('Only .ipsw files can be dropped here.');
    }
  }

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-gray-200 font-semibold text-sm">macOS IPSW Files</h2>
        <button onClick={refresh} className="text-xs text-gray-500 hover:text-gray-300 px-2 py-1.5 hover:bg-gray-800 rounded">↻</button>
      </div>

      {loading && <div className="text-gray-600 text-sm py-8 text-center">Loading...</div>}
      {error && <div className="text-red-300 text-sm">{error}</div>}

      {data && (
        <div className="space-y-4">
          {/* Latest from Apple CDN */}
          <div className="border border-gray-800 rounded-lg p-4 bg-gray-900/60">
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-xs text-gray-500 uppercase tracking-wider mb-1">Latest from Apple CDN</div>
                <div className="text-xs text-cyan-400 break-all">{data.latestUrl || 'Unavailable'}</div>
              </div>
              {data.latestUrl && (
                <button
                  onClick={handleDownloadLatest}
                  disabled={downloading}
                  className="shrink-0 text-xs px-3 py-1.5 bg-cyan-500/20 text-cyan-300 border border-cyan-500/40 rounded hover:bg-cyan-500/30 disabled:opacity-50"
                >
                  {downloading ? 'Starting…' : '↓ Download'}
                </button>
              )}
            </div>
          </div>

          {/* Drag-and-drop + register zone */}
          <div
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            className={`border-2 border-dashed rounded-lg p-4 transition-colors ${
              dragOver
                ? 'border-cyan-400 bg-cyan-400/5'
                : 'border-gray-700 bg-gray-900/30'
            }`}
          >
            <div className="text-xs text-gray-500 mb-2 text-center">
              Drop a <span className="text-gray-400">.ipsw</span> file here — or enter a path to register
            </div>
            <div className="flex gap-2">
              <input
                ref={inputRef}
                type="text"
                value={registerPath}
                onChange={e => setRegisterPath(e.target.value)}
                placeholder="/path/to/macOS.ipsw"
                className="flex-1 text-xs bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-gray-300 placeholder-gray-600 focus:outline-none focus:border-cyan-600"
                onKeyDown={e => { if (e.key === 'Enter' && registerPath) handleRegister(registerPath); }}
              />
              <button
                onClick={() => registerPath && handleRegister(registerPath)}
                disabled={!registerPath}
                className="text-xs px-3 py-1.5 bg-cyan-500/20 text-cyan-300 border border-cyan-500/40 rounded hover:bg-cyan-500/30 disabled:opacity-40"
              >
                Register
              </button>
            </div>
            {registerStatus && (
              <div className={`text-xs mt-2 ${registerStatus.startsWith('Error') ? 'text-red-400' : 'text-green-400'}`}>
                {registerStatus}
              </div>
            )}
          </div>

          {/* Local IPSW files */}
          <div className="border border-gray-800 rounded-lg overflow-hidden bg-gray-900/60">
            <div className="px-4 py-3 border-b border-gray-800 text-xs text-gray-500 uppercase tracking-wider">
              Local IPSW files ({data.localFiles.length})
            </div>
            {data.localFiles.length === 0 ? (
              <div className="p-4 text-xs text-gray-600">No local IPSW files found.</div>
            ) : (
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-gray-600 border-b border-gray-800">
                    <th className="text-left px-4 py-2 font-normal">Name</th>
                    <th className="text-right px-4 py-2 font-normal">Size</th>
                    <th className="text-left px-4 py-2 font-normal">Path</th>
                  </tr>
                </thead>
                <tbody>
                  {data.localFiles.map(f => (
                    <tr key={f.path} className="border-b border-gray-800/50 hover:bg-gray-800/30">
                      <td className="px-4 py-2 text-gray-300">{f.name}</td>
                      <td className="px-4 py-2 text-right text-gray-500">{(f.size / 1e9).toFixed(1)} GB</td>
                      <td className="px-4 py-2 text-gray-600 truncate max-w-[200px]" title={f.path}>{f.path}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
