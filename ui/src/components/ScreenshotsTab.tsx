import { useState } from 'react';
import { useScreenshots, MediaFile } from '../hooks/useAPI.ts';

function formatDate(mtime: number) {
  return new Date(mtime).toLocaleString();
}

interface Props { vmId: string }

export function ScreenshotsTab({ vmId }: Props) {
  const { data: screenshots, loading, error, refresh } = useScreenshots(vmId);
  const [lightbox, setLightbox] = useState<MediaFile | null>(null);

  if (loading) return <div className="p-4 text-xs text-gray-500">Loading screenshots…</div>;
  if (error)   return <div className="p-4 text-xs text-red-400">{error}</div>;
  if (!screenshots?.length) {
    return (
      <div className="p-4 text-xs text-gray-600">
        No screenshots found. Screenshots captured by build scripts or Peekaboo appear here.
      </div>
    );
  }

  const mediaUrl = (path: string) => `/api/media?path=${encodeURIComponent(path)}`;

  return (
    <div className="p-4">
      <div className="flex items-center justify-between mb-3">
        <span className="text-xs text-gray-500">{screenshots.length} screenshot{screenshots.length !== 1 ? 's' : ''}</span>
        <button onClick={refresh} className="text-[10px] text-gray-600 hover:text-gray-400">↻ Refresh</button>
      </div>

      {/* Lightbox */}
      {lightbox && (
        <div
          className="fixed inset-0 z-50 bg-black/80 flex items-center justify-center p-4"
          onClick={() => setLightbox(null)}
        >
          <div className="max-w-4xl w-full" onClick={e => e.stopPropagation()}>
            <img
              src={mediaUrl(lightbox.path)}
              alt={lightbox.name}
              className="w-full h-auto rounded border border-gray-700 max-h-[80vh] object-contain"
            />
            <div className="flex items-center justify-between mt-2 text-xs text-gray-400">
              <span>{lightbox.name}</span>
              <div className="flex gap-3">
                <span className="text-gray-600">{formatDate(lightbox.mtime)}</span>
                <a
                  href={mediaUrl(lightbox.path)}
                  download={lightbox.name}
                  className="text-blue-400 hover:text-blue-300"
                >
                  Download
                </a>
                <button onClick={() => setLightbox(null)} className="text-gray-500 hover:text-gray-300">
                  Close
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Grid */}
      <div className="grid grid-cols-3 gap-2">
        {screenshots.map(s => (
          <button
            key={s.path}
            onClick={() => setLightbox(s)}
            className="group relative aspect-video bg-gray-900 rounded border border-gray-800 overflow-hidden hover:border-gray-600 transition-colors"
          >
            <img
              src={mediaUrl(s.path)}
              alt={s.name}
              className="w-full h-full object-cover"
              loading="lazy"
            />
            <div className="absolute inset-x-0 bottom-0 bg-black/60 px-1.5 py-1 text-[9px] text-gray-300 opacity-0 group-hover:opacity-100 transition-opacity truncate">
              {s.name}
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
