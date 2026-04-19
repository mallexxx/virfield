import { useState } from 'react';
import { useStorage, apiPost, apiDelete } from '../hooks/useAPI.ts';

function formatBytes(bytes: number | null) {
  if (bytes === null) return '—';
  if (bytes >= 1e12) return `${(bytes / 1e12).toFixed(1)} TB`;
  if (bytes >= 1e9)  return `${(bytes / 1e9).toFixed(1)} GB`;
  return `${(bytes / 1e6).toFixed(0)} MB`;
}

function DiskBar({ used, total }: { used: number | null; total: number | null }) {
  if (!used || !total) return null;
  const pct = Math.round((used / total) * 100);
  const color = pct > 90 ? 'bg-red-500' : pct > 75 ? 'bg-yellow-500' : 'bg-green-500';
  return (
    <div className="flex items-center gap-2 mt-1.5">
      <div className="flex-1 h-1.5 bg-gray-800 rounded overflow-hidden">
        <div className={`h-full ${color} rounded`} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-[10px] text-gray-500">{pct}%</span>
    </div>
  );
}

export function StoragePanel() {
  const { data, loading, error, refresh } = useStorage();
  const [addName, setAddName] = useState('');
  const [addPath, setAddPath] = useState('');
  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const [removingName, setRemovingName] = useState<string | null>(null);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    if (!addName.trim() || !addPath.trim()) return;
    setAdding(true);
    setAddError(null);
    try {
      await apiPost('/storage', { name: addName.trim(), path: addPath.trim() });
      setAddName('');
      setAddPath('');
      refresh();
    } catch (err) {
      setAddError(String(err));
    } finally {
      setAdding(false);
    }
  }

  async function handleRemove(name: string) {
    setRemovingName(name);
    try {
      await apiDelete(`/storage/${encodeURIComponent(name)}`);
      refresh();
    } catch (err) {
      setAddError(String(err));
    } finally {
      setRemovingName(null);
    }
  }

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-gray-200 font-semibold text-sm">Storage & Resources</h2>
        <button onClick={refresh} className="text-xs text-gray-500 hover:text-gray-300 px-2 py-1.5 hover:bg-gray-800 rounded">↻</button>
      </div>

      {loading && <div className="text-gray-600 text-sm py-8 text-center">Loading...</div>}
      {error && <div className="text-red-300 text-xs mb-3">{error}</div>}
      {addError && <div className="text-red-300 text-xs mb-3">{addError}</div>}

      {data && (
        <div className="space-y-3">
          {data.map(loc => (
            <div key={loc.name} className="border border-gray-800 rounded-lg p-4 bg-gray-900/60">
              <div className="flex items-center justify-between mb-1">
                <div className="min-w-0 flex-1">
                  <span className="text-gray-200 text-sm font-medium">{loc.name}</span>
                  <span className="ml-2 text-xs text-gray-500 truncate">{loc.resolvedPath}</span>
                </div>
                <div className="flex items-center gap-3 flex-shrink-0 ml-3">
                  <div className="text-right text-xs">
                    <span className="text-gray-400">{formatBytes(loc.freeBytes)} free</span>
                    <span className="text-gray-600 mx-1">/</span>
                    <span className="text-gray-500">{formatBytes(loc.totalBytes)}</span>
                  </div>
                  <button
                    disabled={removingName === loc.name}
                    onClick={() => handleRemove(loc.name)}
                    className="text-gray-600 hover:text-red-400 text-xs px-1.5 py-0.5 hover:bg-red-500/10 rounded transition-colors"
                    title="Remove storage location"
                  >✕</button>
                </div>
              </div>
              <DiskBar used={loc.usedBytes} total={loc.totalBytes} />
            </div>
          ))}
        </div>
      )}

      {/* Add location form */}
      <form onSubmit={handleAdd} className="mt-5 border border-gray-800 rounded-lg p-4 bg-gray-900/40">
        <div className="text-xs text-gray-500 uppercase tracking-wider mb-3">Add Storage Location</div>
        <div className="flex gap-2">
          <input
            value={addName}
            onChange={e => setAddName(e.target.value)}
            placeholder="Name (e.g. external)"
            className="flex-1 bg-gray-800 border border-gray-700 rounded px-2.5 py-1.5 text-xs text-gray-200 placeholder-gray-600 outline-none focus:border-gray-500"
          />
          <input
            value={addPath}
            onChange={e => setAddPath(e.target.value)}
            placeholder="Path (e.g. /Volumes/SSD)"
            className="flex-[2] bg-gray-800 border border-gray-700 rounded px-2.5 py-1.5 text-xs text-gray-200 placeholder-gray-600 outline-none focus:border-gray-500"
          />
          <button
            type="submit"
            disabled={adding || !addName.trim() || !addPath.trim()}
            className="px-3 py-1.5 bg-blue-600/20 text-blue-300 border border-blue-600/40 rounded text-xs hover:bg-blue-600/30 disabled:opacity-40"
          >
            {adding ? 'Adding…' : 'Add'}
          </button>
        </div>
      </form>
    </div>
  );
}
