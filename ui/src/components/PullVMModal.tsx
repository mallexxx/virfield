import { useState, useEffect, useCallback } from 'react';
import { useGet, apiPost } from '../hooks/useAPI.ts';

interface GhcrSource {
  id: string;
  name: string;
  registry: string;
  organization: string;
  is_default: number;
}

interface GhcrPackage {
  name: string;
  visibility: string;
  updatedAt: string;
}

interface GhcrTag {
  tag: string;
  updatedAt: string;
}

interface GhcrTask {
  id: string;
  status: string;
  error?: string;
  log: string;
}

interface Props {
  onClose: () => void;
  onPulled: () => void;
}

function timeAgo(iso: string) {
  const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (secs < 60)  return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

export function PullVMModal({ onClose, onPulled }: Props) {
  const { data: sources, refresh: refreshSources } = useGet<GhcrSource[]>('/ghcr/sources');

  const [sourceId, setSourceId] = useState('');
  const [packages, setPackages] = useState<GhcrPackage[] | null>(null);
  const [pkgLoading, setPkgLoading] = useState(false);
  const [pkgError, setPkgError] = useState<string | null>(null);
  const [pkgRefreshKey, setPkgRefreshKey] = useState(0);

  const [tags, setTags] = useState<GhcrTag[] | null>(null);
  const [tagLoading, setTagLoading] = useState(false);
  const [tagError, setTagError] = useState<string | null>(null);

  const [imageName, setImageName] = useState('');
  const [tag, setTag] = useState('');
  const [vmName, setVmName] = useState('');
  const [force, setForce] = useState(false);

  const [collisionCheck, setCollisionCheck] = useState<{ exists: boolean } | null>(null);
  const [checking, setChecking] = useState(false);
  const [pulling, setPulling] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [task, setTask] = useState<GhcrTask | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Inline add-source form state
  const [showAddSource, setShowAddSource] = useState(false);
  const [newSrc, setNewSrc] = useState({ name: '', registry: 'ghcr.io', organization: '', isDefault: true });
  const [addingSrc, setAddingSrc] = useState(false);
  const [addSrcError, setAddSrcError] = useState<string | null>(null);

  // Auto-select default source
  useEffect(() => {
    if (!sourceId && sources?.length) {
      const def = sources.find(s => s.is_default) ?? sources[0];
      setSourceId(def.id);
    }
  }, [sources, sourceId]);

  const addSource = useCallback(async () => {
    if (!newSrc.name || !newSrc.organization) return;
    setAddingSrc(true);
    setAddSrcError(null);
    try {
      const result = await apiPost('/ghcr/sources', newSrc) as { id: string };
      await refreshSources();
      setSourceId(result.id);
      setShowAddSource(false);
      setNewSrc({ name: '', registry: 'ghcr.io', organization: '', isDefault: true });
    } catch (e) {
      setAddSrcError(String(e));
    } finally {
      setAddingSrc(false);
    }
  }, [newSrc, refreshSources]);

  // Load packages when source changes or refresh is triggered
  useEffect(() => {
    if (!sourceId) return;
    setPkgLoading(true);
    setPkgError(null);
    setPackages(null);
    setImageName('');
    setTags(null);
    setTag('');
    fetch(`/api/ghcr/packages?sourceId=${sourceId}`)
      .then(r => r.json())
      .then((data: GhcrPackage[] | { error: string }) => {
        if ('error' in data) { setPkgError(data.error); setPackages([]); }
        else setPackages(data);
      })
      .catch(e => { setPkgError(String(e)); setPackages([]); })
      .finally(() => setPkgLoading(false));
  // pkgRefreshKey forces a reload when the refresh button is clicked
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceId, pkgRefreshKey]);

  // Load tags when image name is selected
  useEffect(() => {
    if (!imageName || !sourceId) { setTags(null); setTagError(null); setTag(''); return; }
    setTagLoading(true);
    setTagError(null);
    setTags(null);
    setTag('');
    fetch(`/api/ghcr/packages/${encodeURIComponent(imageName)}/tags?sourceId=${sourceId}`)
      .then(r => r.json())
      .then((data: GhcrTag[] | { error: string }) => {
        if ('error' in data) { setTagError(data.error); setTags([]); }
        else { setTags(data); if (data.length) setTag(data[0].tag); }
      })
      .catch(e => { setTagError(String(e)); setTags([]); })
      .finally(() => setTagLoading(false));
  }, [imageName, sourceId]);

  // Auto-generate VM name from image+tag
  useEffect(() => {
    if (imageName && tag) {
      setVmName(`${imageName}-${tag}`.replace(/[^a-zA-Z0-9._-]/g, '-').toLowerCase().slice(0, 60));
    }
  }, [imageName, tag]);

  // Debounced collision check on vmName
  useEffect(() => {
    if (!vmName) { setCollisionCheck(null); return; }
    setChecking(true);
    const t = setTimeout(async () => {
      try {
        const resp = await apiPost('/ghcr/check-collision', { vmName }) as { exists: boolean };
        setCollisionCheck({ exists: resp.exists });
      } catch { setCollisionCheck(null); }
      finally { setChecking(false); }
    }, 500);
    return () => clearTimeout(t);
  }, [vmName]);

  // Poll task while running
  useEffect(() => {
    if (!taskId) return;
    const poll = async () => {
      const resp = await fetch(`/api/ghcr/task/${taskId}`);
      if (!resp.ok) return;
      const t = await resp.json() as GhcrTask;
      setTask(t);
      if (t.status === 'done') { onPulled(); clearInterval(timer); }
      if (t.status === 'failed' || t.status === 'cancelled') { setPulling(false); clearInterval(timer); }
    };
    poll();
    const timer = setInterval(poll, 1500);
    return () => clearInterval(timer);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  async function doPull() {
    if (!imageName || !tag || !vmName || !sourceId) return;
    setError(null);
    setPulling(true);
    try {
      const result = await apiPost('/ghcr/pull', { imageName, tag, vmName, sourceId, force }) as { taskId: string };
      setTaskId(result.taskId);
    } catch (err) {
      setError(String(err));
      setPulling(false);
    }
  }

  async function cancel() {
    if (taskId) await apiPost(`/ghcr/task/${taskId}/cancel`);
    setPulling(false);
  }

  const selectedSource = (sources ?? []).find(s => s.id === sourceId);
  const canPull = Boolean(imageName && tag && vmName && sourceId)
    && (!collisionCheck?.exists || force)
    && !pulling;

  const taskDone = task?.status === 'done';
  const taskFailed = task?.status === 'failed' || task?.status === 'cancelled';

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4">
      <div className="bg-gray-900 border border-gray-700 rounded-xl shadow-2xl w-full max-w-lg">
        <div className="flex items-center justify-between px-5 py-4 border-b border-gray-800">
          <h2 className="text-sm font-semibold text-gray-100">Pull VM from GHCR</h2>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300 text-lg leading-none">×</button>
        </div>

        <div className="p-5 space-y-4">

          {/* Source */}
          <div>
            <div className="flex items-center justify-between mb-1">
              <label className="text-xs text-gray-400">Registry Source</label>
              <button
                onClick={() => setShowAddSource(s => !s)}
                className="text-[10px] text-gray-500 hover:text-orange-300"
              >
                {showAddSource ? '✕ cancel' : '+ add source'}
              </button>
            </div>

            {showAddSource ? (
              <div className="border border-gray-700 rounded p-3 bg-gray-900/60 space-y-2">
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="block text-[10px] text-gray-500 mb-1">Name</label>
                    <input
                      value={newSrc.name}
                      onChange={e => setNewSrc(s => ({ ...s, name: e.target.value }))}
                      placeholder="e.g. My Org"
                      className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
                    />
                  </div>
                  <div>
                    <label className="block text-[10px] text-gray-500 mb-1">Registry</label>
                    <input
                      value={newSrc.registry}
                      onChange={e => setNewSrc(s => ({ ...s, registry: e.target.value }))}
                      placeholder="ghcr.io"
                      className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
                    />
                  </div>
                  <div className="col-span-2">
                    <label className="block text-[10px] text-gray-500 mb-1">Organization / Username</label>
                    <input
                      value={newSrc.organization}
                      onChange={e => setNewSrc(s => ({ ...s, organization: e.target.value }))}
                      placeholder="e.g. your-username or your-org"
                      className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60"
                    />
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <label className="flex items-center gap-1.5 text-xs text-gray-400 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={newSrc.isDefault}
                      onChange={e => setNewSrc(s => ({ ...s, isDefault: e.target.checked }))}
                      className="accent-orange-500"
                    />
                    Set as default
                  </label>
                  <button
                    onClick={addSource}
                    disabled={addingSrc || !newSrc.name || !newSrc.organization}
                    className="text-xs px-3 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40"
                  >
                    {addingSrc ? 'Adding…' : '+ Add'}
                  </button>
                </div>
                {addSrcError && <p className="text-[10px] text-red-400">{addSrcError}</p>}
              </div>
            ) : (sources ?? []).length === 0 ? (
              <p className="text-xs text-gray-500 py-1">
                No sources yet —{' '}
                <button onClick={() => setShowAddSource(true)} className="text-orange-400 hover:underline">
                  add one above
                </button>
              </p>
            ) : (
              <select
                value={sourceId}
                onChange={e => { setSourceId(e.target.value); setImageName(''); setTag(''); }}
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-orange-500/60"
              >
                {(sources ?? []).map(s => (
                  <option key={s.id} value={s.id}>
                    {s.name} — {s.registry}/{s.organization}{s.is_default ? ' ★' : ''}
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* Image chooser */}
          <div>
            <div className="flex items-center justify-between mb-1">
              <label className="text-xs text-gray-400">
                Image
                {pkgLoading && <span className="ml-2 text-gray-600 animate-pulse">loading…</span>}
              </label>
              <button
                onClick={() => setPkgRefreshKey(k => k + 1)}
                disabled={pkgLoading}
                title="Refresh image list"
                className="text-[10px] text-gray-600 hover:text-gray-300 disabled:opacity-40 px-1"
              >
                ↺ refresh
              </button>
            </div>
            {pkgError ? (
              <div className="space-y-1">
                <input
                  value={imageName}
                  onChange={e => setImageName(e.target.value)}
                  placeholder="e.g. uitest-golden"
                  className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono"
                />
                <p className="text-[10px] text-yellow-600">Manual entry — API error: {pkgError}</p>
              </div>
            ) : packages && packages.length > 0 ? (
              <select
                value={imageName}
                onChange={e => setImageName(e.target.value)}
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-orange-500/60 font-mono"
              >
                <option value="">— select image —</option>
                {packages.map(p => (
                  <option key={p.name} value={p.name}>
                    {p.name}{p.visibility === 'private' ? ' [private]' : ''}{p.updatedAt ? `  (${timeAgo(p.updatedAt)})` : ''}
                  </option>
                ))}
              </select>
            ) : (
              <input
                value={imageName}
                onChange={e => setImageName(e.target.value)}
                placeholder={pkgLoading ? 'Loading…' : 'e.g. uitest-golden'}
                disabled={pkgLoading}
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono disabled:opacity-50"
              />
            )}
          </div>

          {/* Tag chooser — always shown; disabled until image is selected */}
          <div>
            <label className="block text-xs text-gray-400 mb-1">
              Tag
              {tagLoading && <span className="ml-2 text-gray-600 animate-pulse">loading…</span>}
            </label>
            {!imageName ? (
              <input
                value=""
                disabled
                placeholder="Select an image first"
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-500 placeholder-gray-600 font-mono opacity-50 cursor-not-allowed"
              />
            ) : tags && tags.length > 0 ? (
              <select
                value={tag}
                onChange={e => setTag(e.target.value)}
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-orange-500/60 font-mono"
              >
                {tags.map(t => (
                  <option key={t.tag} value={t.tag}>
                    {t.tag}  ({timeAgo(t.updatedAt)})
                  </option>
                ))}
              </select>
            ) : (
              <div className="space-y-1">
                <input
                  value={tag}
                  onChange={e => setTag(e.target.value)}
                  placeholder={tagLoading ? 'Loading tags…' : 'e.g. 26.4.1-latest'}
                  disabled={tagLoading}
                  className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono disabled:opacity-50"
                />
                {tagError && !tagLoading && (
                  <p className="text-[10px] text-yellow-600">Manual entry — API error: {tagError}</p>
                )}
              </div>
            )}
          </div>

          {/* Preview URL */}
          {selectedSource && imageName && tag && (
            <p className="text-[10px] text-gray-600 font-mono">
              ↓ {selectedSource.registry}/{selectedSource.organization}/{imageName}:{tag}
            </p>
          )}

          {/* Local VM name */}
          <div>
            <label className="block text-xs text-gray-400 mb-1">Local VM Name</label>
            <div className="relative">
              <input
                value={vmName}
                onChange={e => setVmName(e.target.value)}
                placeholder="e.g. uitest-26.4.1-golden"
                className={`w-full bg-gray-800 border rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none font-mono ${
                  collisionCheck?.exists && !force
                    ? 'border-red-600/60 focus:border-red-500'
                    : collisionCheck?.exists === false
                    ? 'border-green-600/40 focus:border-green-500'
                    : 'border-gray-700 focus:border-orange-500/60'
                }`}
              />
              {checking && (
                <span className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-gray-500 animate-pulse">
                  checking…
                </span>
              )}
            </div>
            {collisionCheck?.exists && (
              <div className="mt-2 p-2 bg-red-900/20 border border-red-800/40 rounded text-xs text-red-300">
                ⚠ A VM named <code className="font-mono">{vmName}</code> already exists.
                Choose a different name, or enable force overwrite below.
              </div>
            )}
            {collisionCheck?.exists === false && (
              <p className="mt-1 text-[10px] text-green-500">✓ Name available</p>
            )}
          </div>

          {/* Force overwrite */}
          {collisionCheck?.exists && (
            <label className="flex items-start gap-2 text-xs text-red-400 cursor-pointer">
              <input
                type="checkbox"
                checked={force}
                onChange={e => setForce(e.target.checked)}
                className="mt-0.5 accent-red-500"
              />
              <span>
                <strong>Force overwrite</strong> — replaces the existing VM.
                Any unsaved state will be lost.
              </span>
            </label>
          )}

          {/* Error */}
          {error && (
            <div className="p-2 bg-red-900/20 border border-red-800/40 rounded text-xs text-red-300">
              {error}
            </div>
          )}

          {/* Live log */}
          {taskId && (
            <div>
              <span className={`block text-[10px] font-medium mb-1 ${
                taskDone ? 'text-green-400' : taskFailed ? 'text-red-400' : 'text-orange-400 animate-pulse'
              }`}>
                {taskDone ? '✓ Pull complete' : taskFailed ? `✗ ${task?.error ?? 'Failed'}` : '⟳ Pulling…'}
              </span>
              <pre className="bg-gray-950 border border-gray-800 rounded p-2 text-[10px] text-gray-400 font-mono overflow-auto max-h-40 whitespace-pre-wrap">
                {task?.log || '(waiting for output…)'}
              </pre>
            </div>
          )}
        </div>

        <div className="px-5 py-4 border-t border-gray-800 flex items-center justify-end gap-3">
          {pulling && !taskDone ? (
            <button onClick={cancel}
              className="text-xs px-3 py-1.5 bg-red-500/20 text-red-300 border border-red-500/40 rounded hover:bg-red-500/30">
              Cancel
            </button>
          ) : (
            <button onClick={onClose}
              className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200 hover:bg-gray-800 rounded">
              {taskDone ? 'Close' : 'Cancel'}
            </button>
          )}
          {!taskDone && (
            <button onClick={doPull} disabled={!canPull}
              className="text-xs px-4 py-1.5 bg-blue-500/20 text-blue-300 border border-blue-500/40 rounded hover:bg-blue-500/30 disabled:opacity-40">
              {pulling ? 'Pulling…' : '⬇ Pull VM'}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
