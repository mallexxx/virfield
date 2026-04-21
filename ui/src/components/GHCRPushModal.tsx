import { useState, useEffect, useRef } from 'react';
import { useGet, apiPost } from '../hooks/useAPI.ts';

interface GhcrSource {
  id: string;
  name: string;
  registry: string;
  organization: string;
  is_default: number;
}

interface GhcrTask {
  id: string;
  status: string;
  error?: string;
  log: string;
}

interface Props {
  vmName: string;
  onClose: () => void;
  /** Called as soon as a push task is created so the parent can track it inline. */
  onTaskStarted?: (taskId: string, label: string) => void;
}

function today() {
  return new Date().toISOString().slice(0, 10).replace(/-/g, '');
}

export function GHCRPushModal({ vmName, onClose, onTaskStarted }: Props) {
  const { data: sources } = useGet<GhcrSource[]>('/ghcr/sources');
  const { data: settings } = useGet<{ github_token_set: boolean }>('/settings');

  const [sourceId, setSourceId] = useState('');
  const [imageName, setImageName] = useState('');
  const [tag, setTag] = useState('');
  const [additionalTags, setAdditionalTags] = useState('');
  const [pushing, setPushing] = useState(false);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [task, setTask] = useState<GhcrTask | null>(null);
  const [error, setError] = useState<string | null>(null);
  const logRef = useRef<HTMLPreElement>(null);

  // Auto-select default source
  useEffect(() => {
    if (!sourceId && sources?.length) {
      const def = sources.find(s => s.is_default) ?? sources[0];
      setSourceId(def.id);
    }
  }, [sources, sourceId]);

  // Auto-fill image name + tag from VM name
  useEffect(() => {
    if (!imageName) setImageName(vmName);
    if (!tag) {
      const match = vmName.match(/(\d+[\d.]+)/);
      const ver = match ? match[1] : '';
      setTag(ver ? `${ver}-${today()}` : today());
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vmName]);

  // Poll task while running
  useEffect(() => {
    if (!taskId) return;
    const poll = async () => {
      try {
        const resp = await fetch(`/api/ghcr/task/${taskId}`);
        if (resp.ok) {
          const t = await resp.json() as GhcrTask;
          setTask(t);
          if (['done', 'failed', 'cancelled'].includes(t.status)) setPushing(false);
        }
      } catch { /* ignore */ }
    };
    poll();
    const interval = setInterval(poll, 1500);
    return () => clearInterval(interval);
  }, [taskId]);

  // Auto-scroll log to bottom
  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [task?.log]);

  async function doPush() {
    if (!imageName || !tag || !sourceId) return;
    setError(null);
    setPushing(true);
    try {
      const extra = additionalTags.split(',').map(t => t.trim()).filter(Boolean);
      const result = await apiPost('/ghcr/push', {
        vmName, imageName, tag, sourceId, additionalTags: extra,
      }) as { taskId: string };
      setTaskId(result.taskId);
      const src = (sources ?? []).find(s => s.id === sourceId);
      const label = src
        ? `${src.registry}/${src.organization}/${imageName}:${tag}`
        : `${imageName}:${tag}`;
      onTaskStarted?.(result.taskId, label);
    } catch (err) {
      setError(String(err));
      setPushing(false);
    }
  }

  async function cancel() {
    if (taskId) await apiPost(`/ghcr/task/${taskId}/cancel`);
    setPushing(false);
  }

  const selectedSource = (sources ?? []).find(s => s.id === sourceId);
  const hasCredentials = settings?.github_token_set;
  const taskDone = task?.status === 'done';
  const taskFailed = task?.status === 'failed' || task?.status === 'cancelled';
  const canPush = Boolean(imageName && tag && sourceId && hasCredentials) && !pushing;

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4">
      <div className="bg-gray-900 border border-gray-700 rounded-xl shadow-2xl w-full max-w-lg">
        <div className="flex items-center justify-between px-5 py-4 border-b border-gray-800">
          <div>
            <h2 className="text-sm font-semibold text-gray-100">Push to GHCR</h2>
            <p className="text-[10px] text-gray-500 font-mono mt-0.5">{vmName}</p>
          </div>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300 text-lg leading-none" title={pushing ? 'Minimise — push continues in background' : 'Close'}>×</button>
        </div>

        <div className="p-5 space-y-4">
          {!hasCredentials && (
            <div className="p-2 bg-yellow-900/20 border border-yellow-800/40 rounded text-xs text-yellow-300">
              ⚠ GitHub Token not set. Configure it in Settings before pushing.
            </div>
          )}

          {/* Source */}
          <div>
            <label className="block text-xs text-gray-400 mb-1">Registry Source</label>
            {(sources ?? []).length === 0 ? (
              <p className="text-xs text-red-400">No GHCR sources configured. Add one in Settings → GHCR Sources.</p>
            ) : (
              <select
                value={sourceId}
                onChange={e => setSourceId(e.target.value)}
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 focus:outline-none focus:border-orange-500/60"
              >
                {(sources ?? []).map(s => (
                  <option key={s.id} value={s.id}>
                    {s.name} ({s.registry}/{s.organization}){s.is_default ? ' ★' : ''}
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* Image + tag */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs text-gray-400 mb-1">Image Name</label>
              <input value={imageName} onChange={e => setImageName(e.target.value)}
                placeholder="uitest-golden"
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1">Tag</label>
              <input value={tag} onChange={e => setTag(e.target.value)}
                placeholder="26.4.1-20260419"
                className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono"
              />
            </div>
          </div>

          {/* Additional tags */}
          <div>
            <label className="block text-xs text-gray-400 mb-1">
              Additional Tags <span className="text-gray-600">(comma-separated, optional)</span>
            </label>
            <input value={additionalTags} onChange={e => setAdditionalTags(e.target.value)}
              placeholder="e.g. 26.4.1-latest, latest"
              className="w-full bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-xs text-gray-200 placeholder-gray-600 focus:outline-none focus:border-orange-500/60 font-mono"
            />
          </div>

          {/* Preview */}
          {selectedSource && imageName && tag && (
            <div className="bg-gray-800/60 rounded p-2 text-[10px] font-mono text-gray-400">
              <div>↑ {selectedSource.registry}/{selectedSource.organization}/{imageName}:{tag}</div>
              {additionalTags.split(',').filter(t => t.trim()).map(t => (
                <div key={t.trim()} className="text-gray-600">
                  ↑ {selectedSource.registry}/{selectedSource.organization}/{imageName}:{t.trim()}
                </div>
              ))}
            </div>
          )}

          {error && (
            <div className="p-2 bg-red-900/20 border border-red-800/40 rounded text-xs text-red-300">{error}</div>
          )}

          {/* Live log */}
          {taskId && (
            <div>
              <div className="flex items-center justify-between mb-1">
                <span className={`text-[10px] font-medium ${
                  taskDone ? 'text-green-400' : taskFailed ? 'text-red-400' : 'text-orange-400 animate-pulse'
                }`}>
                  {taskDone ? '✓ Push complete' : taskFailed ? `✗ ${task?.error ?? 'Failed'}` : '⟳ Pushing…'}
                </span>
                {taskDone && selectedSource && (
                  <a href={`https://${selectedSource.registry}/${selectedSource.organization}/${imageName}`}
                    target="_blank" rel="noreferrer" className="text-[10px] text-blue-400 hover:underline">
                    View on GHCR ↗
                  </a>
                )}
              </div>
              <pre ref={logRef}
                className="bg-gray-950 border border-gray-800 rounded p-2 text-[10px] text-gray-400 font-mono overflow-auto max-h-48 whitespace-pre-wrap">
                {task?.log || '(waiting for output…)'}
              </pre>
            </div>
          )}
        </div>

        <div className="px-5 py-4 border-t border-gray-800 flex items-center justify-end gap-3">
          {pushing && !taskDone ? (
            <>
              <button onClick={cancel}
                className="text-xs px-3 py-1.5 bg-red-500/20 text-red-300 border border-red-500/40 rounded hover:bg-red-500/30">
                Cancel
              </button>
              <button onClick={onClose}
                className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200 hover:bg-gray-800 rounded">
                Minimise
              </button>
            </>
          ) : (
            <button onClick={onClose}
              className="text-xs px-3 py-1.5 text-gray-400 hover:text-gray-200 hover:bg-gray-800 rounded">
              {taskDone ? 'Close' : 'Cancel'}
            </button>
          )}
          {!taskDone && (
            <button onClick={doPush} disabled={!canPush}
              className="text-xs px-4 py-1.5 bg-orange-500/20 text-orange-300 border border-orange-500/40 rounded hover:bg-orange-500/30 disabled:opacity-40">
              {pushing ? 'Pushing…' : '↑ Push to GHCR'}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
