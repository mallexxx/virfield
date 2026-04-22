import { useState } from 'react';
import { useGet } from '../hooks/useAPI.ts';

interface FileEntry {
  category: 'log' | 'recording' | 'screenshot' | 'state' | 'result' | 'vm';
  path: string;
  name: string;
  size: number;
  mtime: number;
  label?: string;
}

const CATEGORY_LABELS: Record<FileEntry['category'], string> = {
  vm:         'VM',
  log:        'Logs',
  recording:  'Recordings',
  screenshot: 'Screenshots',
  state:      'State',
  result:     'Test Results',
};

const CATEGORY_COLORS: Record<FileEntry['category'], string> = {
  vm:         'text-yellow-300 bg-yellow-900/30',
  log:        'text-gray-400 bg-gray-800/60',
  recording:  'text-purple-300 bg-purple-900/30',
  screenshot: 'text-blue-300 bg-blue-900/30',
  state:      'text-orange-300 bg-orange-900/30',
  result:     'text-green-300 bg-green-900/30',
};

const ALL_CATEGORIES: FileEntry['category'][] = ['vm', 'log', 'recording', 'screenshot', 'state', 'result'];

function formatBytes(b: number) {
  if (b >= 1e9) return `${(b / 1e9).toFixed(1)} GB`;
  if (b >= 1e6) return `${(b / 1e6).toFixed(0)} MB`;
  if (b >= 1e3) return `${(b / 1e3).toFixed(0)} KB`;
  return `${b} B`;
}

function formatDate(ms: number) {
  return new Date(ms).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function fileIcon(category: FileEntry['category'], name: string) {
  if (category === 'vm') return name.endsWith('.img') ? '💿' : '⚙';
  if (category === 'recording') return '▶';
  if (category === 'screenshot') return '🖼';
  if (category === 'state') return '{}';
  if (category === 'result') {
    if (name.endsWith('.xcresult')) return '⬡';
    if (name.endsWith('.xml')) return '≡';
    if (name.endsWith('.log')) return '≡';
    return '≡';
  }
  return '≡';
}

function mediaUrl(path: string) {
  return `/api/media?path=${encodeURIComponent(path)}`;
}

export function FilesTab({ vmId }: { vmId: string }) {
  const { data: files, loading, error, refresh } = useGet<FileEntry[]>(`/vms/${vmId}/files`, [vmId], 10000);
  const [filter, setFilter] = useState<FileEntry['category'] | 'all'>('all');
  const [preview, setPreview] = useState<FileEntry | null>(null);

  if (loading) return <div className="p-4 text-xs text-gray-500">Loading files…</div>;
  if (error)   return <div className="p-4 text-xs text-red-400">{error}</div>;

  const allFiles = files ?? [];
  const visible = filter === 'all' ? allFiles : allFiles.filter(f => f.category === filter);

  const counts = Object.fromEntries(
    ALL_CATEGORIES.map(c => [c, allFiles.filter(f => f.category === c).length])
  ) as Record<FileEntry['category'], number>;

  return (
    <div className="flex flex-col h-96">
      {/* Toolbar */}
      <div className="flex items-center gap-1 px-3 py-1.5 border-b border-gray-800 bg-gray-900/80 flex-shrink-0 overflow-x-auto">
        <button
          onClick={() => setFilter('all')}
          className={`text-[10px] px-2 py-0.5 rounded shrink-0 ${filter === 'all' ? 'bg-gray-700 text-gray-200' : 'text-gray-500 hover:text-gray-300'}`}
        >All ({allFiles.length})</button>
        {ALL_CATEGORIES.filter(c => counts[c] > 0).map(c => (
          <button
            key={c}
            onClick={() => setFilter(c)}
            className={`text-[10px] px-2 py-0.5 rounded shrink-0 ${filter === c ? 'bg-gray-700 text-gray-200' : 'text-gray-500 hover:text-gray-300'}`}
          >{CATEGORY_LABELS[c]} ({counts[c]})</button>
        ))}
        <button onClick={refresh} className="text-[10px] text-gray-600 hover:text-gray-400 ml-auto shrink-0">↻</button>
      </div>

      {/* Preview */}
      {preview && (
        <div className="border-b border-gray-800 bg-black/60 flex-shrink-0">
          {preview.category === 'recording' ? (
            <video key={preview.path} src={mediaUrl(preview.path)} controls className="w-full max-h-48" />
          ) : (preview.category === 'screenshot' || preview.name.match(/\.(png|jpg|jpeg)$/i)) ? (
            <img src={mediaUrl(preview.path)} alt={preview.name} className="max-h-48 max-w-full mx-auto object-contain" />
          ) : (
            <div className="p-2 text-[10px] text-gray-500 italic">No preview available</div>
          )}
          <div className="flex items-center gap-2 px-3 py-1 text-[10px] text-gray-500 bg-gray-900/60">
            <span className="truncate font-mono flex-1" title={preview.path}>{preview.path}</span>
            <a href={mediaUrl(preview.path)} download={preview.name} className="text-blue-400 hover:text-blue-300 shrink-0">↓</a>
            <button onClick={() => setPreview(null)} className="text-gray-600 hover:text-gray-400 shrink-0">✕</button>
          </div>
        </div>
      )}

      {/* File list */}
      <div className="flex-1 overflow-y-auto">
        {visible.length === 0 ? (
          <div className="p-4 text-xs text-gray-600 text-center">No files found.</div>
        ) : (
          <div className="divide-y divide-gray-800/60">
            {visible.map(f => (
              <div
                key={f.path}
                onClick={() => setPreview(prev => prev?.path === f.path ? null : f)}
                className={`flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-gray-800/40 transition-colors ${
                  preview?.path === f.path ? 'bg-gray-800/60' : ''
                }`}
              >
                <span className="text-[10px] font-mono text-gray-600 shrink-0 w-4 text-center select-none">{fileIcon(f.category, f.name)}</span>
                <span className={`text-[9px] px-1 py-0.5 rounded shrink-0 font-medium select-none ${CATEGORY_COLORS[f.category]}`}>
                  {f.label ?? CATEGORY_LABELS[f.category]}
                </span>
                <span className="flex-1 min-w-0 truncate font-mono text-[11px] text-gray-300 select-text cursor-text" title={f.path}>{f.name}</span>
                <span className="text-[10px] text-gray-600 shrink-0 select-text cursor-text">{formatBytes(f.size)}</span>
                <span className="text-[10px] text-gray-700 shrink-0 select-text cursor-text">{formatDate(f.mtime)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
