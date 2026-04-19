import { useState } from 'react';
import { useRecordings, useRecordStatus, MediaFile } from '../hooks/useAPI.ts';

function formatBytes(b: number) {
  if (b >= 1e9) return `${(b / 1e9).toFixed(1)} GB`;
  if (b >= 1e6) return `${(b / 1e6).toFixed(0)} MB`;
  return `${b} B`;
}

function formatDate(mtime: number) {
  return new Date(mtime).toLocaleString();
}

interface Props { vmId: string }

export function RecordingsTab({ vmId }: Props) {
  const { data: recordings, loading, error, refresh } = useRecordings(vmId);
  const { data: recStatus } = useRecordStatus(vmId);
  const [active, setActive] = useState<MediaFile | null>(null);

  const isRecording = recStatus?.recording ?? false;

  if (loading) return <div className="p-4 text-xs text-gray-500">Loading recordings…</div>;
  if (error)   return <div className="p-4 text-xs text-red-400">{error}</div>;

  if (!recordings?.length && !isRecording) {
    return (
      <div className="p-4 text-xs text-gray-600">
        No recordings yet. Use the <span className="text-gray-400">⏺</span> button in the VM header to start a recording.
      </div>
    );
  }

  const mediaUrl = (path: string) => `/api/media?path=${encodeURIComponent(path)}`;

  return (
    <div className="p-4 space-y-3">
      {isRecording && (
        <div className="flex items-center gap-2 px-3 py-2 bg-red-500/10 border border-red-500/30 rounded text-xs text-red-300">
          <span className="animate-pulse">●</span>
          <span>Recording in progress…</span>
          <span className="text-red-500/60 text-[10px] ml-auto truncate">{recStatus?.file?.split('/').pop()}</span>
        </div>
      )}
      <div className="flex items-center justify-between">
        <span className="text-xs text-gray-500">{(recordings ?? []).length} recording{(recordings?.length ?? 0) !== 1 ? 's' : ''}</span>
        <button onClick={refresh} className="text-[10px] text-gray-600 hover:text-gray-400">↻ Refresh</button>
      </div>

      {/* Video player */}
      {active && (
        <div className="bg-black rounded overflow-hidden border border-gray-800">
          <video
            key={active.path}
            src={mediaUrl(active.path)}
            controls
            className="w-full max-h-72"
          />
          <div className="flex items-center justify-between px-3 py-1.5 text-[10px] text-gray-500">
            <span className="truncate">{active.name}</span>
            <a
              href={mediaUrl(active.path)}
              download={active.name}
              className="text-blue-400 hover:text-blue-300 shrink-0 ml-2"
            >
              Download
            </a>
          </div>
        </div>
      )}

      {/* Recording list */}
      <div className="space-y-1">
        {(recordings ?? []).map(r => (
          <button
            key={r.path}
            onClick={() => setActive(a => a?.path === r.path ? null : r)}
            className={`w-full flex items-center gap-3 px-3 py-2 rounded text-xs text-left transition-colors ${
              active?.path === r.path
                ? 'bg-purple-900/30 border border-purple-700/50 text-purple-200'
                : 'bg-gray-900/40 border border-gray-800 text-gray-400 hover:bg-gray-800/50'
            }`}
          >
            <span className="text-lg leading-none">▶</span>
            <span className="flex-1 min-w-0 truncate">{r.name}</span>
            <span className="text-gray-600 shrink-0">{formatBytes(r.size)}</span>
            <span className="text-gray-700 shrink-0 text-[10px]">{formatDate(r.mtime)}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
